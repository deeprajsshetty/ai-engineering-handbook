// Package internal_test holds unit tests for the small security- and
// reliability-critical helpers: config validation, secret references, egress
// policy, authentication, rate limiting, retry and cursor encoding.
package internal_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/deeprajsshetty/observability-platform/platform/internal/auth"
	"github.com/deeprajsshetty/observability-platform/platform/internal/config"
	"github.com/deeprajsshetty/observability-platform/platform/internal/httpx"
	"github.com/deeprajsshetty/observability-platform/platform/internal/kubeclient"
	"github.com/deeprajsshetty/observability-platform/platform/internal/netguard"
	"github.com/deeprajsshetty/observability-platform/platform/internal/retry"
	"github.com/deeprajsshetty/observability-platform/platform/internal/secrets"
	"github.com/deeprajsshetty/observability-platform/platform/internal/store"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestConfigValidation(t *testing.T) {
	base := map[string]string{"OBS_DATABASE_URL": "postgres://u:supersecret@db:5432/obs?sslmode=require"}
	c, err := config.LoadFrom(env(base))
	if err != nil {
		t.Fatal(err)
	}
	if !c.Roles["api"] || !c.Roles["worker"] || c.AuthMode != "dev" {
		t.Fatalf("defaults: %+v", c)
	}
	if red := config.RedactURL(c.DatabaseURL); strings.Contains(red, "supersecret") || strings.Contains(red, "sslmode") {
		t.Fatalf("password leaked in redacted URL: %s", red)
	}
	cases := map[string]map[string]string{
		"missing db":             {},
		"dev auth in production": {"OBS_DATABASE_URL": "postgres://x@y/z", "OBS_ENV": "production"},
		"insecure in production": {"OBS_DATABASE_URL": "postgres://x@y/z", "OBS_ENV": "production", "OBS_AUTH_MODE": "oidc", "OBS_OIDC_ISSUER": "https://i", "OBS_OIDC_AUDIENCE": "a", "OBS_ALLOW_INSECURE_ENDPOINTS": "true"},
		"bad duration":           {"OBS_DATABASE_URL": "postgres://x@y/z", "OBS_METRICS_INTERVAL": "soon"},
		"bad role":               {"OBS_DATABASE_URL": "postgres://x@y/z", "OBS_ROLES": "api,cron"},
		"oidc without issuer":    {"OBS_DATABASE_URL": "postgres://x@y/z", "OBS_AUTH_MODE": "oidc"},
		"non-postgres url":       {"OBS_DATABASE_URL": "mysql://x@y/z"},
	}
	for name, e := range cases {
		if _, err := config.LoadFrom(env(e)); err == nil {
			t.Errorf("%s: expected error", name)
		} else if strings.Contains(err.Error(), "supersecret") {
			t.Errorf("%s: error leaks secret", name)
		}
	}
}

func TestSecretReferences(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "k8s-token"), []byte("s3cr3t\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := &secrets.Resolver{Dir: dir, Getenv: env(map[string]string{"OBS_SECRET_A": "va", "OBS_DATABASE_URL": "postgres://p"})}
	if v, err := r.Resolve("env:OBS_SECRET_A"); err != nil || v.Reveal() != "va" {
		t.Fatalf("env ref: %v", err)
	}
	if v, err := r.Resolve("file:k8s-token"); err != nil || v.Reveal() != "s3cr3t" {
		t.Fatalf("file ref: %v", err)
	}
	v, _ := r.Resolve("env:OBS_SECRET_A")
	if s := v.String(); s != "[REDACTED]" {
		t.Fatal("Value must redact when printed")
	}
	for _, bad := range []string{"env:OBS_DATABASE_URL", "env:HOME", "file:../etc/passwd", "file:/etc/passwd", "vault:x", "plain"} {
		if _, err := r.Resolve(bad); err == nil {
			t.Errorf("%s must be rejected", bad)
		}
	}
	if _, err := r.Resolve("env:OBS_SECRET_MISSING"); !errors.Is(err, secrets.ErrNotFound) {
		t.Fatalf("missing secret: %v", err)
	}
	for _, k := range []string{"password", "apiToken", "client_secret", "accessKeyId"} {
		if !secrets.LooksSecret(k) {
			t.Errorf("%s should look secret", k)
		}
	}
}

