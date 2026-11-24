// Package secrets resolves credential references. Connection profiles store a
// reference ("env:OBS_SECRET_X" or "file:name"), never the secret itself.
//
// Restrictions stop a connection profile from being used to read arbitrary
// process secrets: env references must use the OBS_SECRET_ prefix, and file
// references are resolved inside one configured directory without traversal.
package secrets

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// EnvPrefix is the required prefix of environment-variable references.
const EnvPrefix = "OBS_SECRET_"

const maxSecretBytes = 64 * 1024

var (
	envNamePattern  = regexp.MustCompile(`^OBS_SECRET_[A-Z0-9_]{1,64}$`)
	fileNamePattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}(/[A-Za-z0-9._-]{1,128}){0,3}$`)
)

// ErrNotFound means the reference is well formed but no value exists.
var ErrNotFound = errors.New("secret not found")

// Value wraps a secret so it is never printed by accident.
type Value struct{ v string }

// Reveal returns the secret. Call it only where the value is sent to the
// provider it belongs to.
func (s Value) Reveal() string { return s.v }

// String implements fmt.Stringer with a redacted value.
func (s Value) String() string { return "[REDACTED]" }

// GoString implements fmt.GoStringer with a redacted value.
func (s Value) GoString() string { return "[REDACTED]" }

// Empty reports whether no secret is held.
func (s Value) Empty() bool { return s.v == "" }

// ValidateRef checks the syntax of a reference without resolving it.
func ValidateRef(ref string) error {
	kind, name, ok := strings.Cut(ref, ":")
	if !ok {
		return fmt.Errorf("credential reference must be env:%sNAME or file:<name>", EnvPrefix)
	}
	switch kind {
	case "env":
		if !envNamePattern.MatchString(name) {
			return fmt.Errorf("env reference must match %s", envNamePattern)
		}
	case "file":
		if !fileNamePattern.MatchString(name) || strings.Contains(name, "..") {
			return errors.New("file reference must be a relative name inside the secret directory, without '..'")
		}
	default:
		return fmt.Errorf("unknown credential reference kind %q", kind)
	}
	return nil
}

// Resolver reads referenced secrets.
type Resolver struct {
	Dir    string
	Getenv func(string) string
}

// NewResolver returns a resolver reading files under dir and the process env.
func NewResolver(dir string) *Resolver { return &Resolver{Dir: dir, Getenv: os.Getenv} }

// Resolve returns the secret for ref. An empty ref returns an empty Value.
func (r *Resolver) Resolve(ref string) (Value, error) {
	if ref == "" {
		return Value{}, nil
	}
	if err := ValidateRef(ref); err != nil {
		return Value{}, err
	}
	kind, name, _ := strings.Cut(ref, ":")
	switch kind {
	case "env":
		v := r.Getenv(name)
		if v == "" {
			return Value{}, fmt.Errorf("%w: environment variable %s is not set", ErrNotFound, name)
		}
		return Value{v: strings.TrimSpace(v)}, nil
	default:
		base, err := filepath.Abs(r.Dir)
		if err != nil {
			return Value{}, err
		}
		p := filepath.Join(base, filepath.FromSlash(name))
		if !strings.HasPrefix(p, base+string(os.PathSeparator)) {
			return Value{}, errors.New("file reference escapes the secret directory")
		}
		f, err := os.Open(p)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return Value{}, fmt.Errorf("%w: file %s", ErrNotFound, name)
			}
			return Value{}, fmt.Errorf("reading secret file %s: %w", name, err)
		}
		defer f.Close()
		buf := make([]byte, maxSecretBytes+1)
		n, _ := f.Read(buf)
		if n > maxSecretBytes {
			return Value{}, errors.New("secret file exceeds 64 KiB")
		}
		return Value{v: strings.TrimSpace(string(buf[:n]))}, nil
	}
}

// LooksSecret reports whether a configuration key name suggests a secret.
// Connection config is rejected if it contains such keys, so secrets cannot
// be stored in plaintext columns by mistake.
func LooksSecret(key string) bool {
	k := strings.ToLower(key)
	for _, s := range []string{"password", "passwd", "secret", "token", "apikey", "api_key", "privatekey", "private_key", "accesskey", "access_key", "credential"} {
		if strings.Contains(k, s) {
			return true
		}
	}
	return false
}
