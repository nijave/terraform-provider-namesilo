// SPDX-License-Identifier: GPL-3.0-or-later

package namesilo

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

// getDomainInfoFullFixture is the §8.3 shape: every scalar element, a
// nameservers list with position attributes, and a contact_ids block with the
// four roles.
const getDomainInfoFullFixture = `<namesilo><reply>
  <code>300</code>
  <detail>success</detail>
  <created>2020-01-02</created>
  <expires>2027-01-02</expires>
  <status>active</status>
  <locked>Yes</locked>
  <private>No</private>
  <auto_renew>Yes</auto_renew>
  <traffic_type>U</traffic_type>
  <email_verification_required>No</email_verification_required>
  <portfolio>default</portfolio>
  <forward_url>https://example.net</forward_url>
  <forward_type>301</forward_type>
  <nameservers>
    <nameserver position="1">ns1.example.net</nameserver>
    <nameserver position="2">ns2.example.net</nameserver>
    <nameserver position="3">ns3.example.net</nameserver>
  </nameservers>
  <contact_ids>
    <registrant>c1</registrant>
    <administrative>c2</administrative>
    <technical>c3</technical>
    <billing>c4</billing>
  </contact_ids>
</reply></namesilo>`

func serveXML(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/xml")
		fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// assertOnlyDomainParam checks a toggle request carried the fixed parameters,
// the domain, and nothing else.
func assertOnlyDomainParam(t *testing.T, q url.Values) {
	t.Helper()
	for _, fixed := range []string{"version", "type", "key"} {
		if !q.Has(fixed) {
			t.Errorf("query %v is missing the fixed %q parameter", q, fixed)
		}
	}
	if got := q.Get("domain"); got != "example.com" {
		t.Errorf("domain = %q, want %q", got, "example.com")
	}
	for k := range q {
		switch k {
		case "version", "type", "key", "domain":
		default:
			t.Errorf("unexpected parameter %q in query %v", k, q)
		}
	}
}

func TestGetDomainInfo(t *testing.T) {
	var gotPath string
	requests := make(chan capturedRequest, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		requests <- capturedRequest{path: r.URL.Path, query: r.URL.Query()}
		w.Header().Set("Content-Type", "text/xml")
		fmt.Fprint(w, getDomainInfoFullFixture)
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "test-key", "test")

	info, err := c.GetDomainInfo(context.Background(), "example.com")
	if err != nil {
		t.Fatalf("GetDomainInfo: %v", err)
	}

	got := <-requests
	if got.path != "/getDomainInfo" {
		t.Errorf("path = %q, want %q", got.path, "/getDomainInfo")
	}
	if v := got.query.Get("domain"); v != "example.com" {
		t.Errorf("domain = %q, want %q", v, "example.com")
	}

	if info.Created != "2020-01-02" {
		t.Errorf("Created = %q, want %q", info.Created, "2020-01-02")
	}
	if info.Expires != "2027-01-02" {
		t.Errorf("Expires = %q, want %q", info.Expires, "2027-01-02")
	}
	if info.Status != "active" {
		t.Errorf("Status = %q, want %q", info.Status, "active")
	}
	if !info.Locked {
		t.Error("Locked = false, want true")
	}
	if info.Private {
		t.Error("Private = true, want false")
	}
	if !info.AutoRenew {
		t.Error("AutoRenew = false, want true")
	}
	if info.TrafficType != "U" {
		t.Errorf("TrafficType = %q, want %q", info.TrafficType, "U")
	}
	if info.EmailVerificationRequired {
		t.Error("EmailVerificationRequired = true, want false")
	}
	if info.Portfolio != "default" {
		t.Errorf("Portfolio = %q, want %q", info.Portfolio, "default")
	}
	if info.ForwardURL != "https://example.net" {
		t.Errorf("ForwardURL = %q, want %q", info.ForwardURL, "https://example.net")
	}
	if info.ForwardType != "301" {
		t.Errorf("ForwardType = %q, want %q", info.ForwardType, "301")
	}
	wantNS := []string{"ns1.example.net", "ns2.example.net", "ns3.example.net"}
	if len(info.Nameservers) != len(wantNS) {
		t.Fatalf("Nameservers = %v, want %v", info.Nameservers, wantNS)
	}
	for i, want := range wantNS {
		if info.Nameservers[i] != want {
			t.Errorf("Nameservers[%d] = %q, want %q", i, info.Nameservers[i], want)
		}
	}
	wantContacts := ContactRoles{Registrant: "c1", Administrative: "c2", Technical: "c3", Billing: "c4"}
	if info.Contacts != wantContacts {
		t.Errorf("Contacts = %+v, want %+v", info.Contacts, wantContacts)
	}
	if gotPath != "/getDomainInfo" {
		t.Errorf("handler path = %q, want %q", gotPath, "/getDomainInfo")
	}
}

func TestGetDomainInfoQuirks(t *testing.T) {
	t.Run("nameservers come back raw", func(t *testing.T) {
		const fixture = `<namesilo><reply><code>300</code><detail>success</detail>
			<locked>Yes</locked><private>No</private><auto_renew>Yes</auto_renew>
			<email_verification_required>No</email_verification_required>
			<nameservers>
				<nameserver position="1">NS1.EXAMPLE.NET.</nameserver>
				<nameserver position="2">Ns2.Example.Net.</nameserver>
			</nameservers></reply></namesilo>`
		srv := serveXML(t, fixture)
		c := NewClient(srv.URL, "test-key", "test")

		info, err := c.GetDomainInfo(context.Background(), "example.com")
		if err != nil {
			t.Fatalf("GetDomainInfo: %v", err)
		}
		want := []string{"NS1.EXAMPLE.NET.", "Ns2.Example.Net."}
		if len(info.Nameservers) != len(want) {
			t.Fatalf("Nameservers = %v, want %v", info.Nameservers, want)
		}
		for i := range want {
			if info.Nameservers[i] != want[i] {
				t.Errorf("Nameservers[%d] = %q, want raw %q", i, info.Nameservers[i], want[i])
			}
		}
	})

	t.Run("Yes and No are case-insensitive", func(t *testing.T) {
		cases := []struct {
			value string
			want  bool
		}{
			{"Yes", true},
			{"yes", true},
			{"YES", true},
			{"No", false},
			{"no", false},
			{"nO", false},
		}
		for _, tc := range cases {
			got, err := parseYesNo("getDomainInfo", "locked", tc.value)
			if err != nil {
				t.Fatalf("parseYesNo(%q): %v", tc.value, err)
			}
			if got != tc.want {
				t.Errorf("parseYesNo(%q) = %v, want %v", tc.value, got, tc.want)
			}
		}
	})

	t.Run("an unrecognized boolean names the operation and field", func(t *testing.T) {
		const fixture = `<namesilo><reply><code>300</code><detail>success</detail>
			<locked>maybe</locked><private>No</private><auto_renew>Yes</auto_renew>
			<email_verification_required>No</email_verification_required>
			</reply></namesilo>`
		srv := serveXML(t, fixture)
		c := NewClient(srv.URL, "test-key", "test")

		_, err := c.GetDomainInfo(context.Background(), "example.com")
		if err == nil {
			t.Fatal("GetDomainInfo: nil error, want an error for a boolean that is neither Yes nor No")
		}
		if !strings.Contains(err.Error(), "getDomainInfo") {
			t.Errorf("error %q does not name the operation", err)
		}
		if !strings.Contains(err.Error(), "<locked>") {
			t.Errorf("error %q does not name the <locked> field", err)
		}
	})
}

func TestChangeNameServers(t *testing.T) {
	cases := []struct {
		name        string
		nameservers []string
	}{
		{
			name:        "two nameservers",
			nameservers: []string{"ns1.example.net", "ns2.example.net"},
		},
		{
			name: "thirteen nameservers",
			nameservers: []string{
				"ns1.example.net", "ns2.example.net", "ns3.example.net",
				"ns4.example.net", "ns5.example.net", "ns6.example.net",
				"ns7.example.net", "ns8.example.net", "ns9.example.net",
				"ns10.example.net", "ns11.example.net", "ns12.example.net",
				"ns13.example.net",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			requests := make(chan capturedRequest, 1)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests <- capturedRequest{path: r.URL.Path, query: r.URL.Query()}
				writeReply(w, "300", "success")
			}))
			defer srv.Close()
			c := NewClient(srv.URL, "test-key", "test")

			if err := c.ChangeNameServers(context.Background(), "example.com", tc.nameservers); err != nil {
				t.Fatalf("ChangeNameServers: %v", err)
			}

			got := <-requests
			if got.path != "/changeNameServers" {
				t.Errorf("path = %q, want %q", got.path, "/changeNameServers")
			}
			if v := got.query.Get("domain"); v != "example.com" {
				t.Errorf("domain = %q, want %q", v, "example.com")
			}
			for i, ns := range tc.nameservers {
				key := fmt.Sprintf("ns%d", i+1)
				if v := got.query.Get(key); v != ns {
					t.Errorf("%s = %q, want %q", key, v, ns)
				}
			}
			// Exactly len(nameservers) ns parameters, no spares.
			if v := got.query.Get(fmt.Sprintf("ns%d", len(tc.nameservers)+1)); v != "" {
				t.Errorf("unexpected extra nameserver parameter ns%d = %q", len(tc.nameservers)+1, v)
			}
			count := 0
			for k := range got.query {
				if strings.HasPrefix(k, "ns") {
					count++
				}
			}
			if count != len(tc.nameservers) {
				t.Errorf("sent %d ns parameters, want %d", count, len(tc.nameservers))
			}
		})
	}
}

