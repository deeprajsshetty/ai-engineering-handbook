// Package kubeclient is a minimal, read-only Kubernetes API client built on
// net/http. It supports only GET with bearer-token authentication and the
// API server's limit/continue pagination. It deliberately avoids client-go:
// the platform needs a handful of list calls, and a small surface is easier to
// review, contract-test and keep SSRF-safe (requests go through netguard).
package kubeclient

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/deeprajsshetty/observability-platform/platform/internal/netguard"
	"github.com/deeprajsshetty/observability-platform/platform/internal/retry"
	"github.com/deeprajsshetty/observability-platform/platform/internal/secrets"
)

// MaxResponseBytes bounds one API response.
const MaxResponseBytes = 32 << 20

// Errors.
var (
	ErrUnauthorized = errors.New("kubernetes API rejected the credential (401)")
	ErrForbidden    = errors.New("kubernetes API denied the request (403); check the read-only ClusterRole")
	ErrNotFound     = errors.New("kubernetes API resource not found (404)")
	ErrExpired      = errors.New("kubernetes list continue token expired (410)")
)

// Client talks to one API server.
type Client struct {
	base  *url.URL
	token secrets.Value
	http  *http.Client
	retry retry.Policy
}

// Options configures a client.
type Options struct {
	Server        string
	Token         secrets.Value
	CACertPEM     string
	Policy        netguard.Policy
	Timeout       time.Duration
	Retry         retry.Policy
	HTTPTransport http.RoundTripper // tests only; bypasses netguard dialing
}

// New validates the endpoint against the egress policy and builds a client.
func New(o Options) (*Client, error) {
	u, err := o.Policy.ValidateURL(strings.TrimRight(o.Server, "/"))
	if err != nil {
		return nil, err
	}
	if o.Token.Empty() {
		return nil, errors.New("a bearer token credential is required")
	}
	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if o.CACertPEM != "" {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM([]byte(o.CACertPEM)) {
			return nil, errors.New("caCertificate is not a valid PEM certificate")
		}
		tlsCfg.RootCAs = pool
	}
	if o.Timeout <= 0 {
		o.Timeout = 30 * time.Second
	}
	hc := o.Policy.HTTPClient(o.Timeout, tlsCfg)
	if o.HTTPTransport != nil {
		hc.Transport = o.HTTPTransport
	}
	if o.Retry.MaxAttempts == 0 {
		o.Retry = retry.Default
	}
	return &Client{base: u, token: o.Token, http: hc, retry: o.Retry}, nil
}

// Get fetches path (with optional query) into out, retrying transient errors.
func (c *Client) Get(ctx context.Context, path string, q url.Values, out any) error {
	return retry.Do(ctx, c.retry, func(ctx context.Context) error {
		return c.once(ctx, path, q, out)
	}, nil)
}

func (c *Client) once(ctx context.Context, path string, q url.Values, out any) error {
	u := *c.base
	u.Path = strings.TrimRight(c.base.Path, "/") + path
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token.Reveal())
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "obsplatform-discovery/0.1")
	resp, err := c.http.Do(req)
	if err != nil {
		if errors.Is(err, netguard.ErrDenied) || ctx.Err() != nil {
			return err
		}
		return retry.Retryable(fmt.Errorf("kubernetes API request failed: %w", err), 0)
	}
	defer resp.Body.Close()
	body := io.LimitReader(resp.Body, MaxResponseBytes+1)
	switch {
	case resp.StatusCode == http.StatusOK:
		b, err := io.ReadAll(body)
		if err != nil {
			return retry.Retryable(fmt.Errorf("reading kubernetes response: %w", err), 0)
		}
		if len(b) > MaxResponseBytes {
			return fmt.Errorf("kubernetes response exceeds %d bytes; lower pageSize", MaxResponseBytes)
		}
		if err := json.Unmarshal(b, out); err != nil {
			return fmt.Errorf("decoding kubernetes response: %w", err)
		}
		return nil
	case resp.StatusCode == http.StatusUnauthorized:
		return ErrUnauthorized
	case resp.StatusCode == http.StatusForbidden:
		return fmt.Errorf("%w: GET %s", ErrForbidden, path)
	case resp.StatusCode == http.StatusNotFound:
		return fmt.Errorf("%w: GET %s", ErrNotFound, path)
	case resp.StatusCode == http.StatusGone:
		return ErrExpired
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
		ra, _ := strconv.Atoi(resp.Header.Get("Retry-After"))
		return retry.Retryable(fmt.Errorf("kubernetes API returned %d for %s", resp.StatusCode, path), time.Duration(ra)*time.Second)
	default:
		return fmt.Errorf("kubernetes API returned %d for %s", resp.StatusCode, path)
	}
}

// ListPage is the envelope of a Kubernetes list response.
type ListPage struct {
	Metadata struct {
		Continue string `json:"continue"`
	} `json:"metadata"`
	Items []json.RawMessage `json:"items"`
}

// List pages through a collection with limit/continue and calls fn per page.
// A 410 Gone (continue token expired) is returned to the caller: restarting
// the list could double-count, and the job is safely re-runnable.
func (c *Client) List(ctx context.Context, path string, limit int, fn func(items []json.RawMessage) error) error {
	cont := ""
	for {
		q := url.Values{"limit": {strconv.Itoa(limit)}}
		if cont != "" {
			q.Set("continue", cont)
		}
		var page ListPage
		if err := c.Get(ctx, path, q, &page); err != nil {
			return err
		}
		if err := fn(page.Items); err != nil {
			return err
		}
		if page.Metadata.Continue == "" {
			return nil
		}
		cont = page.Metadata.Continue
	}
}

// ObjectMeta is the subset of metadata the platform reads.
type ObjectMeta struct {
	Name            string            `json:"name"`
	Namespace       string            `json:"namespace"`
	UID             string            `json:"uid"`
	Labels          map[string]string `json:"labels"`
	OwnerReferences []struct {
		Kind string `json:"kind"`
		Name string `json:"name"`
		UID  string `json:"uid"`
	} `json:"ownerReferences"`
	CreationTimestamp string `json:"creationTimestamp"`
}

// ParseQuantity converts a Kubernetes resource quantity (for example "250m",
// "1500000n", "2", "512Ki", "1Gi", "1e3") to a float64 in base units (cores
// or bytes).
func ParseQuantity(s string) (float64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, errors.New("empty quantity")
	}
	suffixes := []struct {
		s string
		m float64
	}{
		{"Ki", 1 << 10}, {"Mi", 1 << 20}, {"Gi", 1 << 30}, {"Ti", 1 << 40}, {"Pi", 1 << 50}, {"Ei", 1 << 60},
		{"n", 1e-9}, {"u", 1e-6}, {"m", 1e-3}, {"k", 1e3}, {"M", 1e6}, {"G", 1e9}, {"T", 1e12}, {"P", 1e15}, {"E", 1e18},
	}
	for _, x := range suffixes {
		if strings.HasSuffix(s, x.s) {
			v, err := strconv.ParseFloat(strings.TrimSuffix(s, x.s), 64)
			if err != nil {
				return 0, fmt.Errorf("invalid quantity %q", s)
			}
			return v * x.m, nil
		}
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid quantity %q", s)
	}
	return v, nil
}
