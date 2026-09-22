// SPDX-License-Identifier: GPL-3.0-or-later

package namesilo

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// contactSuccessReply is a synthetic minimal success envelope. The corpus has
// no captured empty-contactList reply, and the "success without contact_id"
// case is fault injection, so those two assertions stay inline.
const contactSuccessReply = `<namesilo><reply><code>300</code><detail>success</detail></reply></namesilo>`

// wantDefaultContact is what contactList-single.xml and contactList-all.xml
// parse to: the account default profile, sanitized. company, address2, and
// fax arrive as empty elements and decode to empty strings.
var wantDefaultContact = Contact{
	ID:             "29145691",
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

// wantCreatedContact is what contactList-created.xml parses to: a created test
// profile that is not the account default.
var wantCreatedContact = Contact{
	ID:             "29145829",
	DefaultProfile: false,
	Nickname:       "tf-fixture-probe",
	FirstName:      "Terraform",
	LastName:       "Acceptance",
	Address:        "1 Test Way",
	City:           "Testville",
	State:          "TX",
	Zip:            "73301",
	Country:        "US",
	Email:          "terraform-test@example.com",
	Phone:          "+1 512 555 0100",
}

// wantFullContact is the complete Contact the request-construction tests send,
// with every field set (including the country-specific ones the captured
// profiles leave unset). It exercises the request spelling, not a reply shape.
var wantFullContact = Contact{
	ID:                   "c1",
	DefaultProfile:       true,
	Nickname:             "home",
	Company:              "Example Co",
	FirstName:            "Ada",
	LastName:             "Lovelace",
	Address:              "1 Main St",
	Address2:             "Suite 2",
	City:                 "Exampletown",
	State:                "CA",
	Zip:                  "90210",
	Country:              "US",
	Email:                "ada@example.net",
	Phone:                "1-555-0100",
	Fax:                  "1-555-0101",
	UsNexusCategory:      "C11",
	UsApplicationPurpose: "P1",
	CaLegalForm:          "CORP",
	CaLanguage:           "EN",
	CaAgreementVersion:   "1.0",
	CaWhoisDisplay:       "PRIVATE",
	EuCitizenshipCountry: "DE",
}

// captureRequest runs run against a fresh client pointed at a server serving
// body, then returns the request the server saw.
func captureRequest(t *testing.T, body string, run func(*Client) error) capturedRequest {
	t.Helper()
	requests := make(chan capturedRequest, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- capturedRequest{path: r.URL.Path, query: r.URL.Query()}
		w.Header().Set("Content-Type", "text/xml")
		fmt.Fprint(w, body)
	}))
	defer srv.Close()

	if err := run(NewClient(srv.URL, "test-key", "test")); err != nil {
		t.Fatalf("call: %v", err)
	}
	return <-requests
}

// assertParams checks the query carries exactly the wanted parameters (the
// fixed version, type, and key aside), each present even when its value is
// empty: the always-send rule (§8.1).
func assertParams(t *testing.T, q url.Values, want map[string]string) {
	t.Helper()
	for k, v := range want {
		if !q.Has(k) {
			t.Errorf("query %v is missing the %q parameter", q, k)
			continue
		}
		if got := q.Get(k); got != v {
			t.Errorf("parameter %q = %q, want %q", k, got, v)
		}
	}
	for k := range q {
		if _, ok := want[k]; ok {
			continue
		}
		switch k {
		case "version", "type", "key":
		default:
			t.Errorf("unexpected parameter %q in query %v", k, q)
		}
	}
}

// fullContactParams is what a Contact with every field set sends, under the
// API's short parameter names (§8.3).
var fullContactParams = map[string]string{
	"nn":   "home",
	"cp":   "Example Co",
	"fn":   "Ada",
	"ln":   "Lovelace",
	"ad":   "1 Main St",
	"ad2":  "Suite 2",
	"cy":   "Exampletown",
	"st":   "CA",
	"zp":   "90210",
	"ct":   "US",
	"em":   "ada@example.net",
	"ph":   "1-555-0100",
	"fx":   "1-555-0101",
	"usnc": "C11",
	"usap": "P1",
	"calf": "CORP",
	"caln": "EN",
	"caag": "1.0",
	"cawd": "PRIVATE",
	"eucs": "DE",
}

// assertContactEquals compares a parsed profile with the wanted one.
func assertContactEquals(t *testing.T, got, want Contact) {
	t.Helper()
	if got != want {
		t.Errorf("contact = %+v, want %+v", got, want)
	}
}