func TestListDomains(t *testing.T) {
	t.Run("one page with maxBid ignored", func(t *testing.T) {
		const fixture = `<namesilo><reply><code>300</code><detail>success</detail>
			<domains>
				<domain created="2020-01-02" expires="2027-01-02">a.com</domain>
				<domain created="2021-03-04" expires="2028-03-04" maxBid="19.99">b.com</domain>
			</domains>
			<pager><total>2</total><pageSize>50</pageSize><page>1</page></pager>
			</reply></namesilo>`
		requests := make(chan capturedRequest, 1)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests <- capturedRequest{path: r.URL.Path, query: r.URL.Query()}
			w.Header().Set("Content-Type", "text/xml")
			fmt.Fprint(w, fixture)
		}))
		defer srv.Close()
		c := NewClient(srv.URL, "test-key", "test")

		list, err := c.ListDomains(context.Background(), 50)
		if err != nil {
			t.Fatalf("ListDomains: %v", err)
		}

		got := <-requests
		if got.path != "/listDomains" {
			t.Errorf("path = %q, want %q", got.path, "/listDomains")
		}
		if v := got.query.Get("page"); v != "1" {
			t.Errorf("page = %q, want %q", v, "1")
		}
		if v := got.query.Get("pageSize"); v != "50" {
			t.Errorf("pageSize = %q, want %q", v, "50")
		}

		if list.Total != 2 {
			t.Errorf("Total = %d, want 2", list.Total)
		}
		if list.Truncated {
			t.Error("Truncated = true, want false")
		}
		want := []DomainSummary{
			{Name: "a.com", Created: "2020-01-02", Expires: "2027-01-02"},
			{Name: "b.com", Created: "2021-03-04", Expires: "2028-03-04"},
		}
		if len(list.Domains) != len(want) {
			t.Fatalf("Domains = %+v, want %+v", list.Domains, want)
		}
		for i := range want {
			if list.Domains[i] != want[i] {
				t.Errorf("Domains[%d] = %+v, want %+v", i, list.Domains[i], want[i])
			}
		}
	})

	t.Run("multiple pages", func(t *testing.T) {
		var calls int64
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt64(&calls, 1)
			page := r.URL.Query().Get("page")
			w.Header().Set("Content-Type", "text/xml")
			switch page {
			case "1":
				fmt.Fprint(w, `<namesilo><reply><code>300</code><detail>success</detail>
					<domains>
						<domain created="2020-01-02" expires="2027-01-02">a.com</domain>
						<domain created="2020-01-02" expires="2027-01-02">b.com</domain>
					</domains>
					<pager><total>3</total><pageSize>2</pageSize><page>1</page></pager>
					</reply></namesilo>`)
			case "2":
				fmt.Fprint(w, `<namesilo><reply><code>300</code><detail>success</detail>
					<domains>
						<domain created="2020-01-02" expires="2027-01-02">c.com</domain>
					</domains>
					<pager><total>3</total><pageSize>2</pageSize><page>2</page></pager>
					</reply></namesilo>`)
			default:
				t.Errorf("unexpected page %q", page)
			}
		}))
		defer srv.Close()
		c := NewClient(srv.URL, "test-key", "test")

		list, err := c.ListDomains(context.Background(), 2)
		if err != nil {
			t.Fatalf("ListDomains: %v", err)
		}
		if list.Total != 3 {
			t.Errorf("Total = %d, want 3", list.Total)
		}
		if list.Truncated {
			t.Error("Truncated = true, want false")
		}
		var names []string
		for _, d := range list.Domains {
			names = append(names, d.Name)
		}
		if strings.Join(names, ",") != "a.com,b.com,c.com" {
			t.Errorf("Domains = %v, want a.com,b.com,c.com", names)
		}
		if n := atomic.LoadInt64(&calls); n != 2 {
			t.Errorf("handler calls = %d, want 2", n)
		}
	})

	t.Run("duplicate page guard", func(t *testing.T) {
		var calls int64
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt64(&calls, 1)
			w.Header().Set("Content-Type", "text/xml")
			fmt.Fprint(w, `<namesilo><reply><code>300</code><detail>success</detail>
				<domains>
					<domain created="2020-01-02" expires="2027-01-02">a.com</domain>
					<domain created="2020-01-02" expires="2027-01-02">b.com</domain>
				</domains>
				<pager><total>5</total><pageSize>2</pageSize><page>1</page></pager>
				</reply></namesilo>`)
		}))
		defer srv.Close()
		c := NewClient(srv.URL, "test-key", "test")

		list, err := c.ListDomains(context.Background(), 2)
		if err != nil {
			t.Fatalf("ListDomains: %v", err)
		}
		if list.Total != 5 {
			t.Errorf("Total = %d, want 5 (the API's last reported total)", list.Total)
		}
		if !list.Truncated {
			t.Error("Truncated = false, want true when paging stopped short of total")
		}
		if len(list.Domains) != 2 {
			t.Errorf("Domains = %+v, want exactly one page (2 rows)", list.Domains)
		}
		if n := atomic.LoadInt64(&calls); n != 2 {
			t.Errorf("handler calls = %d, want 2 (page 1 then the repeating page 2)", n)
		}
	})

	t.Run("empty account", func(t *testing.T) {
		srv := serveXML(t, `<namesilo><reply><code>300</code><detail>success</detail>
			<domains></domains>
			<pager><total>0</total><pageSize>50</pageSize><page>1</page></pager>
			</reply></namesilo>`)
		c := NewClient(srv.URL, "test-key", "test")

		list, err := c.ListDomains(context.Background(), 50)
		if err != nil {
			t.Fatalf("ListDomains: %v", err)
		}
		if list.Domains == nil {
			t.Error("Domains = nil, want an empty non-nil slice")
		}
		if len(list.Domains) != 0 {
			t.Errorf("Domains = %+v, want empty", list.Domains)
		}
		if list.Total != 0 {
			t.Errorf("Total = %d, want 0", list.Total)
		}
		if list.Truncated {
			t.Error("Truncated = true, want false")
		}
	})
}

