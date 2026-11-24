// Package config loads and validates process configuration from environment
// variables. Validation fails fast at start-up; secret values (the database
// URL password) are never included in String output or logs.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config is the full process configuration. See docs/operations/configuration.md.
type Config struct {
	Env             string // development | production
	HTTPAddr        string
	DatabaseURL     string
	StoreDriver     string // postgres | oracle
	DBMaxConns      int32
	Roles           map[string]bool // api, worker
	WebDir          string
	LogLevel        string
	LogFormat       string
	ShutdownTimeout time.Duration

	AuthMode       string // dev | oidc
	DevTokens      string
	OIDCIssuer     string
	OIDCAudience   string
	RateLimitRPS   float64
	RateLimitBurst int
	MaxBodyBytes   int64

	DiscoveryWorkers      int
	DiscoveryJobTimeout   time.Duration
	DiscoveryPageTimeout  time.Duration
	DiscoveryMaxResources int
	DiscoveryMaxAttempts  int
	DiscoveryPollInterval time.Duration
	JobLeaseTimeout       time.Duration
	MaxJobEvents          int

	MetricsInterval    time.Duration
	MetricsBackfill    time.Duration
	MetricsConcurrency int

	MetricRetention    time.Duration
	TopologyPurgeAfter time.Duration
	JobRetention       time.Duration
	AuditRetention     time.Duration
	RetentionInterval  time.Duration

	FreshnessWindow time.Duration
	StaleAfter      time.Duration

	EgressAllowlist        []string
	AllowInsecureEndpoints bool
	SecretDir              string
}

// IsProduction reports whether production safeguards apply.
func (c Config) IsProduction() bool { return c.Env == "production" }

// Load reads configuration from the environment.
func Load() (Config, error) { return LoadFrom(os.Getenv) }

// LoadFrom reads configuration using getenv; used by tests.
func LoadFrom(getenv func(string) string) (Config, error) {
	l := loader{get: getenv}
	c := Config{
		Env:             l.str("OBS_ENV", "development"),
		HTTPAddr:        l.str("OBS_HTTP_ADDR", ":8080"),
		DatabaseURL:     l.str("OBS_DATABASE_URL", ""),
		StoreDriver:     l.str("OBS_STORE_DRIVER", "postgres"),
		DBMaxConns:      int32(l.integer("OBS_DB_MAX_CONNS", 10, 1, 200)),
		WebDir:          l.str("OBS_WEB_DIR", ""),
		LogLevel:        l.str("OBS_LOG_LEVEL", "info"),
		LogFormat:       l.str("OBS_LOG_FORMAT", "json"),
		ShutdownTimeout: l.dur("OBS_SHUTDOWN_TIMEOUT", 20*time.Second),

		AuthMode:       l.str("OBS_AUTH_MODE", "dev"),
		DevTokens:      l.str("OBS_DEV_TOKENS", ""),
		OIDCIssuer:     l.str("OBS_OIDC_ISSUER", ""),
		OIDCAudience:   l.str("OBS_OIDC_AUDIENCE", ""),
		RateLimitRPS:   l.float("OBS_RATE_LIMIT_RPS", 20),
		RateLimitBurst: l.integer("OBS_RATE_LIMIT_BURST", 40, 1, 10000),
		MaxBodyBytes:   int64(l.integer("OBS_MAX_BODY_BYTES", 1<<20, 1024, 16<<20)),

		DiscoveryWorkers:      l.integer("OBS_DISCOVERY_WORKERS", 2, 1, 64),
		DiscoveryJobTimeout:   l.dur("OBS_DISCOVERY_JOB_TIMEOUT", 10*time.Minute),
		DiscoveryPageTimeout:  l.dur("OBS_DISCOVERY_PAGE_TIMEOUT", 30*time.Second),
		DiscoveryMaxResources: l.integer("OBS_DISCOVERY_MAX_RESOURCES", 50000, 1, 1000000),
		DiscoveryMaxAttempts:  l.integer("OBS_DISCOVERY_MAX_ATTEMPTS", 3, 1, 10),
		DiscoveryPollInterval: l.dur("OBS_DISCOVERY_POLL_INTERVAL", time.Second),
		JobLeaseTimeout:       l.dur("OBS_JOB_LEASE_TIMEOUT", 30*time.Second),
		MaxJobEvents:          l.integer("OBS_MAX_JOB_EVENTS", 200, 10, 10000),

		MetricsInterval:    l.dur("OBS_METRICS_INTERVAL", 60*time.Second),
		MetricsBackfill:    l.dur("OBS_METRICS_BACKFILL", time.Hour),
		MetricsConcurrency: l.integer("OBS_METRICS_CONCURRENCY", 4, 1, 64),

		MetricRetention:    l.dur("OBS_METRIC_RETENTION", 7*24*time.Hour),
		TopologyPurgeAfter: l.dur("OBS_TOPOLOGY_PURGE_AFTER", 7*24*time.Hour),
		JobRetention:       l.dur("OBS_JOB_RETENTION", 30*24*time.Hour),
		AuditRetention:     l.dur("OBS_AUDIT_RETENTION", 90*24*time.Hour),
		RetentionInterval:  l.dur("OBS_RETENTION_INTERVAL", time.Hour),

		FreshnessWindow: l.dur("OBS_FRESHNESS_WINDOW", time.Hour),
		StaleAfter:      l.dur("OBS_STALE_AFTER", 24*time.Hour),

		EgressAllowlist:        l.list("OBS_EGRESS_ALLOWLIST"),
		AllowInsecureEndpoints: l.boolean("OBS_ALLOW_INSECURE_ENDPOINTS", false),
		SecretDir:              l.str("OBS_SECRET_DIR", "/var/run/secrets/obs"),
	}
	c.Roles = map[string]bool{}
	for _, r := range strings.Split(l.str("OBS_ROLES", "api,worker"), ",") {
		if r = strings.TrimSpace(r); r != "" {
			c.Roles[r] = true
		}
	}
	if err := errors.Join(append(l.errs, c.validate()...)...); err != nil {
		return Config{}, err
	}
	return c, nil
}

