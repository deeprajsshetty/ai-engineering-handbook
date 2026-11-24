// Package ids generates identifiers.
//
// Two kinds exist:
//   - New: random, time-ordered identifiers for things the platform creates
//     (connections, jobs, collection runs). Time ordering keeps B-tree inserts
//     append-mostly and makes cursors stable.
//   - Deterministic: a hash of a natural key, used for discovered resources and
//     relationships so that repeated observations of the same thing always map
//     to the same row (idempotent upserts without a lookup).
package ids

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"regexp"
	"strings"
	"time"
)

// New returns "<prefix>_<12 hex ms timestamp><16 hex random>".
func New(prefix string) string {
	var b [14]byte
	ms := uint64(time.Now().UTC().UnixMilli())
	var ts [8]byte
	binary.BigEndian.PutUint64(ts[:], ms)
	copy(b[:6], ts[2:])
	if _, err := rand.Read(b[6:]); err != nil {
		panic("ids: crypto/rand failed: " + err.Error())
	}
	return prefix + "_" + hex.EncodeToString(b[:])
}

// Deterministic returns "<prefix>_<24 hex>" derived from the parts. Parts are
// length-prefixed before hashing so ("ab","c") and ("a","bc") differ.
func Deterministic(prefix string, parts ...string) string {
	h := sha256.New()
	var l [4]byte
	for _, p := range parts {
		binary.BigEndian.PutUint32(l[:], uint32(len(p)))
		h.Write(l[:])
		h.Write([]byte(p))
	}
	return prefix + "_" + hex.EncodeToString(h.Sum(nil))[:24]
}

var idPattern = regexp.MustCompile(`^[a-z]{2,8}_[0-9a-f]{24,28}$`)

// Valid reports whether s is a syntactically valid identifier with the given
// prefix. It is used to reject malformed path parameters before they reach SQL.
func Valid(prefix, s string) bool {
	return strings.HasPrefix(s, prefix+"_") && idPattern.MatchString(s)
}