func TestContactAddUpdateDeleteRequests(t *testing.T) {
	full := wantFullContact

	t.Run("contactAdd sends every field with the short API names", func(t *testing.T) {
		req := captureRequest(t, loadFixture(t, "contactAdd.xml"), func(c *Client) error {
			_, err := c.AddContact(context.Background(), full)
			return err
		})
		if req.path != "/contactAdd" {
			t.Errorf("path = %q, want %q", req.path, "/contactAdd")
		}
		assertParams(t, req.query, fullContactParams)
		if _, ok := req.query["contact_id"]; ok {
			t.Error("contactAdd sent a contact_id parameter")
		}
	})

	t.Run("contactAdd sends empty optionals too", func(t *testing.T) {
		bare := Contact{
			FirstName: "Ada",
			LastName:  "Lovelace",
			Address:   "1 Main St",
			City:      "Exampletown",
			State:     "CA",
			Zip:       "90210",
			Country:   "US",
			Email:     "ada@example.net",
			Phone:     "1-555-0100",
		}
		want := map[string]string{
			"fn": "Ada", "ln": "Lovelace", "ad": "1 Main St", "ad2": "",
			"cy": "Exampletown", "st": "CA", "zp": "90210", "ct": "US",
			"em": "ada@example.net", "ph": "1-555-0100", "fx": "",
			"nn": "", "cp": "",
			"usnc": "", "usap": "", "calf": "", "caln": "", "caag": "", "cawd": "", "eucs": "",
		}
		req := captureRequest(t, loadFixture(t, "contactAdd.xml"), func(c *Client) error {
			_, err := c.AddContact(context.Background(), bare)
			return err
		})
		assertParams(t, req.query, want)
	})

	t.Run("contactUpdate adds contact_id", func(t *testing.T) {
		updating := full
		updating.ID = "c42"
		want := map[string]string{"contact_id": "c42"}
		for k, v := range fullContactParams {
			want[k] = v
		}
		req := captureRequest(t, loadFixture(t, "contactUpdate.xml"), func(c *Client) error {
			return c.UpdateContact(context.Background(), updating)
		})
		if req.path != "/contactUpdate" {
			t.Errorf("path = %q, want %q", req.path, "/contactUpdate")
		}
		assertParams(t, req.query, want)
	})

	t.Run("contactDelete sends only contact_id", func(t *testing.T) {
		req := captureRequest(t, loadFixture(t, "contactDelete.xml"), func(c *Client) error {
			return c.DeleteContact(context.Background(), "c42")
		})
		if req.path != "/contactDelete" {
			t.Errorf("path = %q, want %q", req.path, "/contactDelete")
		}
		assertParams(t, req.query, map[string]string{"contact_id": "c42"})
	})

	t.Run("contactAdd returns the reply's contact_id", func(t *testing.T) {
		srv := serveXML(t, loadFixture(t, "contactAdd.xml"))
		c := NewClient(srv.URL, "test-key", "test")

		id, err := c.AddContact(context.Background(), full)
		if err != nil {
			t.Fatalf("AddContact: %v", err)
		}
		if id != "29145829" {
			t.Errorf("id = %q, want the fixture's %q", id, "29145829")
		}
	})

	t.Run("contactAdd without a contact_id element is an error", func(t *testing.T) {
		// Synthetic fault injection: a success-shaped reply that omits
		// <contact_id> must not yield an empty id in state; the error names the
		// operation and the element.
		srv := serveXML(t, contactSuccessReply)
		c := NewClient(srv.URL, "test-key", "test")

		id, err := c.AddContact(context.Background(), full)
		if err == nil {
			t.Fatalf("AddContact = %q, want an error for a reply without <contact_id>", id)
		}
		if !strings.Contains(err.Error(), "contactAdd") {
			t.Errorf("error %q does not name the operation", err)
		}
		if !strings.Contains(err.Error(), "<contact_id>") {
			t.Errorf("error %q does not name the element", err)
		}
	})
}