func (c Config) validate() []error {
	var errs []error
	add := func(format string, a ...any) { errs = append(errs, fmt.Errorf(format, a...)) }
	if c.Env != "development" && c.Env != "production" {
		add("OBS_ENV must be development or production")
	}
	switch c.StoreDriver {
	case "postgres":
		if c.DatabaseURL == "" {
			add("OBS_DATABASE_URL is required (for example postgres://obs@localhost:5432/obs)")
		} else if u, err := url.Parse(c.DatabaseURL); err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
			add("OBS_DATABASE_URL must be a postgres:// URL when OBS_STORE_DRIVER=postgres")
		}
	case "oracle":
		if c.DatabaseURL == "" {
			add("OBS_DATABASE_URL is required (for example oracle://obs@db.example:1521/OBSPDB)")
		} else if u, err := url.Parse(c.DatabaseURL); err != nil || u.Scheme != "oracle" || u.Host == "" {
			add("OBS_DATABASE_URL must be an oracle://user@host:port/service URL when OBS_STORE_DRIVER=oracle")
		}
	default:
		add("OBS_STORE_DRIVER must be postgres or oracle")
	}
	for r := range c.Roles {
		if r != "api" && r != "worker" {
			add("OBS_ROLES contains unknown role %q (allowed: api, worker)", r)
		}
	}
	if len(c.Roles) == 0 {
		add("OBS_ROLES must include api and/or worker")
	}
	switch c.AuthMode {
	case "dev":
		if c.IsProduction() {
			add("OBS_AUTH_MODE=dev is not allowed when OBS_ENV=production")
		}
	case "oidc":
		if c.OIDCIssuer == "" || c.OIDCAudience == "" {
			add("OBS_AUTH_MODE=oidc requires OBS_OIDC_ISSUER and OBS_OIDC_AUDIENCE")
		}
	default:
		add("OBS_AUTH_MODE must be dev or oidc")
	}
	if c.IsProduction() && c.AllowInsecureEndpoints {
		add("OBS_ALLOW_INSECURE_ENDPOINTS=true is not allowed when OBS_ENV=production")
	}
	if c.LogFormat != "json" && c.LogFormat != "text" {
		add("OBS_LOG_FORMAT must be json or text")
	}
	switch c.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		add("OBS_LOG_LEVEL must be debug, info, warn or error")
	}
	if c.RateLimitRPS <= 0 {
		add("OBS_RATE_LIMIT_RPS must be positive")
	}
	if c.MetricsInterval < 5*time.Second {
		add("OBS_METRICS_INTERVAL must be at least 5s")
	}
	if c.StaleAfter <= c.FreshnessWindow {
		add("OBS_STALE_AFTER must be longer than OBS_FRESHNESS_WINDOW")
	}
	if c.JobLeaseTimeout < 3*time.Second {
		add("OBS_JOB_LEASE_TIMEOUT must be at least 3s")
	}
	return errs
}

// Redacted returns a log-safe view of the configuration.
func (c Config) Redacted() map[string]any {
	roles := make([]string, 0, len(c.Roles))
	for r := range c.Roles {
		roles = append(roles, r)
	}
	return map[string]any{
		"env": c.Env, "httpAddr": c.HTTPAddr, "database": RedactURL(c.DatabaseURL), "storeDriver": c.StoreDriver,
		"roles": roles, "authMode": c.AuthMode, "webDir": c.WebDir,
		"discoveryWorkers": c.DiscoveryWorkers, "metricsInterval": c.MetricsInterval.String(),
		"metricRetention": c.MetricRetention.String(), "egressAllowlist": c.EgressAllowlist,
		"allowInsecureEndpoints": c.AllowInsecureEndpoints,
	}
}

// RedactURL removes the password and query from a URL.
func RedactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "<unparseable>"
	}
	if u.User != nil {
		u.User = url.User(u.User.Username())
	}
	u.RawQuery = ""
	return u.String()
}

type loader struct {
	get  func(string) string
	errs []error
}

func (l *loader) str(k, def string) string {
	if v := strings.TrimSpace(l.get(k)); v != "" {
		return v
	}
	return def
}

func (l *loader) integer(k string, def, min, max int) int {
	v := l.get(k)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n < min || n > max {
		l.errs = append(l.errs, fmt.Errorf("%s must be an integer between %d and %d", k, min, max))
		return def
	}
	return n
}

func (l *loader) float(k string, def float64) float64 {
	v := l.get(k)
	if v == "" {
		return def
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	if err != nil {
		l.errs = append(l.errs, fmt.Errorf("%s must be a number", k))
		return def
	}
	return f
}

func (l *loader) dur(k string, def time.Duration) time.Duration {
	v := l.get(k)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(strings.TrimSpace(v))
	if err != nil || d <= 0 {
		l.errs = append(l.errs, fmt.Errorf("%s must be a positive Go duration such as 30s or 2h", k))
		return def
	}
	return d
}

func (l *loader) boolean(k string, def bool) bool {
	v := l.get(k)
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(strings.TrimSpace(v))
	if err != nil {
		l.errs = append(l.errs, fmt.Errorf("%s must be true or false", k))
		return def
	}
	return b
}

func (l *loader) list(k string) []string {
	var out []string
	for _, p := range strings.Split(l.get(k), ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, strings.ToLower(p))
		}
	}
	return out
}
