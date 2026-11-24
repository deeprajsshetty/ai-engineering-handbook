// Package auth defines the authentication seam and the authorization model.
//
// The MVP ships one Authenticator: DevAuthenticator, static bearer tokens for
// local development. It is refused in production by configuration. The
// production seam is the Authenticator interface: an OIDC implementation
// validates a JWT access token (or a BFF session cookie) against the issuer's
// keys and maps claims to a Principal. It is not implemented in the MVP; see
// docs/adr/0006-auth-dev-mode-and-oidc-seam.md.
package auth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
)

// Permission is an action on a resource class.
type Permission string

const (
	PermConnectionsRead  Permission = "connections:read"
	PermConnectionsWrite Permission = "connections:write"
	PermDiscoveryRead    Permission = "discovery:read"
	PermDiscoveryRun     Permission = "discovery:run"
	PermTopologyRead     Permission = "topology:read"
	PermMetricsRead      Permission = "metrics:read"
	PermMetricsCollect   Permission = "metrics:collect"
	PermAuditRead        Permission = "audit:read"
)

// Role is a named set of permissions.
type Role string

const (
	RoleViewer   Role = "viewer"
	RoleOperator Role = "operator"
	RoleAdmin    Role = "admin"
)

var rolePerms = map[Role][]Permission{
	RoleViewer:   {PermConnectionsRead, PermDiscoveryRead, PermTopologyRead, PermMetricsRead},
	RoleOperator: {PermConnectionsRead, PermDiscoveryRead, PermTopologyRead, PermMetricsRead, PermConnectionsWrite, PermDiscoveryRun, PermMetricsCollect},
	RoleAdmin:    {PermConnectionsRead, PermDiscoveryRead, PermTopologyRead, PermMetricsRead, PermConnectionsWrite, PermDiscoveryRun, PermMetricsCollect, PermAuditRead},
}

// ValidRole reports whether r is known.
func ValidRole(r string) bool { _, ok := rolePerms[Role(r)]; return ok }

// Principal is an authenticated caller. TenantID always comes from the
// credential, never from the request, so a caller cannot select a tenant.
type Principal struct {
	Subject  string `json:"subject"`
	TenantID string `json:"tenantId"`
	Role     Role   `json:"role"`
	AuthMode string `json:"authMode"`
}

// Can reports whether the principal has p.
func (p Principal) Can(perm Permission) bool {
	for _, x := range rolePerms[p.Role] {
		if x == perm {
			return true
		}
	}
	return false
}

// Permissions lists the principal's permissions, sorted.
func (p Principal) Permissions() []string {
	out := make([]string, 0, len(rolePerms[p.Role]))
	for _, x := range rolePerms[p.Role] {
		out = append(out, string(x))
	}
	sort.Strings(out)
	return out
}

// RateKey identifies the principal for rate limiting.
func (p Principal) RateKey() string { return p.TenantID + "/" + p.Subject }

// Errors returned by authenticators.
var (
	ErrUnauthenticated = errors.New("missing or invalid credentials")
)

// Authenticator turns a request into a Principal.
type Authenticator interface {
	Authenticate(r *http.Request) (Principal, error)
	Mode() string
}

type ctxKey struct{}

// WithPrincipal stores p in ctx.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, ctxKey{}, p)
}

// FromContext returns the principal stored by middleware.
func FromContext(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(ctxKey{}).(Principal)
	return p, ok
}

// DefaultDevTokens are the documented local-development tokens. They grant
// access only to the local demo and are rejected in production mode.
const DefaultDevTokens = "dev-admin=demo:demo-admin:admin;dev-operator=demo:demo-operator:operator;dev-viewer=demo:demo-viewer:viewer;dev-acme-admin=acme:acme-admin:admin"

var tenantPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,62}$`)

// ValidTenant reports whether t is a valid tenant identifier.
func ValidTenant(t string) bool { return tenantPattern.MatchString(t) }

type devEntry struct {
	hash      [32]byte
	principal Principal
}

// DevAuthenticator accepts static bearer tokens.
type DevAuthenticator struct{ entries []devEntry }

// NewDevAuthenticator parses "token=tenant:subject:role;..." specs.
func NewDevAuthenticator(spec string) (*DevAuthenticator, error) {
	if strings.TrimSpace(spec) == "" {
		spec = DefaultDevTokens
	}
	a := &DevAuthenticator{}
	for _, item := range strings.Split(spec, ";") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		tok, rest, ok := strings.Cut(item, "=")
		parts := strings.Split(rest, ":")
		if !ok || len(tok) < 8 || len(parts) != 3 {
			return nil, fmt.Errorf("OBS_DEV_TOKENS entry must be token=tenant:subject:role with a token of at least 8 characters")
		}
		if !ValidTenant(parts[0]) || parts[1] == "" || !ValidRole(parts[2]) {
			return nil, fmt.Errorf("OBS_DEV_TOKENS entry has an invalid tenant, subject or role")
		}
		a.entries = append(a.entries, devEntry{
			hash:      sha256.Sum256([]byte(tok)),
			principal: Principal{Subject: parts[1], TenantID: parts[0], Role: Role(parts[2]), AuthMode: "dev"},
		})
	}
	if len(a.entries) == 0 {
		return nil, errors.New("OBS_DEV_TOKENS defines no tokens")
	}
	return a, nil
}

// Mode returns "dev".
func (a *DevAuthenticator) Mode() string { return "dev" }

// Authenticate checks the bearer token in constant time per entry.
func (a *DevAuthenticator) Authenticate(r *http.Request) (Principal, error) {
	h := r.Header.Get("Authorization")
	tok, ok := strings.CutPrefix(h, "Bearer ")
	if !ok || tok == "" {
		return Principal{}, ErrUnauthenticated
	}
	sum := sha256.Sum256([]byte(strings.TrimSpace(tok)))
	var found *Principal
	for i := range a.entries {
		if subtle.ConstantTimeCompare(sum[:], a.entries[i].hash[:]) == 1 {
			found = &a.entries[i].principal
		}
	}
	if found == nil {
		return Principal{}, ErrUnauthenticated
	}
	return *found, nil
}

// OIDCNotImplemented is returned at start-up when OBS_AUTH_MODE=oidc.
var OIDCNotImplemented = errors.New("OBS_AUTH_MODE=oidc: the OIDC authenticator is a defined seam but not implemented in this MVP; see docs/adr/0006-auth-dev-mode-and-oidc-seam.md")