// TestContactListLiveGolden decodes the captured contactList reply and asserts
// every field. It pins the wire shape the fake used to compensate for: full
// snake_case element names, not the request's short forms. Address2 and fax
// arrive as empty elements and must decode to empty strings, not errors or
// leftovers.
func TestContactListLiveGolden(t *testing.T) {
	srv := serveXML(t, loadFixture(t, "contactList-created.xml"))
	c := NewClient(srv.URL, "test-key", "test")

	contacts, err := c.ListContacts(context.Background(), "")
	if err != nil {
		t.Fatalf("ListContacts: %v", err)
	}
	if len(contacts) != 1 {
		t.Fatalf("contacts = %+v, want one profile", contacts)
	}
	if contacts[0].Address2 != "" {
		t.Errorf("Address2 = %q, want empty", contacts[0].Address2)
	}
	if contacts[0].Fax != "" {
		t.Errorf("Fax = %q, want empty", contacts[0].Fax)
	}
	assertContactEquals(t, contacts[0], wantCreatedContact)
}

func TestContactList(t *testing.T) {
	t.Run("with a contact_id returns that one profile", func(t *testing.T) {
		body := loadFixture(t, "contactList-created.xml")
		requests := make(chan capturedRequest, 1)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests <- capturedRequest{path: r.URL.Path, query: r.URL.Query()}
			w.Header().Set("Content-Type", "text/xml")
			fmt.Fprint(w, body)
		}))
		defer srv.Close()
		c := NewClient(srv.URL, "test-key", "test")

		contacts, err := c.ListContacts(context.Background(), "29145829")
		if err != nil {
			t.Fatalf("ListContacts: %v", err)
		}
		req := <-requests
		if req.path != "/contactList" {
			t.Errorf("path = %q, want %q", req.path, "/contactList")
		}
		if v := req.query.Get("contact_id"); v != "29145829" {
			t.Errorf("contact_id = %q, want %q", v, "29145829")
		}
		if len(contacts) != 1 {
			t.Fatalf("contacts = %+v, want one profile", contacts)
		}
		assertContactEquals(t, contacts[0], wantCreatedContact)
	})

	t.Run("without a contact_id sends the empty parameter and returns every profile", func(t *testing.T) {
		body := loadFixture(t, "contactList-all.xml")
		requests := make(chan capturedRequest, 1)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests <- capturedRequest{path: r.URL.Path, query: r.URL.Query()}
			w.Header().Set("Content-Type", "text/xml")
			fmt.Fprint(w, body)
		}))
		defer srv.Close()
		c := NewClient(srv.URL, "test-key", "test")

		contacts, err := c.ListContacts(context.Background(), "")
		if err != nil {
			t.Fatalf("ListContacts: %v", err)
		}
		req := <-requests
		// The contact_id parameter is always sent, even empty (§8.1): empty
		// means every profile. The captured account has one profile, the
		// default.
		if !req.query.Has("contact_id") {
			t.Errorf("query %v is missing the empty contact_id parameter", req.query)
		}
		if v := req.query.Get("contact_id"); v != "" {
			t.Errorf("contact_id = %q, want empty", v)
		}
		if len(contacts) != 1 {
			t.Fatalf("contacts = %+v, want one profile", contacts)
		}
		assertContactEquals(t, contacts[0], wantDefaultContact)
	})

	t.Run("zero profiles is an empty non-nil slice", func(t *testing.T) {
		// Synthetic success envelope: the corpus has no captured contactList
		// reply with no <contact> element.
		srv := serveXML(t, contactSuccessReply)
		c := NewClient(srv.URL, "test-key", "test")

		contacts, err := c.ListContacts(context.Background(), "")
		if err != nil {
			t.Fatalf("ListContacts: %v", err)
		}
		if contacts == nil {
			t.Fatal("contacts = nil, want an empty non-nil slice")
		}
		if len(contacts) != 0 {
			t.Errorf("contacts = %+v, want empty", contacts)
		}
	})
}

