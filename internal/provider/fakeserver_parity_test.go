// SPDX-License-Identifier: GPL-3.0-or-later

package provider_test

import (
	"encoding/xml"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nijave/terraform-provider-namesilo/internal/namesilo"
)

// parityDomain is the disposable domain the live corpus was captured against.
const parityDomain = "testing-1519107.top"

// parityDigest is the digest the captured DS-record fixtures carry.
const parityDigest = "ABABABABABABABABABABABABABABABABABABABABABABABABABABABABABABABAB"

// loadNameSiloFixture reads one captured reply from the namesilo package's
// testdata directory. The provider tests live one directory over.
func loadNameSiloFixture(t *testing.T, name string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "namesilo", "testdata", name))
	if err != nil {
		t.Fatalf("reading namesilo testdata/%s: %v", name, err)
	}
	return string(body)
}

// xmlShape is the structural skeleton of an XML document: element names,
// attribute names, and child order. Text values are deliberately absent: the
// fake renders dynamic values per its state, so only the structure can be
// required to match the fixture.
type xmlShape struct {
	name     xml.Name
	attrs    map[string]bool
	children []*xmlShape
}

// shapeFromXML parses one XML document into its root xmlShape. It ignores
// character data, comments, and processing instructions.
func shapeFromXML(t *testing.T, data []byte) *xmlShape {
	t.Helper()
	dec := xml.NewDecoder(strings.NewReader(string(data)))
	var stack []*xmlShape
	var root *xmlShape
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("decoding XML: %v\nbody: %s", err, data)
		}
		switch se := tok.(type) {
		case xml.StartElement:
			n := &xmlShape{name: se.Name, attrs: make(map[string]bool, len(se.Attr))}
			for _, a := range se.Attr {
				n.attrs[a.Name.Local] = true
			}
			if len(stack) == 0 {
				root = n
			} else {
				parent := stack[len(stack)-1]
				parent.children = append(parent.children, n)
			}
			stack = append(stack, n)
		case xml.EndElement:
			stack = stack[:len(stack)-1]
		}
	}
	if root == nil {
		t.Fatalf("no XML element in body: %s", data)
	}
	return root
}

// childNames is the sequence of child element names with consecutive
// duplicates collapsed to one, so a repeated element (nameservers, ds_records,
// contacts) matches a fixture with a different count. Ignored names are
// dropped from both sides.
func childNames(children []*xmlShape) []string {
	var out []string
	for _, c := range children {
		if n := len(out); n > 0 && out[n-1] == c.name.Local {
			continue
		}
		out = append(out, c.name.Local)
	}
	return out
}

// firstChild returns the first child with the given name, or nil.
func firstChild(children []*xmlShape, name string) *xmlShape {
	for _, c := range children {
		if c.name.Local == name {
			return c
		}
	}
	return nil
}

// assertSameShape requires the fake's element to have the fixture's element
// name, the same attribute names, and the same child element names in the same
// order (repeats collapsed). It recurses into each child, so a repeated
// element's own shape is checked against the fixture's.
func assertSameShape(t *testing.T, path string, want, got *xmlShape) {
	t.Helper()
	if want == nil || got == nil {
		t.Fatalf("%s: missing element (fixture %v, fake %v)", path, want != nil, got != nil)
	}
	if want.name != got.name {
		t.Errorf("%s: fixture <%s>, fake <%s>", path, want.name.Local, got.name.Local)
		return
	}
	for a := range want.attrs {
		if !got.attrs[a] {
			t.Errorf("%s: <%s> lacks the fixture's attribute %q", path, want.name.Local, a)
		}
	}
	for a := range got.attrs {
		if !want.attrs[a] {
			t.Errorf("%s: <%s> has an attribute the fixture does not: %q", path, want.name.Local, a)
		}
	}
	wNames := childNames(want.children)
	gNames := childNames(got.children)
	if strings.Join(wNames, ",") != strings.Join(gNames, ",") {
		t.Errorf("%s: <%s> children: fixture %v, fake %v", path, want.name.Local, wNames, gNames)
		return
	}
	for _, name := range wNames {
		assertSameShape(t, path+"/"+name,
			firstChild(want.children, name),
			firstChild(got.children, name))
	}
}