func TestEgressPolicy(t *testing.T) {
	p := netguard.Policy{Allowlist: []string{"*.eks.example.com", "10.0.0.5"}}
	ok := []string{"https://abc.eks.example.com", "https://10.0.0.5:6443"}
	bad := []string{"https://eks.example.com", "https://abc.eks.example.com.evil.io", "http://abc.eks.example.com",
		"https://169.254.169.254", "https://127.0.0.1", "ftp://abc.eks.example.com", "https://abc.eks.example.com/?x=1"}
	for _, u := range ok {
		if _, err := p.ValidateURL(u); err != nil {
			t.Errorf("%s should be allowed: %v", u, err)
		}
	}
	for _, u := range bad {
		if _, err := p.ValidateURL(u); !errors.Is(err, netguard.ErrDenied) {
			t.Errorf("%s should be denied", u)
		}
	}
	if _, err := (netguard.Policy{}).ValidateURL("https://anything.example.com"); err == nil {
		t.Error("empty allow-list must deny everything")
	}
}

func TestDevAuthenticator(t *testing.T) {
	a, err := auth.NewDevAuthenticator("")
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "/", nil)
	if _, err := a.Authenticate(r); err == nil {
		t.Fatal("missing token accepted")
	}
	r.Header.Set("Authorization", "Bearer dev-viewer")
	p, err := a.Authenticate(r)
	if err != nil || p.TenantID != "demo" || p.Role != auth.RoleViewer {
		t.Fatalf("viewer: %+v %v", p, err)
	}
	if p.Can(auth.PermDiscoveryRun) || !p.Can(auth.PermTopologyRead) {
		t.Fatal("viewer permissions wrong")
	}
	r.Header.Set("Authorization", "Bearer dev-viewerX")
	if _, err := a.Authenticate(r); err == nil {
		t.Fatal("near-miss token accepted")
	}
	for _, spec := range []string{"short=demo:x:admin", "longtoken1=demo:x:root", "longtoken1=BAD TENANT:x:admin", "garbage"} {
		if _, err := auth.NewDevAuthenticator(spec); err == nil {
			t.Errorf("spec %q should be rejected", spec)
		}
	}
}

func TestRateLimiter(t *testing.T) {
	l := httpx.NewRateLimiter(1, 3)
	for i := 0; i < 3; i++ {
		if ok, _ := l.Allow("k"); !ok {
			t.Fatalf("burst request %d refused", i)
		}
	}
	ok, wait := l.Allow("k")
	if ok || wait <= 0 || wait > time.Second {
		t.Fatalf("expected refusal with wait <= 1s, got %v %v", ok, wait)
	}
	if ok, _ := l.Allow("other"); !ok {
		t.Fatal("buckets must be per key")
	}
}

func TestRetry(t *testing.T) {
	p := retry.Policy{MaxAttempts: 4, BaseDelay: time.Millisecond, MaxDelay: 2 * time.Millisecond}
	n := 0
	err := retry.Do(context.Background(), p, func(context.Context) error {
		n++
		if n < 3 {
			return retry.Retryable(errors.New("busy"), 0)
		}
		return nil
	}, nil)
	if err != nil || n != 3 {
		t.Fatalf("retryable: n=%d err=%v", n, err)
	}
	n = 0
	perm := errors.New("permanent")
	if err := retry.Do(context.Background(), p, func(context.Context) error { n++; return perm }, nil); !errors.Is(err, perm) || n != 1 {
		t.Fatalf("non-retryable must not retry: n=%d", n)
	}
	n = 0
	_ = retry.Do(context.Background(), p, func(context.Context) error { n++; return retry.Retryable(errors.New("x"), 0) }, nil)
	if n != 4 {
		t.Fatalf("attempts not capped: %d", n)
	}
	for i := 1; i < 10; i++ {
		if d := retry.Backoff(p, i); d < 0 || d > p.MaxDelay {
			t.Fatalf("backoff %d out of range: %v", i, d)
		}
	}
}

func TestCursorAndQuantity(t *testing.T) {
	c := store.EncodeCursor("a", "b")
	keys, err := store.DecodeCursor(c, 2)
	if err != nil || keys[0] != "a" || keys[1] != "b" {
		t.Fatalf("cursor roundtrip: %v %v", keys, err)
	}
	if _, err := store.DecodeCursor(c, 1); !errors.Is(err, store.ErrInvalidCursor) {
		t.Fatal("arity mismatch must be invalid")
	}
	for in, want := range map[string]float64{"250m": 0.25, "2": 2, "1Ki": 1024, "1Gi": 1 << 30, "500000n": 0.0005, "1e3": 1000, "1M": 1e6} {
		got, err := kubeclient.ParseQuantity(in)
		if err != nil || got != want {
			t.Errorf("ParseQuantity(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	if _, err := kubeclient.ParseQuantity("abc"); err == nil {
		t.Error("invalid quantity accepted")
	}
}
