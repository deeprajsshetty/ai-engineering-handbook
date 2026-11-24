// Package database is the generic, engine-agnostic discovery provider for
// database servers. The engine (PostgreSQL, Oracle, ...) is chosen per
// connection; everything engine-specific lives behind dbengine.Engine.
//
// Topology produced (identical shape for every engine):
//
//	database.instance  --contains-->  database.database   (PostgreSQL database, Oracle PDB / non-CDB)
//	database.instance  --contains-->  database.tablespace (instance-level tablespaces)
//	database.database  --contains-->  database.tablespace (Oracle PDB tablespaces)
//
// Security: the host must be in OBS_EGRESS_ALLOWLIST and every resolved
// address is re-checked at dial time; the password comes only from the
// connection's credentialRef; plaintext transport requires
// OBS_ALLOW_INSECURE_ENDPOINTS (development).
package database

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/deeprajsshetty/observability-platform/platform/internal/dbengine"
	"github.com/deeprajsshetty/observability-platform/platform/internal/discovery"
	"github.com/deeprajsshetty/observability-platform/platform/internal/domain/topology"
	"github.com/deeprajsshetty/observability-platform/platform/internal/netguard"
	"github.com/deeprajsshetty/observability-platform/platform/internal/secrets"
)

// Name is the provider name.
const Name = "database"

// ResourceTypes produced by this provider.
var ResourceTypes = []string{"database.instance", "database.database", "database.tablespace"}

var (
	hostPattern = regexp.MustCompile(`^[A-Za-z0-9.-]{1,253}$|^[0-9a-fA-F:]{2,45}$`)
	namePattern = regexp.MustCompile(`^[A-Za-z0-9_.$#-]{1,128}$`)
	userPattern = regexp.MustCompile(`^[A-Za-z0-9_.$#@-]{1,128}$`)
)

// Provider implements discovery.Provider.
type Provider struct {
	Engines     *dbengine.Registry
	Policy      netguard.Policy
	DialTimeout time.Duration
}

func (p *Provider) fields() []discovery.ConfigField {
	var engines []discovery.Option
	for _, n := range p.Engines.Names() {
		e, _ := p.Engines.Get(n)
		engines = append(engines, discovery.Option{Value: n, Label: e.DisplayName(), Description: e.MinimumPrivileges()})
	}
	return []discovery.ConfigField{
		{Name: "engine", Type: "string", Required: true, Description: "Database engine.", Options: engines},
		{Name: "host", Type: "string", Required: true, Description: "Server host name or IP. Must be in OBS_EGRESS_ALLOWLIST.", Placeholder: "db1.internal.example"},
		{Name: "port", Type: "integer", Description: "TCP port. Defaults to the engine's standard port (PostgreSQL 5432, Oracle 1521)."},
		{Name: "database", Type: "string", Required: true, Description: "PostgreSQL: database to connect to (for example postgres). Oracle: service name (CDB root service to see all PDBs)."},
		{Name: "username", Type: "string", Required: true, Description: "Read-only monitoring user. The password comes from credentialRef."},
		{Name: "tls", Type: "string", Default: string(dbengine.TLSVerifyFull), Description: "Transport security.", Options: []discovery.Option{
			{Value: string(dbengine.TLSVerifyFull), Label: "Encrypt and verify (recommended)"},
			{Value: string(dbengine.TLSRequire), Label: "Encrypt, skip certificate verification"},
			{Value: string(dbengine.TLSDisable), Label: "Plaintext (development only)"},
		}},
		{Name: "location", Type: "string", Default: "global", Description: "Region or data centre shown in topology, for example on-prem-dc1."},
		{Name: "environment", Type: "string", Description: "Optional env label, for example prod."},
	}
}

// Info describes the provider.
func (p *Provider) Info() discovery.Info {
	var engines []string
	for _, n := range p.Engines.Names() {
		e, _ := p.Engines.Get(n)
		engines = append(engines, e.DisplayName())
	}
	return discovery.Info{
		Name: Name, DisplayName: "Database server",
		Description: "Discovers a database server, its databases and tablespaces, and collects measured health metrics through read-only " +
			"catalog queries. Engines: " + strings.Join(engines, ", ") + ".",
		RequiresCredential: true, MetricsSource: "measured", ConfigFields: p.fields(), ResourceTypes: ResourceTypes,
	}
}

