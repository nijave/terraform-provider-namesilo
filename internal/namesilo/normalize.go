// SPDX-License-Identifier: GPL-3.0-or-later

package namesilo

import (
	"fmt"
	"sort"
	"strings"
)

// DSRecord is one DS record on a domain: the key tag, the signing algorithm,
// the digest type, and the digest itself (§8.3). The integers mirror the
// API's typed fields, so only the digest needs normalization.
type DSRecord struct {
	KeyTag     int64  // API field keyTag
	Algorithm  int64  // API field alg
	DigestType int64  // API field digestType
	Digest     string // API field digest
}

// NormalizeNameserver lowercases, trims space, and strips one trailing dot.
// Only one dot is stripped: a name that already ends in two dots is a FQDN
// root quirk the API produced, not something to guess about.
func NormalizeNameserver(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	return strings.TrimSuffix(name, ".")
}

// NormalizeNameservers applies NormalizeNameserver and drops empty entries,
// preserving order. The result is not deduplicated: callers pass it through a
// set.
func NormalizeNameservers(nameservers []string) []string {
	out := make([]string, 0, len(nameservers))
	for _, ns := range nameservers {
		if n := NormalizeNameserver(ns); n != "" {
			out = append(out, n)
		}
	}
	return out
}

// SameNameservers reports set equality after normalization and sorting.
func SameNameservers(a, b []string) bool {
	x := append([]string(nil), NormalizeNameservers(a)...)
	y := append([]string(nil), NormalizeNameservers(b)...)
	sort.Strings(x)
	sort.Strings(y)
	if len(x) != len(y) {
		return false
	}
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}

// NormalizeDSRecord applies the same treatment to a DS record: the digest is
// lowercased and trimmed. Integer fields are already typed.
func NormalizeDSRecord(record DSRecord) DSRecord {
	record.Digest = strings.ToLower(strings.TrimSpace(record.Digest))
	return record
}

// Key returns the canonical identity of a record: the fields joined with a
// separator that cannot appear in any of them. Used for diffing and for
// deterministic ordering.
func (r DSRecord) Key() string {
	// The unit separator \x1f cannot occur inside a key tag, an algorithm
	// number, a digest type, or a hex digest, so the join is collision-free.
	return fmt.Sprintf("%d\x1f%d\x1f%d\x1f%s", r.KeyTag, r.Algorithm, r.DigestType, r.Digest)
}

// DiffDSRecords returns what to add and what to remove to turn current into
// desired. Both results are sorted by Key, so a plan and its tests are
// deterministic. Records that normalize to the same canonical tuple are the
// same record, however their digests are cased or spaced in the input. The
// results are never nil, so callers can range over them without a check.
func DiffDSRecords(current, desired []DSRecord) (add, remove []DSRecord) {
	currentSet := make(map[string]DSRecord, len(current))
	for _, r := range current {
		n := NormalizeDSRecord(r)
		currentSet[n.Key()] = n
	}
	desiredSet := make(map[string]DSRecord, len(desired))
	for _, r := range desired {
		n := NormalizeDSRecord(r)
		desiredSet[n.Key()] = n
	}

	add = make([]DSRecord, 0, len(desired))
	for k, r := range desiredSet {
		if _, ok := currentSet[k]; !ok {
			add = append(add, r)
		}
	}
	remove = make([]DSRecord, 0, len(current))
	for k, r := range currentSet {
		if _, ok := desiredSet[k]; !ok {
			remove = append(remove, r)
		}
	}
	sort.Slice(add, func(i, j int) bool { return add[i].Key() < add[j].Key() })
	sort.Slice(remove, func(i, j int) bool { return remove[i].Key() < remove[j].Key() })
	return add, remove
}