// parityContact is a representative profile for the contactList parity cases:
// the fixed fields set, the country-specific fields unset (the captured
// profiles set none of them).
func parityContact() namesilo.Contact {
	return namesilo.Contact{
		DefaultProfile: true,
		Nickname:       "Default",
		FirstName:      "Alex",
		LastName:       "Morgan",
		Address:        "4187 Maple Street",
		City:           "Springfield",
		State:          "IL",
		Zip:            "62704",
		Country:        "US",
		Email:          "contact@example.com",
		Phone:          "+15125550100",
	}
}

// seedParityDomain adds the captured domain with representative state: three
// nameservers and all four contact roles, so the reply carries every element
// the fixture does.
func seedParityDomain(f *fakeNamesilo) *fakeDomain {
	d := f.addDomain(parityDomain)
	d.created = "2026-09-22"
	d.expires = "2027-09-22"
	d.status = "Active"
	d.nameservers = []string{"NS1.DNSOWL.COM", "NS2.DNSOWL.COM", "NS3.DNSOWL.COM"}
	d.roles = namesilo.ContactRoles{
		Registrant:     "29145691",
		Administrative: "29145691",
		Technical:      "29145691",
		Billing:        "29145691",
	}
	return d
}

// TestFakeServerFixtureParity is the mechanism that keeps the fake from
// drifting from the real API. For every operation with a captured reply, it
// renders the fake's response for a representative state and the fixture, then
// requires identical element names, attribute names, and child ordering. Text
// values are ignored (the fake's values are dynamic); structure is not.
//
// For listDomains the request is a bare call, because that is the call the
// corpus captured: the API adds the <pager> only for a paged call, and the
// fake mirrors that (the client always sends page/pageSize, which the paged
// provider tests exercise).
func TestFakeServerFixtureParity(t *testing.T) {
	cases := []struct {
		name    string
		fixture string
		request string
		seed    func(*fakeNamesilo)
	}{
		{
			name:    "getDomainInfo",
			fixture: "getDomainInfo.xml",
			request: "getDomainInfo?domain=" + parityDomain,
			seed:    func(f *fakeNamesilo) { seedParityDomain(f) },
		},
		{
			name:    "listDomains",
			fixture: "listDomains.xml",
			request: "listDomains?page=1&pageSize=100",
			seed:    func(f *fakeNamesilo) { seedParityDomain(f) },
		},
		{
			name:    "dnsSecListRecords with a record",
			fixture: "dnsSecListRecords-with.xml",
			request: "dnsSecListRecords?domain=" + parityDomain,
			seed: func(f *fakeNamesilo) {
				seedParityDomain(f)
				f.setDSRecords(parityDomain, []namesilo.DSRecord{
					{KeyTag: 12345, Algorithm: 13, DigestType: 2, Digest: parityDigest},
				})
			},
		},
		{
			name:    "dnsSecListRecords empty",
			fixture: "dnsSecListRecords-empty.xml",
			request: "dnsSecListRecords?domain=" + parityDomain,
			seed:    func(f *fakeNamesilo) { seedParityDomain(f) },
		},
		{
			name:    "contactList single",
			fixture: "contactList-single.xml",
			request: "contactList?contact_id=29145691",
			seed:    func(f *fakeNamesilo) { f.seedContact("29145691", parityContact()) },
		},
		{
			name:    "contactList all",
			fixture: "contactList-all.xml",
			request: "contactList?contact_id=",
			seed:    func(f *fakeNamesilo) { f.seedContact("29145691", parityContact()) },
		},
		{
			name:    "contactAdd",
			fixture: "contactAdd.xml",
			request: "contactAdd",
			seed:    func(f *fakeNamesilo) {},
		},
		{
			name:    "contactUpdate",
			fixture: "contactUpdate.xml",
			request: "contactUpdate?contact_id=1001",
			seed:    func(f *fakeNamesilo) { f.seedContact("1001", parityContact()) },
		},
		{
			name:    "contactDelete",
			fixture: "contactDelete.xml",
			request: "contactDelete?contact_id=1001",
			seed:    func(f *fakeNamesilo) {},
		},
		{
			name:    "contactDomainAssociate",
			fixture: "contactDomainAssociate.xml",
			request: "contactDomainAssociate?domain=" + parityDomain + "&registrant=29145691",
			seed:    func(f *fakeNamesilo) { seedParityDomain(f) },
		},
		{
			name:    "addPrivacy",
			fixture: "addPrivacy.xml",
			request: "addPrivacy?domain=" + parityDomain,
			seed:    func(f *fakeNamesilo) { seedParityDomain(f) },
		},
		{
			name:    "removePrivacy",
			fixture: "removePrivacy.xml",
			request: "removePrivacy?domain=" + parityDomain,
			seed:    func(f *fakeNamesilo) { seedParityDomain(f).private = true },
		},
		{
			name:    "domainLock",
			fixture: "domainLock.xml",
			request: "domainLock?domain=" + parityDomain,
			seed:    func(f *fakeNamesilo) { seedParityDomain(f) },
		},
		{
			name:    "domainUnlock",
			fixture: "domainUnlock.xml",
			request: "domainUnlock?domain=" + parityDomain,
			seed:    func(f *fakeNamesilo) { seedParityDomain(f).locked = true },
		},
		{
			name:    "addAutoRenewal",
			fixture: "addAutoRenewal.xml",
			request: "addAutoRenewal?domain=" + parityDomain,
			seed:    func(f *fakeNamesilo) { seedParityDomain(f) },
		},
		{
			name:    "removeAutoRenewal",
			fixture: "removeAutoRenewal.xml",
			request: "removeAutoRenewal?domain=" + parityDomain,
			seed:    func(f *fakeNamesilo) { seedParityDomain(f).autoRenew = true },
		},
		{
			name:    "changeNameServers",
			fixture: "changeNameServers.xml",
			request: "changeNameServers?domain=" + parityDomain + "&ns1=NS1.DNSOWL.COM&ns2=NS2.DNSOWL.COM",
			seed:    func(f *fakeNamesilo) { seedParityDomain(f) },
		},
		{
			name:    "dnsSecAddRecord",
			fixture: "dnsSecAddRecord.xml",
			request: "dnsSecAddRecord?domain=" + parityDomain + "&keyTag=12345&alg=13&digestType=2&digest=" + parityDigest,
			seed:    func(f *fakeNamesilo) { seedParityDomain(f) },
		},
		{
			name:    "dnsSecDeleteRecord",
			fixture: "dnsSecDeleteRecord.xml",
			request: "dnsSecDeleteRecord?domain=" + parityDomain + "&keyTag=12345&alg=13&digestType=2&digest=" + parityDigest,
			seed: func(f *fakeNamesilo) {
				seedParityDomain(f)
				f.setDSRecords(parityDomain, []namesilo.DSRecord{
					{KeyTag: 12345, Algorithm: 13, DigestType: 2, Digest: parityDigest},
				})
			},
		},
	}

	if len(cases) == 0 {
		t.Fatal("no parity cases; the test is not doing what it claims")
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := newFakeNamesilo()
			defer fake.Close()
			tc.seed(fake)

			sep := "?"
			if strings.Contains(tc.request, "?") {
				sep = "&"
			}
			resp, err := http.Get(fake.URL() + "/" + tc.request + sep + "key=test-key")
			if err != nil {
				t.Fatalf("GET %s: %v", tc.request, err)
			}
			defer resp.Body.Close()
			got, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("reading the fake's reply: %v", err)
			}
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("fake replied HTTP %d: %s", resp.StatusCode, got)
			}

			wantShape := shapeFromXML(t, []byte(loadNameSiloFixture(t, tc.fixture)))
			gotShape := shapeFromXML(t, got)
			assertSameShape(t, tc.name, wantShape, gotShape)
		})
	}
}