// Settings is the parsed connection configuration.
type Settings struct {
	Engine      dbengine.Engine
	Target      dbengine.Target
	Location    string
	Environment string
}

// Parse reads and validates configuration (no network).
func (p *Provider) Parse(cfg map[string]any) (Settings, []discovery.FieldError) {
	errs := discovery.ValidateKnownFields(cfg, p.fields())
	var s Settings
	name := discovery.Str(cfg, "engine", "")
	e, ok := p.Engines.Get(name)
	if !ok && name != "" {
		errs = append(errs, discovery.FieldError{Field: "engine", Message: "unsupported engine; supported: " + strings.Join(p.Engines.Names(), ", ")})
	}
	s.Engine = e
	host := strings.ToLower(discovery.Str(cfg, "host", ""))
	if host != "" && !hostPattern.MatchString(host) {
		errs = append(errs, discovery.FieldError{Field: "host", Message: "must be a host name or IP address"})
	} else if host != "" {
		if err := p.Policy.ValidateHost(host); err != nil {
			errs = append(errs, discovery.FieldError{Field: "host", Message: err.Error()})
		}
	}
	errs = append(errs, discovery.RangeCheck(cfg, "port", 1, 65535)...)
	dbname := discovery.Str(cfg, "database", "")
	if dbname != "" && !namePattern.MatchString(dbname) {
		errs = append(errs, discovery.FieldError{Field: "database", Message: "must be 1-128 letters, digits or _ . $ # -"})
	}
	user := discovery.Str(cfg, "username", "")
	if user != "" && !userPattern.MatchString(user) {
		errs = append(errs, discovery.FieldError{Field: "username", Message: "must be 1-128 letters, digits or _ . $ # @ -"})
	}
	tls := dbengine.TLSMode(discovery.Str(cfg, "tls", string(dbengine.TLSVerifyFull)))
	switch tls {
	case dbengine.TLSVerifyFull, dbengine.TLSRequire:
	case dbengine.TLSDisable:
		if !p.Policy.AllowInsecure {
			errs = append(errs, discovery.FieldError{Field: "tls", Message: "disable requires OBS_ALLOW_INSECURE_ENDPOINTS=true (development only)"})
		}
	default:
		errs = append(errs, discovery.FieldError{Field: "tls", Message: "must be verify-full, require or disable"})
	}
	loc := discovery.Str(cfg, "location", topology.DefaultRegion)
	if len(loc) > 64 {
		errs = append(errs, discovery.FieldError{Field: "location", Message: "must be at most 64 characters"})
	}
	env := discovery.Str(cfg, "environment", "")
	if len(env) > 64 {
		errs = append(errs, discovery.FieldError{Field: "environment", Message: "must be at most 64 characters"})
	}
	port := discovery.Int(cfg, "port", 0)
	if port == 0 && e != nil {
		port = e.DefaultPort()
	}
	s.Target = dbengine.Target{Host: host, Port: port, Database: dbname, Username: user, TLS: tls}
	s.Location, s.Environment = loc, env
	return s, errs
}

// ValidateConfig checks configuration statically.
func (p *Provider) ValidateConfig(cfg map[string]any, credentialRef string) []discovery.FieldError {
	_, errs := p.Parse(cfg)
	if credentialRef == "" {
		errs = append(errs, discovery.FieldError{Field: "credentialRef", Message: "a password reference is required"})
	}
	return errs
}

// Session is an open, guarded connection to one server.
type Session struct {
	Settings Settings
	Querier  dbengine.Querier
	close    func() error
}

// Close releases the pool.
func (s *Session) Close() error { return s.close() }

// Open validates configuration and returns a session. It does not run queries.
func (p *Provider) Open(cfg map[string]any, cred secrets.Value) (*Session, error) {
	s, errs := p.Parse(cfg)
	if len(errs) > 0 {
		return nil, fmt.Errorf("invalid configuration: %s", errs[0].Error())
	}
	timeout := p.DialTimeout
	if timeout == 0 {
		timeout = 10 * time.Second
	}
	d := p.Policy.Dialer(timeout)
	db, err := s.Engine.Open(s.Target, cred.Reveal(), d.DialContext)
	if err != nil {
		return nil, err
	}
	return &Session{Settings: s, Querier: dbengine.SQLQuerier{DB: db}, close: db.Close}, nil
}

