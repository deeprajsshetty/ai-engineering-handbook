package topology

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Limits on observation content. They bound memory per job and row size, and
// stop a misbehaving provider from writing arbitrarily large documents.
const (
	MaxExternalIDLen   = 512
	MaxNameLen         = 256
	MaxLabels          = 64
	MaxLabelKeyLen     = 128
	MaxLabelValueLen   = 256
	MaxPropertiesBytes = 16 * 1024
	DefaultRegion      = "global"
)

var (
	typePattern     = regexp.MustCompile(`^[a-z][a-z0-9]*(\.[a-z][a-z0-9_]*){1,3}$`)
	providerPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{1,31}$`)
	regionPattern   = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,64}$`)
)

// ErrInvalidObservation wraps all normalization failures.
var ErrInvalidObservation = errors.New("invalid observation")

// ValidType reports whether t is a syntactically valid canonical type.
func ValidType(t string) bool { return typePattern.MatchString(t) }

// ValidProvider reports whether p is a syntactically valid provider name.
func ValidProvider(p string) bool { return providerPattern.MatchString(p) }

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidObservation, fmt.Sprintf(format, a...))
}

func cleanText(s string) string {
	s = strings.TrimSpace(s)
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
}

func validKeyPart(name, v string, max int) error {
	if v == "" {
		return invalid("%s is required", name)
	}
	if len(v) > max {
		return invalid("%s exceeds %d bytes", name, max)
	}
	if !utf8.ValidString(v) {
		return invalid("%s is not valid UTF-8", name)
	}
	for _, r := range v {
		if unicode.IsControl(r) {
			return invalid("%s contains control characters", name)
		}
	}
	return nil
}

// ValidateKey checks a natural key.
func ValidateKey(k Key) error {
	if !ValidProvider(k.Provider) {
		return invalid("provider %q is not a valid provider name", k.Provider)
	}
	if err := validKeyPart("scope", k.Scope, MaxExternalIDLen); err != nil {
		return err
	}
	return validKeyPart("externalId", k.ExternalID, MaxExternalIDLen)
}

// Normalize validates an observation and converts it to a Resource with a
// deterministic ID and a content hash. Labels are trimmed; empty label keys
// are dropped; names default to the external ID; regions default to "global".
func Normalize(tenantID, connectionID string, o Observation) (Resource, error) {
	if err := ValidateKey(o.Key); err != nil {
		return Resource{}, err
	}
	if !ValidType(o.Type) {
		return Resource{}, invalid("type %q must look like \"category.kind\"", o.Type)
	}
	name := cleanText(o.Name)
	if name == "" {
		name = o.Key.ExternalID
	}
	if len(name) > MaxNameLen {
		name = truncateUTF8(name, MaxNameLen)
	}
	region := cleanText(o.Region)
	if region == "" {
		region = DefaultRegion
	}
	if !regionPattern.MatchString(region) {
		return Resource{}, invalid("region %q contains unsupported characters", region)
	}
	labels, err := normalizeLabels(o.Labels)
	if err != nil {
		return Resource{}, err
	}
	props := o.Properties
	if props == nil {
		props = map[string]any{}
	}
	pb, err := json.Marshal(props)
	if err != nil {
		return Resource{}, invalid("properties are not JSON-serializable: %v", err)
	}
	if len(pb) > MaxPropertiesBytes {
		return Resource{}, invalid("properties exceed %d bytes", MaxPropertiesBytes)
	}
	// Round-trip so stored and hashed forms are identical (numbers become float64).
	var canonicalProps map[string]any
	_ = json.Unmarshal(pb, &canonicalProps)

	r := Resource{
		ID:           ResourceID(tenantID, o.Key),
		TenantID:     tenantID,
		ConnectionID: connectionID,
		Provider:     o.Key.Provider,
		Scope:        o.Key.Scope,
		ExternalID:   o.Key.ExternalID,
		Type:         o.Type,
		NativeType:   truncateUTF8(cleanText(o.NativeType), 128),
		Name:         name,
		Region:       region,
		Labels:       labels,
		Properties:   canonicalProps,
		Status:       StatusActive,
	}
	r.ContentHash = ContentHash(r)
	return r, nil
}

func normalizeLabels(in map[string]string) (map[string]string, error) {
	out := make(map[string]string, len(in))
	for k, v := range in {
		k = cleanText(k)
		if k == "" {
			continue
		}
		if len(k) > MaxLabelKeyLen {
			return nil, invalid("label key %q exceeds %d bytes", truncateUTF8(k, 32), MaxLabelKeyLen)
		}
		v = cleanText(v)
		if len(v) > MaxLabelValueLen {
			v = truncateUTF8(v, MaxLabelValueLen)
		}
		out[k] = v
	}
	if len(out) > MaxLabels {
		return nil, invalid("%d labels exceed the limit of %d", len(out), MaxLabels)
	}
	return out, nil
}

// ContentHash hashes the attributes that define "changed" for reconciliation.
// Timestamps, status and the observing job are excluded on purpose: a
// resource seen again with identical attributes is "unchanged".
// encoding/json sorts map keys, so the hash is independent of map order.
func ContentHash(r Resource) string {
	doc := struct {
		Type       string            `json:"t"`
		NativeType string            `json:"n"`
		Name       string            `json:"m"`
		Region     string            `json:"r"`
		Labels     map[string]string `json:"l"`
		Properties map[string]any    `json:"p"`
	}{r.Type, r.NativeType, r.Name, r.Region, r.Labels, r.Properties}
	b, _ := json.Marshal(doc)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:16])
}

func truncateUTF8(s string, max int) string {
	if len(s) <= max {
		return s
	}
	for max > 0 && !utf8.RuneStart(s[max]) {
		max--
	}
	return s[:max]
}