func TestContactParsing(t *testing.T) {
	t.Run("empty elements normalize to empty strings", func(t *testing.T) {
		// contactList-single.xml carries <company/>, <address2/>, and <fax/>
		// as empty elements, which is how the API spells "unset" (§8.4).
		srv := serveXML(t, loadFixture(t, "contactList-single.xml"))
		c := NewClient(srv.URL, "test-key", "test")

		contacts, err := c.ListContacts(context.Background(), "")
		if err != nil {
			t.Fatalf("ListContacts: %v", err)
		}
		if len(contacts) != 1 {
			t.Fatalf("contacts = %+v, want one profile", contacts)
		}
		got := contacts[0]
		if got.Company != "" || got.Address2 != "" || got.Fax != "" {
			t.Errorf("empty elements = company %q, address2 %q, fax %q; want all empty",
				got.Company, got.Address2, got.Fax)
		}
		assertContactEquals(t, got, wantDefaultContact)
	})

	t.Run("a single contact element is a one-element result", func(t *testing.T) {
		srv := serveXML(t, loadFixture(t, "contactList-single.xml"))
		c := NewClient(srv.URL, "test-key", "test")

		contacts, err := c.ListContacts(context.Background(), "")
		if err != nil {
			t.Fatalf("ListContacts: %v", err)
		}
		if len(contacts) != 1 {
			t.Fatalf("contacts = %+v, want one element, not a scalar", contacts)
		}
		assertContactEquals(t, contacts[0], wantDefaultContact)
	})

	t.Run("default_profile parses 1 and 0", func(t *testing.T) {
		// The two captured profiles carry the two legal values: the account
		// default profile is 1, the created test profile is 0.
		for _, tc := range []struct {
			fixture string
			want    bool
		}{
			{fixture: "contactList-single.xml", want: true},
			{fixture: "contactList-created.xml", want: false},
		} {
			t.Run(tc.fixture, func(t *testing.T) {
				srv := serveXML(t, loadFixture(t, tc.fixture))
				c := NewClient(srv.URL, "test-key", "test")

				contacts, err := c.ListContacts(context.Background(), "")
				if err != nil {
					t.Fatalf("ListContacts: %v", err)
				}
				if len(contacts) != 1 {
					t.Fatalf("contacts = %+v, want one profile", contacts)
				}
				if contacts[0].DefaultProfile != tc.want {
					t.Errorf("DefaultProfile = %v, want %v", contacts[0].DefaultProfile, tc.want)
				}
			})
		}
	})

	t.Run("default_profile any other value is an error", func(t *testing.T) {
		// Synthetic fault injection: an out-of-range default_profile, which
		// the capture does not contain.
		for _, value := range []string{"yes", "2", ""} {
			fixture := fmt.Sprintf(`<namesilo><reply><code>300</code><detail>success</detail>
				<contact><contact_id>c1</contact_id><default_profile>%s</default_profile></contact>
				</reply></namesilo>`, value)
			srv := serveXML(t, fixture)
			c := NewClient(srv.URL, "test-key", "test")

			contacts, err := c.ListContacts(context.Background(), "")
			if err == nil {
				t.Fatalf("ListContacts = %+v, want an error for <default_profile> %q", contacts, value)
			}
			if !strings.Contains(err.Error(), "default_profile") {
				t.Errorf("error %q does not name the field", err)
			}
			if !strings.Contains(err.Error(), "contactList") {
				t.Errorf("error %q does not name the operation", err)
			}
			if strings.Contains(err.Error(), "test-key") {
				t.Errorf("error %q leaks the API key", err)
			}
		}
	})
}

func TestAssociateContacts(t *testing.T) {
	t.Run("sends every non-empty role", func(t *testing.T) {
		req := captureRequest(t, loadFixture(t, "contactDomainAssociate.xml"), func(c *Client) error {
			return c.AssociateContacts(context.Background(), "example.com", ContactRoles{
				Registrant:     "c1",
				Administrative: "c2",
				Technical:      "c3",
				Billing:        "c4",
			})
		})
		if req.path != "/contactDomainAssociate" {
			t.Errorf("path = %q, want %q", req.path, "/contactDomainAssociate")
		}
		assertParams(t, req.query, map[string]string{
			"domain":         "example.com",
			"registrant":     "c1",
			"administrative": "c2",
			"technical":      "c3",
			"billing":        "c4",
		})
	})

	t.Run("sends only the non-empty roles", func(t *testing.T) {
		req := captureRequest(t, loadFixture(t, "contactDomainAssociate.xml"), func(c *Client) error {
			return c.AssociateContacts(context.Background(), "example.com", ContactRoles{
				Registrant: "c1",
				Technical:  "c3",
			})
		})
		assertParams(t, req.query, map[string]string{
			"domain":     "example.com",
			"registrant": "c1",
			"technical":  "c3",
		})
	})

	t.Run("no roles sends only the domain", func(t *testing.T) {
		req := captureRequest(t, loadFixture(t, "contactDomainAssociate.xml"), func(c *Client) error {
			return c.AssociateContacts(context.Background(), "example.com", ContactRoles{})
		})
		assertParams(t, req.query, map[string]string{"domain": "example.com"})
	})
}