// Check connects and runs the inventory's first query.
func (p *Provider) Check(ctx context.Context, req discovery.Request) error {
	sess, err := p.Open(req.Connection.Config, req.Credential)
	if err != nil {
		return err
	}
	defer sess.Close()
	_, err = sess.Settings.Engine.Inventory(ctx, sess.Querier)
	return sanitize(err)
}

// sanitize keeps driver errors but strips anything that looks like a DSN.
func sanitize(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	if i := strings.Index(msg, "://"); i >= 0 {
		return errors.New("database connection failed (details withheld: message contained a connection string)")
	}
	return errors.New(msg)
}

// Scope returns the topology scope for an inventory: engine plus the
// server's stable identity, falling back to host:port when the identity is
// not readable with the granted privileges.
func Scope(s Settings, inv dbengine.Inventory) string {
	id := inv.Instance.ID
	if id == "" {
		id = fmt.Sprintf("%s:%d", s.Target.Host, s.Target.Port)
	}
	return s.Engine.Name() + ":" + id
}

// ExternalID maps an engine object to the resource external ID. The metrics
// collector uses the same mapping to attach readings to resources.
func ExternalID(o dbengine.ObjectRef) string {
	switch o.Kind {
	case "database":
		return "db/" + o.Name
	case "tablespace":
		if o.Database != "" {
			return "ts/" + o.Database + "/" + o.Name
		}
		return "ts/" + o.Name
	default:
		return "instance"
	}
}

// Discover reads the inventory and emits it as one page.
func (p *Provider) Discover(ctx context.Context, req discovery.Request, emit discovery.Emit) error {
	sess, err := p.Open(req.Connection.Config, req.Credential)
	if err != nil {
		return err
	}
	defer sess.Close()
	inv, err := sess.Settings.Engine.Inventory(ctx, sess.Querier)
	if err != nil {
		return sanitize(err)
	}
	return emit(Build(sess.Settings, inv))
}

// Build converts an inventory into observations. It is pure and is shared by
// tests and by the fixture provider's simulated database estate.
func Build(s Settings, inv dbengine.Inventory) discovery.Page {
	scope := Scope(s, inv)
	eng := s.Engine.Name()
	key := func(o dbengine.ObjectRef) topology.Key {
		return topology.Key{Provider: Name, Scope: scope, ExternalID: ExternalID(o)}
	}
	labels := func() map[string]string {
		l := map[string]string{"engine": eng}
		if s.Environment != "" {
			l["env"] = s.Environment
		}
		return l
	}
	var pg discovery.Page
	instRef := dbengine.ObjectRef{Kind: "instance"}
	props := map[string]any{"engine": eng, "engineVersion": inv.Instance.Version, "host": s.Target.Host, "port": s.Target.Port}
	for k, v := range inv.Instance.Properties {
		props[k] = v
	}
	name := inv.Instance.Name
	if name == "" {
		name = s.Target.Host
	}
	pg.Resources = append(pg.Resources, topology.Observation{Key: key(instRef), Type: "database.instance",
		NativeType: eng + ":instance", Name: s.Engine.DisplayName() + " " + name, Region: s.Location, Labels: labels(), Properties: props})
	for _, d := range inv.Databases {
		ref := dbengine.ObjectRef{Kind: "database", Name: d.Name}
		pg.Resources = append(pg.Resources, topology.Observation{Key: key(ref), Type: "database.database",
			NativeType: eng + ":database", Name: d.Name, Region: s.Location, Labels: labels(), Properties: d.Properties})
		pg.Relationships = append(pg.Relationships, topology.RelationshipObservation{Source: key(instRef), Target: key(ref), Type: topology.RelContains})
	}
	for _, t := range inv.Tablespaces {
		ref := dbengine.ObjectRef{Kind: "tablespace", Database: t.Database, Name: t.Name}
		pg.Resources = append(pg.Resources, topology.Observation{Key: key(ref), Type: "database.tablespace",
			NativeType: eng + ":tablespace", Name: t.Name, Region: s.Location, Labels: labels(), Properties: t.Properties})
		parent := instRef
		if t.Database != "" {
			parent = dbengine.ObjectRef{Kind: "database", Name: t.Database}
		}
		pg.Relationships = append(pg.Relationships, topology.RelationshipObservation{Source: key(parent), Target: key(ref), Type: topology.RelContains})
	}
	return pg
}