func TestToggles(t *testing.T) {
	// toggleCodes are the per-operation already-in-state codes. Any code in
	// this set that is not the operation's own must be an error.
	toggleCodes := []string{"250", "251", "252", "253", "255", "256"}

	type toggle struct {
		name    string
		op      string
		ownCode string
		call    func(context.Context, *Client) error
	}
	toggles := []toggle{
		{name: "domainLock", op: "domainLock", ownCode: "252", call: func(ctx context.Context, c *Client) error {
			return c.DomainLock(ctx, "example.com")
		}},
		{name: "domainUnlock", op: "domainUnlock", ownCode: "253", call: func(ctx context.Context, c *Client) error {
			return c.DomainUnlock(ctx, "example.com")
		}},
		{name: "addAutoRenewal", op: "addAutoRenewal", ownCode: "250", call: func(ctx context.Context, c *Client) error {
			return c.AddAutoRenew(ctx, "example.com")
		}},
		{name: "removeAutoRenewal", op: "removeAutoRenewal", ownCode: "251", call: func(ctx context.Context, c *Client) error {
			return c.RemoveAutoRenew(ctx, "example.com")
		}},
		{name: "addPrivacy", op: "addPrivacy", ownCode: "255", call: func(ctx context.Context, c *Client) error {
			return c.AddPrivacy(ctx, "example.com")
		}},
		{name: "removePrivacy", op: "removePrivacy", ownCode: "256", call: func(ctx context.Context, c *Client) error {
			return c.RemovePrivacy(ctx, "example.com")
		}},
	}

	foreignFor := func(own string) string {
		for _, code := range toggleCodes {
			if code != own {
				return code
			}
		}
		return ""
	}

	for _, tc := range toggles {
		t.Run(tc.name, func(t *testing.T) {
			t.Run("request carries only domain and code 300 succeeds", func(t *testing.T) {
				requests := make(chan capturedRequest, 1)
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests <- capturedRequest{path: r.URL.Path, query: r.URL.Query()}
					writeReply(w, "300", "success")
				}))
				defer srv.Close()
				c := NewClient(srv.URL, "test-key", "test")

				if err := tc.call(context.Background(), c); err != nil {
					t.Fatalf("%s: %v", tc.name, err)
				}
				got := <-requests
				if got.path != "/"+tc.op {
					t.Errorf("path = %q, want %q", got.path, "/"+tc.op)
				}
				assertOnlyDomainParam(t, got.query)
			})

			t.Run("own already-in-state code succeeds", func(t *testing.T) {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					writeReply(w, tc.ownCode, "already in the requested state")
				}))
				defer srv.Close()
				c := NewClient(srv.URL, "test-key", "test")

				if err := tc.call(context.Background(), c); err != nil {
					t.Fatalf("%s with code %s: %v, want success", tc.name, tc.ownCode, err)
				}
			})

			t.Run("foreign already-in-state code is an error", func(t *testing.T) {
				foreign := foreignFor(tc.ownCode)
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					writeReply(w, foreign, "already in some other operation's state")
				}))
				defer srv.Close()
				c := NewClient(srv.URL, "test-key", "test")

				err := tc.call(context.Background(), c)
				if err == nil {
					t.Fatalf("%s with code %s: nil error, want *APIError", tc.name, foreign)
				}
				var apiErr *APIError
				if !errors.As(err, &apiErr) {
					t.Fatalf("%s: error = %v (%T), want *APIError", tc.name, err, err)
				}
				if apiErr.Code != foreign {
					t.Errorf("Code = %q, want %q", apiErr.Code, foreign)
				}
			})
		})
	}
}
