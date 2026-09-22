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

// contactSuccessReply is the smallest success envelope the write operations
// accept.
const contactSuccessReply = `<namesilo><reply><code>300</code><detail>success</detail></reply></namesilo>`

// contactAddReplyFixture is a contactAdd success whose reply carries the new
// profile's contact_id.
const contactAddReplyFixture = `<namesilo><reply><code>300</code><detail>success</detail><contact_id>c42</contact_id></reply></namesilo>`

// contactFullFixture is one contactList profile with every field set, in the
// shape NameSilo's own contactList replies have (§8.4): the reply uses the
// full snake_case element names, unlike the request's short parameter names.
const contactFullFixture = `<namesilo><reply>
  <code>300</code>
  <detail>success</detail>
  <contact>
    <contact_id>c1</contact_id>
    <default_profile>1</default_profile>
    <nickname>home</nickname>
    <company>Example Co</company>
    <first_name>Ada</first_name>
    <last_name>Lovelace</last_name>
    <address>1 Main St</address>
    <address2>Suite 2</address2>
    <city>Exampletown</city>
    <state>CA</state>
    <zip>90210</zip>
    <country>US</country>
    <email>ada@example.net</email>
    <phone>1-555-0100</phone>
    <fax>1-555-0101</fax>
    <us_nexus_category>C11</us_nexus_category>
    <us_application_purpose>P1</us_application_purpose>
    <ca_legal_form>CORP</ca_legal_form>
    <ca_language>EN</ca_language>
    <ca_agreement_version>1.0</ca_agreement_version>
    <ca_whois_display>PRIVATE</ca_whois_display>
    <eu_citizenship_country>DE</eu_citizenship_country>
  </contact>
</reply></namesilo>`

// contactListTwoFixture is two profiles: the "every profile" reply for an
// account with two of them.
const contactListTwoFixture = `<namesilo><reply>
  <code>300</code>
  <detail>success</detail>
  <contact>
    <contact_id>c1</contact_id>
    <default_profile>1</default_profile>
    <nickname>home</nickname>
    <company>Example Co</company>
    <first_name>Ada</first_name>
    <last_name>Lovelace</last_name>
    <address>1 Main St</address>
    <address2>Suite 2</address2>
    <city>Exampletown</city>
    <state>CA</state>
    <zip>90210</zip>
    <country>US</country>
    <email>ada@example.net</email>
    <phone>1-555-0100</phone>
    <fax>1-555-0101</fax>
    <us_nexus_category>C11</us_nexus_category>
    <us_application_purpose>P1</us_application_purpose>
    <ca_legal_form>CORP</ca_legal_form>
    <ca_language>EN</ca_language>
    <ca_agreement_version>1.0</ca_agreement_version>
    <ca_whois_display>PRIVATE</ca_whois_display>
    <eu_citizenship_country>DE</eu_citizenship_country>
  </contact>
  <contact>
    <contact_id>c2</contact_id>
    <default_profile>0</default_profile>
    <nickname>work</nickname>
    <company/>
    <first_name>Grace</first_name>
    <last_name>Hopper</last_name>
    <address>9 Fleet St</address>
    <address2/>
    <city>Arlington</city>
    <state>VA</state>
    <zip>22201</zip>
    <country>US</country>
    <email>grace@example.net</email>
    <phone>1-555-0199</phone>
    <fax/>
    <us_nexus_category/>
    <us_application_purpose/>
    <ca_legal_form/>
    <ca_language/>
    <ca_agreement_version/>
    <ca_whois_display/>
    <eu_citizenship_country/>
  </contact>
</reply></namesilo>`

// wantFullContact is what contactFullFixture's profile parses to.
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

