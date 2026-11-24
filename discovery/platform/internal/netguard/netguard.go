// Package netguard validates outbound provider endpoints and builds HTTP
// clients that refuse unsafe destinations (SSRF protection).
//
// Rules:
//   - https only, unless insecure endpoints are explicitly allowed (local dev);
//   - the host must match the egress allow-list (exact host or "*.suffix");
//   - after DNS resolution, link-local and cloud metadata addresses are always
//     refused, and loopback/private addresses only when allow-listed literally;
//   - redirects are never followed;
//   - credentials embedded in URLs are rejected.
package netguard

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// Policy is the outbound policy.
type Policy struct {
	Allowlist     []string // lower-case hosts or "*.suffix"; empty denies all
	AllowInsecure bool     // allow http:// and loopback (development only)
}

// ErrDenied wraps every policy refusal.
var ErrDenied = errors.New("endpoint denied by egress policy")

func denied(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrDenied, fmt.Sprintf(format, a...))
}

// ValidateURL checks raw against the policy without network access.
func (p Policy) ValidateURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return nil, denied("not an absolute URL")
	}
	if u.User != nil {
		return nil, denied("credentials must not be embedded in the URL")
	}
	switch u.Scheme {
	case "https":
	case "http":
		if !p.AllowInsecure {
			return nil, denied("https is required")
		}
	default:
		return nil, denied("scheme %q is not allowed", u.Scheme)
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return nil, denied("query and fragment are not allowed in an endpoint")
	}
	host := strings.ToLower(u.Hostname())
	if !p.hostAllowed(host) {
		return nil, denied("host %q is not in OBS_EGRESS_ALLOWLIST", host)
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		if err := p.checkIP(ip, true); err != nil {
			return nil, err
		}
	}
	return u, nil
}

func (p Policy) hostAllowed(host string) bool {
	for _, a := range p.Allowlist {
		if a == host {
			return true
		}
		if strings.HasPrefix(a, "*.") && strings.HasSuffix(host, a[1:]) && len(host) > len(a)-1 {
			return true
		}
	}
	return false
}

var metadataAddrs = []netip.Addr{
	netip.MustParseAddr("169.254.169.254"), // AWS, Azure, GCP, OpenStack metadata
	netip.MustParseAddr("fd00:ec2::254"),   // AWS IPv6 metadata
	netip.MustParseAddr("100.100.100.200"), // Alibaba Cloud metadata
}

// checkIP enforces address rules. literal is true when the operator wrote the
// IP into the allow-list, which permits private ranges (on-premises API
// servers commonly have private addresses).
func (p Policy) checkIP(ip netip.Addr, literal bool) error {
	ip = ip.Unmap()
	for _, m := range metadataAddrs {
		if ip == m {
			return denied("cloud metadata address %s is never allowed", ip)
		}
	}
	if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return denied("address %s is not routable for provider APIs", ip)
	}
	if ip.IsLoopback() && !(p.AllowInsecure || literal) {
		return denied("loopback address %s is not allowed", ip)
	}
	return nil
}

// HTTPClient returns a client that re-checks every resolved address at dial
// time (defeating DNS rebinding), never follows redirects and applies
// timeouts. tlsConfig may be nil.
func (p Policy) HTTPClient(timeout time.Duration, tlsConfig *tls.Config) *http.Client {
	dialer := p.Dialer(10 * time.Second)
	if tlsConfig == nil {
		tlsConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	tr := &http.Transport{
		Proxy:                 nil, // provider egress goes direct unless a proxy adapter is added
		DialContext:           dialer.DialContext,
		TLSClientConfig:       tlsConfig,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: timeout,
		MaxIdleConnsPerHost:   4,
		IdleConnTimeout:       60 * time.Second,
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: tr,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// ValidateHost checks a host name or IP literal (for non-HTTP protocols such
// as database wire protocols) against the allow-list without network access.
func (p Policy) ValidateHost(host string) error {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" || strings.ContainsAny(host, "/@?# ") {
		return denied("invalid host")
	}
	if !p.hostAllowed(host) {
		return denied("host %q is not in OBS_EGRESS_ALLOWLIST", host)
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		if err := p.checkIP(ip, true); err != nil {
			return err
		}
	}
	return nil
}

// Dialer returns a TCP dialer that re-checks every resolved address at dial
// time, for drivers that accept a custom dialer (pgx, go-ora).
func (p Policy) Dialer(timeout time.Duration) *net.Dialer {
	return &net.Dialer{
		Timeout:   timeout,
		KeepAlive: 30 * time.Second,
		Control: func(network, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return denied("bad address %q", address)
			}
			ip, err := netip.ParseAddr(host)
			if err != nil {
				return denied("unresolved address %q", host)
			}
			literal := false
			for _, a := range p.Allowlist {
				if a == ip.Unmap().String() {
					literal = true
				}
			}
			return p.checkIP(ip, literal)
		},
	}
}
