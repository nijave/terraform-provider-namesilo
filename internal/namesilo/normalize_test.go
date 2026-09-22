// SPDX-License-Identifier: GPL-3.0-or-later

package namesilo

import (
	"reflect"
	"sort"
	"testing"
)

// dsr builds a DSRecord for the tables below, in API field order.
func dsr(keyTag, algorithm, digestType int64, digest string) DSRecord {
	return DSRecord{KeyTag: keyTag, Algorithm: algorithm, DigestType: digestType, Digest: digest}
}

// assertSortedByKeys fails the test when the records' Key sequence is not
// non-decreasing, which is the deterministic ordering DiffDSRecords promises.
func assertSortedByKeys(t *testing.T, label string, records []DSRecord) {
	t.Helper()
	keys := make([]string, 0, len(records))
	for _, r := range records {
		keys = append(keys, r.Key())
	}
	if !sort.StringsAreSorted(keys) {
		t.Errorf("%s: Key sequence is not sorted: %q", label, keys)
	}
}

func TestNormalizeNameserver(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "already normalized", in: "ns1.example.com", want: "ns1.example.com"},
		{name: "uppercase", in: "NS1.Example.COM", want: "ns1.example.com"},
		{name: "surrounding space", in: "  ns1.example.com\t", want: "ns1.example.com"},
		{name: "one trailing dot", in: "ns1.example.com.", want: "ns1.example.com"},
		{name: "only one trailing dot stripped", in: "ns1.example.com..", want: "ns1.example.com."},
		{name: "space, case, and dot together", in: "  NS1.Example.COM. ", want: "ns1.example.com"},
		{name: "empty string", in: "", want: ""},
		{name: "whitespace only", in: "   ", want: ""},
		{name: "bare trailing dot", in: ".", want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := NormalizeNameserver(tc.in); got != tc.want {
				t.Errorf("NormalizeNameserver(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestNormalizeNameservers(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{name: "nil input", in: nil, want: []string{}},
		{name: "empty input", in: []string{}, want: []string{}},
		{
			name: "order preserved",
			in:   []string{"NS2.Example.com.", "ns1.example.COM"},
			want: []string{"ns2.example.com", "ns1.example.com"},
		},
		{
			name: "empties dropped, order kept",
			in:   []string{"", "  NS1.Example.com. ", "", "ns2.example.com", " "},
			want: []string{"ns1.example.com", "ns2.example.com"},
		},
		{
			name: "duplicates kept, no dedup",
			in:   []string{"NS1.example.com.", "ns1.example.com"},
			want: []string{"ns1.example.com", "ns1.example.com"},
		},
		{name: "all empty", in: []string{"", "  ", "."}, want: []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := NormalizeNameservers(tc.in)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("NormalizeNameservers(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestSameNameservers(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		a    []string
		b    []string
		want bool
	}{
		{
			name: "identical",
			a:    []string{"ns1.example.com", "ns2.example.com"},
			b:    []string{"ns1.example.com", "ns2.example.com"},
			want: true,
		},
		{name: "case variants", a: []string{"NS1.Example.COM"}, b: []string{"ns1.example.com"}, want: true},
		{name: "trailing dot variants", a: []string{"ns1.example.com."}, b: []string{"ns1.example.com"}, want: true},
		{name: "whitespace variants", a: []string{"  ns1.example.com  "}, b: []string{"ns1.example.com"}, want: true},
		{
			name: "case, dot, and whitespace at once",
			a:    []string{"NS1.Example.COM.", " ns2.Example.com "},
			b:    []string{"ns1.example.com", "NS2.EXAMPLE.COM."},
			want: true,
		},
		{
			name: "same set, different order",
			a:    []string{"ns1.example.com", "ns2.example.com"},
			b:    []string{"ns2.example.com", "ns1.example.com"},
			want: true,
		},
		{
			name: "different lengths",
			a:    []string{"ns1.example.com"},
			b:    []string{"ns1.example.com", "ns2.example.com"},
			want: false,
		},
		{name: "different names", a: []string{"ns1.example.com"}, b: []string{"ns2.example.com"}, want: false},
		{name: "both empty", a: nil, b: []string{}, want: true},
		{name: "one empty", a: []string{"ns1.example.com"}, b: nil, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := SameNameservers(tc.a, tc.b); got != tc.want {
				t.Errorf("SameNameservers(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

func TestNormalizeDSRecord(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   DSRecord
		want DSRecord
	}{
		{
			name: "already normalized",
			in:   dsr(2371, 13, 2, "2bb183af7f6b4a1b8f821e08a5d2f4d1f6a7b9c0d1e2f3a4b5c6d7e8f9a0b1c2"),
			want: dsr(2371, 13, 2, "2bb183af7f6b4a1b8f821e08a5d2f4d1f6a7b9c0d1e2f3a4b5c6d7e8f9a0b1c2"),
		},
		{
			name: "uppercase digest",
			in:   dsr(2371, 13, 2, "2BB183AF7F6B4A1B8F821E08A5D2F4D1F6A7B9C0D1E2F3A4B5C6D7E8F9A0B1C2"),
			want: dsr(2371, 13, 2, "2bb183af7f6b4a1b8f821e08a5d2f4d1f6a7b9c0d1e2f3a4b5c6d7e8f9a0b1c2"),
		},
		{
			name: "digest with surrounding space",
			in:   dsr(2371, 13, 2, "  2bb183af  "),
			want: dsr(2371, 13, 2, "2bb183af"),
		},
		{
			name: "space, case, and integers untouched",
			in:   dsr(2371, 13, 2, "\t2BB183AF\n"),
			want: dsr(2371, 13, 2, "2bb183af"),
		},
		{
			name: "empty digest stays empty",
			in:   dsr(2371, 13, 2, ""),
			want: dsr(2371, 13, 2, ""),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := NormalizeDSRecord(tc.in)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("NormalizeDSRecord(%+v) = %+v, want %+v", tc.in, got, tc.want)
			}
		})
	}
}

func TestKey(t *testing.T) {
	t.Parallel()

	r := dsr(2371, 13, 2, "2bb183af")
	want := "2371\x1f13\x1f2\x1f2bb183af"
	if got := r.Key(); got != want {
		t.Errorf("Key() = %q, want %q", got, want)
	}

	// Without a separator these two would both flatten to "12345678"; the
	// unit separator keeps field boundaries visible, so the keys differ.
	a := dsr(12, 34, 56, "78")
	b := dsr(123, 45, 67, "8")
	if a.Key() == b.Key() {
		t.Errorf("keys collide across field boundaries: %q", a.Key())
	}
}

func TestDiffDSRecords(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		current    []DSRecord
		desired    []DSRecord
		wantAdd    []DSRecord
		wantRemove []DSRecord
	}{
		{
			name:       "both empty",
			current:    nil,
			desired:    nil,
			wantAdd:    []DSRecord{},
			wantRemove: []DSRecord{},
		},
		{
			name:       "both empty non-nil slices",
			current:    []DSRecord{},
			desired:    []DSRecord{},
			wantAdd:    []DSRecord{},
			wantRemove: []DSRecord{},
		},
		{
			name:    "adds only",
			current: nil,
			desired: []DSRecord{dsr(8, 13, 2, "bb"), dsr(2371, 13, 2, "aa")},
			wantAdd: []DSRecord{dsr(2371, 13, 2, "aa"), dsr(8, 13, 2, "bb")},
			// Keys sort "2371\x1f13..." before "8\x1f13...".
			wantRemove: []DSRecord{},
		},
		{
			name:       "removes only",
			current:    []DSRecord{dsr(8, 13, 2, "bb"), dsr(2371, 13, 2, "aa")},
			desired:    nil,
			wantAdd:    []DSRecord{},
			wantRemove: []DSRecord{dsr(2371, 13, 2, "aa"), dsr(8, 13, 2, "bb")},
		},
		{
			name: "adds and removes in one diff",
			current: []DSRecord{
				dsr(2371, 13, 2, "aaaa"),
				dsr(8, 13, 2, "bbbb"),
				dsr(65535, 8, 1, "cccc"),
			},
			desired: []DSRecord{
				dsr(1234, 13, 2, "dddd"),
				dsr(8, 13, 2, "bbbb"),
				dsr(2371, 13, 2, "aaaa"),
			},
			wantAdd:    []DSRecord{dsr(1234, 13, 2, "dddd")},
			wantRemove: []DSRecord{dsr(65535, 8, 1, "cccc")},
		},
		{
			name:       "case-only digest difference is no diff",
			current:    []DSRecord{dsr(2371, 13, 2, "2BB183AF")},
			desired:    []DSRecord{dsr(2371, 13, 2, "2bb183af")},
			wantAdd:    []DSRecord{},
			wantRemove: []DSRecord{},
		},
		{
			name:       "case and whitespace digest difference is no diff",
			current:    []DSRecord{dsr(2371, 13, 2, "2BB183AF")},
			desired:    []DSRecord{dsr(2371, 13, 2, "  2bb183af ")},
			wantAdd:    []DSRecord{},
			wantRemove: []DSRecord{},
		},
		{
			name:       "duplicates in current collapse",
			current:    []DSRecord{dsr(2371, 13, 2, "aa"), dsr(2371, 13, 2, "AA")},
			desired:    []DSRecord{dsr(2371, 13, 2, "aa")},
			wantAdd:    []DSRecord{},
			wantRemove: []DSRecord{},
		},
		{
			name:       "duplicates in desired collapse",
			current:    nil,
			desired:    []DSRecord{dsr(8, 13, 2, "bb"), dsr(8, 13, 2, "BB")},
			wantAdd:    []DSRecord{dsr(8, 13, 2, "bb")},
			wantRemove: []DSRecord{},
		},
		{
			name: "same set in different order is no diff",
			current: []DSRecord{
				dsr(8, 13, 2, "bbbb"),
				dsr(2371, 13, 2, "aaaa"),
			},
			desired: []DSRecord{
				dsr(2371, 13, 2, "aaaa"),
				dsr(8, 13, 2, "bbbb"),
			},
			wantAdd:    []DSRecord{},
			wantRemove: []DSRecord{},
		},
		{
			name: "deterministic ordering across many records",
			current: []DSRecord{
				dsr(400, 8, 1, "dd"),
				dsr(100, 8, 1, "aa"),
				dsr(300, 8, 1, "cc"),
				dsr(200, 8, 1, "bb"),
			},
			desired: []DSRecord{
				dsr(600, 8, 1, "ff"),
				dsr(200, 8, 1, "bb"),
				dsr(500, 8, 1, "ee"),
				dsr(300, 8, 1, "cc"),
			},
			wantAdd:    []DSRecord{dsr(500, 8, 1, "ee"), dsr(600, 8, 1, "ff")},
			wantRemove: []DSRecord{dsr(100, 8, 1, "aa"), dsr(400, 8, 1, "dd")},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			add, remove := DiffDSRecords(tc.current, tc.desired)

			// The contract is non-nil results even when there is nothing to do.
			if add == nil {
				t.Error("add is nil, want a non-nil (possibly empty) slice")
			}
			if remove == nil {
				t.Error("remove is nil, want a non-nil (possibly empty) slice")
			}
			if !reflect.DeepEqual(add, tc.wantAdd) {
				t.Errorf("add = %+v, want %+v", add, tc.wantAdd)
			}
			if !reflect.DeepEqual(remove, tc.wantRemove) {
				t.Errorf("remove = %+v, want %+v", remove, tc.wantRemove)
			}
			assertSortedByKeys(t, "add", add)
			assertSortedByKeys(t, "remove", remove)
		})
	}
}