// wantSecondContact is what the second profile in contactListTwoFixture parses
// to: the empty elements (<cp/>, <ad2/>, and so on) are empty strings, not
// errors.
var wantSecondContact = Contact{
	ID:        "c2",
	Nickname:  "work",
	FirstName: "Grace",
	LastName:  "Hopper",
	Address:   "9 Fleet St",
	City:      "Arlington",
	State:     "VA",
	Zip:       "22201",
	Country:   "US",
	Email:     "grace@example.net",
	Phone:     "1-555-0199",
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

// assertContactFields compares a parsed profile with the full fixture.
func assertContactFields(t *testing.T, got Contact) {
	t.Helper()
	if got != wantFullContact {
		t.Errorf("contact = %+v, want %+v", got, wantFullContact)
	}
}

func TestContactAddUpdateDeleteRequests(t *testing.T) {
	full := wantFullContact

	t.Run("contactAdd sends every field with the short API names", func(t *testing.T) {
		req := captureRequest(t, contactAddReplyFixture, func(c *Client) error {
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
		req := captureRequest(t, contactAddReplyFixture, func(c *Client) error {
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
		req := captureRequest(t, contactSuccessReply, func(c *Client) error {
			return c.UpdateContact(context.Background(), updating)
		})
		if req.path != "/contactUpdate" {
			t.Errorf("path = %q, want %q", req.path, "/contactUpdate")
		}
		assertParams(t, req.query, want)
	})

	t.Run("contactDelete sends only contact_id", func(t *testing.T) {
		req := captureRequest(t, contactSuccessReply, func(c *Client) error {
			return c.DeleteContact(context.Background(), "c42")
		})
		if req.path != "/contactDelete" {
			t.Errorf("path = %q, want %q", req.path, "/contactDelete")
		}
		assertParams(t, req.query, map[string]string{"contact_id": "c42"})
	})

	t.Run("contactAdd returns the reply's contact_id", func(t *testing.T) {
		srv := serveXML(t, contactAddReplyFixture)
		c := NewClient(srv.URL, "test-key", "test")

		id, err := c.AddContact(context.Background(), full)
		if err != nil {
			t.Fatalf("AddContact: %v", err)
		}
		if id != "c42" {
			t.Errorf("id = %q, want %q", id, "c42")
		}
	})

	t.Run("contactAdd without a contact_id element is an error", func(t *testing.T) {
		// A success-shaped reply that omits <contact_id> must not yield an
		// empty id in state; the error names the operation and the element.
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

// contactListLiveGolden is the exact contactList reply the real API returned
// for one profile, copied verbatim from the live capture and wrapped in the
// minimal success envelope. It pins the wire shape the fake used to compensate
// for: full snake_case element names, not the request's short forms.
const contactListLiveGolden = `<namesilo><reply><code>300</code><detail>success</detail><contact><contact_id>29145718</contact_id><default_profile>0</default_profile><nickname>tf-live-check</nickname><company>Terraform Provider Acceptance</company><first_name>Terraform</first_name><last_name>Acceptance</last_name><address>1 Test Way</address><address2/><city>Testville</city><state>TX</state><zip>73301</zip><country>US</country><email>terraform-test@example.com</email><phone>+1 512 555 0100</phone><fax/></contact></reply></namesilo>`

// TestContactListLiveGolden decodes the captured real reply as a raw-string
// golden and asserts every field. Address2 and fax arrive as empty elements and
// must decode to empty strings, not errors or leftovers.
func TestContactListLiveGolden(t *testing.T) {
	srv := serveXML(t, contactListLiveGolden)
	c := NewClient(srv.URL, "test-key", "test")

	contacts, err := c.ListContacts(context.Background(), "")
	if err != nil {
		t.Fatalf("ListContacts: %v", err)
	}
	if len(contacts) != 1 {
		t.Fatalf("contacts = %+v, want one profile", contacts)
	}
	want := Contact{
		ID:             "29145718",
		DefaultProfile: false,
		Nickname:       "tf-live-check",
		Company:        "Terraform Provider Acceptance",
		FirstName:      "Terraform",
		LastName:       "Acceptance",
		Address:        "1 Test Way",
		Address2:       "",
		City:           "Testville",
		State:          "TX",
		Zip:            "73301",
		Country:        "US",
		Email:          "terraform-test@example.com",
		Phone:          "+1 512 555 0100",
		Fax:            "",
	}
	if contacts[0] != want {
		t.Errorf("contact = %+v, want %+v", contacts[0], want)
	}
}

func TestContactList(t *testing.T) {
	t.Run("with a contact_id returns that one profile", func(t *testing.T) {
		requests := make(chan capturedRequest, 1)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests <- capturedRequest{path: r.URL.Path, query: r.URL.Query()}
			w.Header().Set("Content-Type", "text/xml")
			fmt.Fprint(w, contactFullFixture)
		}))
		defer srv.Close()
		c := NewClient(srv.URL, "test-key", "test")

		contacts, err := c.ListContacts(context.Background(), "c1")
		if err != nil {
			t.Fatalf("ListContacts: %v", err)
		}
		req := <-requests
		if req.path != "/contactList" {
			t.Errorf("path = %q, want %q", req.path, "/contactList")
		}
		if v := req.query.Get("contact_id"); v != "c1" {
			t.Errorf("contact_id = %q, want %q", v, "c1")
		}
		if len(contacts) != 1 {
			t.Fatalf("contacts = %+v, want one profile", contacts)
		}
		assertContactFields(t, contacts[0])
	})

	t.Run("without a contact_id sends the empty parameter and returns every profile", func(t *testing.T) {
		requests := make(chan capturedRequest, 1)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests <- capturedRequest{path: r.URL.Path, query: r.URL.Query()}
			w.Header().Set("Content-Type", "text/xml")
			fmt.Fprint(w, contactListTwoFixture)
		}))
		defer srv.Close()
		c := NewClient(srv.URL, "test-key", "test")

		contacts, err := c.ListContacts(context.Background(), "")
		if err != nil {
			t.Fatalf("ListContacts: %v", err)
		}
		req := <-requests
		// The contact_id parameter is always sent, even empty (§8.1): empty
		// means every profile.
		if !req.query.Has("contact_id") {
			t.Errorf("query %v is missing the empty contact_id parameter", req.query)
		}
		if v := req.query.Get("contact_id"); v != "" {
			t.Errorf("contact_id = %q, want empty", v)
		}
		if len(contacts) != 2 {
			t.Fatalf("contacts = %+v, want two profiles", contacts)
		}
		assertContactFields(t, contacts[0])
		if contacts[1] != wantSecondContact {
			t.Errorf("contacts[1] = %+v, want %+v", contacts[1], wantSecondContact)
		}
	})

	t.Run("zero profiles is an empty non-nil slice", func(t *testing.T) {
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
		// The second profile in the two-profile fixture carries <cp/>,
		// <ad2/>, <fx/>, and the country-specific elements as empty elements,
		// which is how the API spells "unset" (§8.4).
		srv := serveXML(t, contactListTwoFixture)
		c := NewClient(srv.URL, "test-key", "test")

		contacts, err := c.ListContacts(context.Background(), "")
		if err != nil {
			t.Fatalf("ListContacts: %v", err)
		}
		if len(contacts) != 2 {
			t.Fatalf("contacts = %+v, want two profiles", contacts)
		}
		got := contacts[1]
		if got != wantSecondContact {
			t.Errorf("contact = %+v, want %+v", got, wantSecondContact)
		}
	})

	t.Run("a single contact element is a one-element result", func(t *testing.T) {
		srv := serveXML(t, contactFullFixture)
		c := NewClient(srv.URL, "test-key", "test")

		contacts, err := c.ListContacts(context.Background(), "")
		if err != nil {
			t.Fatalf("ListContacts: %v", err)
		}
		if len(contacts) != 1 {
			t.Fatalf("contacts = %+v, want one element, not a scalar", contacts)
		}
		assertContactFields(t, contacts[0])
	})

	t.Run("default_profile parses 1 and 0", func(t *testing.T) {
		for _, tc := range []struct {
			value string
			want  bool
		}{
			{value: "1", want: true},
			{value: "0", want: false},
		} {
			t.Run(tc.value, func(t *testing.T) {
				fixture := fmt.Sprintf(`<namesilo><reply><code>300</code><detail>success</detail>
					<contact><contact_id>c1</contact_id><default_profile>%s</default_profile></contact>
					</reply></namesilo>`, tc.value)
				srv := serveXML(t, fixture)
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
		req := captureRequest(t, contactSuccessReply, func(c *Client) error {
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
		req := captureRequest(t, contactSuccessReply, func(c *Client) error {
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
		req := captureRequest(t, contactSuccessReply, func(c *Client) error {
			return c.AssociateContacts(context.Background(), "example.com", ContactRoles{})
		})
		assertParams(t, req.query, map[string]string{"domain": "example.com"})
	})
}
