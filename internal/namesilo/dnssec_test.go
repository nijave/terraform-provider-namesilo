// SPDX-License-Identifier: GPL-3.0-or-later

package namesilo

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// dnsSecFixtureDigest is the digest the captured dnsSecListRecords reply
// carries. It is uppercase, which makes the rawness contract visible: the
// client returns the digest the API sent and the provider layer normalizes it.
const dnsSecFixtureDigest = "ABABABABABABABABABABABABABABABABABABABABABABABABABABABABABABABAB"

func TestListDSRecords(t *testing.T) {
	body := loadFixture(t, "dnsSecListRecords-with.xml")
	requests := make(chan capturedRequest, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- capturedRequest{path: r.URL.Path, query: r.URL.Query()}
		w.Header().Set("Content-Type", "text/xml")
		fmt.Fprint(w, body)
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "test-key", "test")

	records, err := c.ListDSRecords(context.Background(), fixtureDomain)
	if err != nil {
		t.Fatalf("ListDSRecords: %v", err)
	}

	got := <-requests
	if got.path != "/dnsSecListRecords" {
		t.Errorf("path = %q, want %q", got.path, "/dnsSecListRecords")
	}
	if v := got.query.Get("domain"); v != fixtureDomain {
		t.Errorf("domain = %q, want %q", v, fixtureDomain)
	}

	want := []DSRecord{
		{KeyTag: 12345, Algorithm: 13, DigestType: 2, Digest: dnsSecFixtureDigest},
	}
	if len(records) != len(want) {
		t.Fatalf("records = %+v, want %+v", records, want)
	}
	for i := range want {
		if records[i] != want[i] {
			t.Errorf("records[%d] = %+v, want %+v", i, records[i], want[i])
		}
	}
}

// TestListDSRecordsLiveGolden decodes the captured reply for a domain with one
// DS record and asserts every field. It pins the wire shape the fake used to
// compensate for: camelCase keyTag and digestType on the reply, full-word
// algorithm.
func TestListDSRecordsLiveGolden(t *testing.T) {
	srv := serveXML(t, loadFixture(t, "dnsSecListRecords-with.xml"))
	c := NewClient(srv.URL, "test-key", "test")

	records, err := c.ListDSRecords(context.Background(), fixtureDomain)
	if err != nil {
		t.Fatalf("ListDSRecords: %v", err)
	}
	want := []DSRecord{{
		KeyTag:     12345,
		Algorithm:  13,
		DigestType: 2,
		Digest:     dnsSecFixtureDigest,
	}}
	if len(records) != len(want) {
		t.Fatalf("records = %+v, want %+v", records, want)
	}
	for i := range want {
		if records[i] != want[i] {
			t.Errorf("records[%d] = %+v, want %+v", i, records[i], want[i])
		}
	}
}

func TestListDSRecordsEmpty(t *testing.T) {
	// The captured unsigned-domain reply: code 300 with a detail saying no DS
	// records exist, and no <ds_record> element at all.
	srv := serveXML(t, loadFixture(t, "dnsSecListRecords-empty.xml"))
	c := NewClient(srv.URL, "test-key", "test")

	records, err := c.ListDSRecords(context.Background(), fixtureDomain)
	if err != nil {
		t.Fatalf("ListDSRecords: %v", err)
	}
	if records == nil {
		t.Fatal("records = nil, want an empty non-nil slice (an unsigned domain is not an error)")
	}
	if len(records) != 0 {
		t.Errorf("records = %+v, want empty", records)
	}
}

func TestListDSRecordsDropsEmptyElement(t *testing.T) {
	// Synthetic shape fixture: a bare <ds_record/> is how the API spells
	// "nothing", and the capture does not contain one, so these stay inline.
	t.Run("bare element alone", func(t *testing.T) {
		srv := serveXML(t, `<namesilo><reply><code>300</code><detail>success</detail><ds_record/></reply></namesilo>`)
		c := NewClient(srv.URL, "test-key", "test")

		records, err := c.ListDSRecords(context.Background(), "example.com")
		if err != nil {
			t.Fatalf("ListDSRecords: %v", err)
		}
		if records == nil {
			t.Fatal("records = nil, want an empty non-nil slice")
		}
		if len(records) != 0 {
			t.Errorf("records = %+v, want the bare <ds_record/> dropped", records)
		}
	})

	t.Run("mixed with a real record", func(t *testing.T) {
		const fixture = `<namesilo><reply><code>300</code><detail>success</detail>
			<ds_record/>
			<ds_record>
				<keyTag>12345</keyTag>
				<algorithm>8</algorithm>
				<digestType>2</digestType>
				<digest>A94F2C81E0D5B7A3F1C6D8E2B4A6C8D0E2F4A6C8D0E2F4A6C8D0E2F4A6C8D0E2</digest>
			</ds_record>
			<ds_record></ds_record>
			</reply></namesilo>`
		srv := serveXML(t, fixture)
		c := NewClient(srv.URL, "test-key", "test")

		records, err := c.ListDSRecords(context.Background(), "example.com")
		if err != nil {
			t.Fatalf("ListDSRecords: %v", err)
		}
		want := []DSRecord{{KeyTag: 12345, Algorithm: 8, DigestType: 2, Digest: "A94F2C81E0D5B7A3F1C6D8E2B4A6C8D0E2F4A6C8D0E2F4A6C8D0E2F4A6C8D0E2"}}
		if len(records) != len(want) {
			t.Fatalf("records = %+v, want %+v (the empty elements are dropped)", records, want)
		}
		if records[0] != want[0] {
			t.Errorf("records[0] = %+v, want %+v", records[0], want[0])
		}
	})
}

// TestDeleteDSRecordUppercasesDigest pins the delete wire case. The API stores
// digests uppercased and matches a delete request case-sensitively, so a
// lowercase digest (the provider's state form) must be sent uppercased or the
// API answers code 210. The caller's record is not mutated: state stays
// lowercase for diff equality.
func TestDeleteDSRecordUppercasesDigest(t *testing.T) {
	const lower = "a94f2c81e0d5b7a3f1c6d8e2b4a6c8d0e2f4a6c8d0e2f4a6c8d0e2f4a6c8d0e2"
	record := DSRecord{KeyTag: 12345, Algorithm: 8, DigestType: 2, Digest: lower}

	body := loadFixture(t, "dnsSecDeleteRecord.xml")
	requests := make(chan capturedRequest, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- capturedRequest{path: r.URL.Path, query: r.URL.Query()}
		w.Header().Set("Content-Type", "text/xml")
		fmt.Fprint(w, body)
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "test-key", "test")

	if err := c.DeleteDSRecord(context.Background(), fixtureDomain, record); err != nil {
		t.Fatalf("DeleteDSRecord: %v", err)
	}

	got := <-requests
	if got.path != "/dnsSecDeleteRecord" {
		t.Errorf("path = %q, want %q", got.path, "/dnsSecDeleteRecord")
	}
	if v := got.query.Get("digest"); v != strings.ToUpper(lower) {
		t.Errorf("delete digest = %q, want the uppercased stored form %q", v, strings.ToUpper(lower))
	}
	if record.Digest != lower {
		t.Errorf("the record's digest became %q; the state form must stay %q", record.Digest, lower)
	}
}

// TestDeleteDSRecordToleratesPending210 pins the pending-record tolerance. A DS
// record that has just been added and has not activated yet cannot be deleted:
// the API answers code 210 "There are no active records specified for deletion"
// and the delete takes effect anyway, so the code is treated as success. The
// reconcile deletes records moments after adding them, so this is routine.
func TestDeleteDSRecordToleratesPending210(t *testing.T) {
	// The captured reply for deleting a still-pending record: code 210 "There
	// are no active records specified for deletion". The delete takes effect
	// anyway, so the code is treated as success.
	srv := serveXML(t, loadFixture(t, "dnsSecDeleteRecord-immediate.xml"))
	c := NewClient(srv.URL, "test-key", "test")

	record := DSRecord{KeyTag: 12345, Algorithm: 8, DigestType: 2, Digest: "A94F2C81E0D5B7A3F1C6D8E2B4A6C8D0E2F4A6C8D0E2F4A6C8D0E2F4A6C8D0E2"}
	if err := c.DeleteDSRecord(context.Background(), fixtureDomain, record); err != nil {
		t.Fatalf("DeleteDSRecord with a pending-record 210: %v, want success", err)
	}
}

// TestPending210IsPerOperation pins that the 210 tolerance belongs to
// dnsSecDeleteRecord alone: the same code from another operation is still an
// error, because the classification is per operation. The captured duplicate
// add is the real 210 for dnsSecAddRecord (a duplicate DS add is a policy
// error, not a 25x tolerance code).
func TestPending210IsPerOperation(t *testing.T) {
	srv := serveXML(t, loadFixture(t, "dnsSecAddRecord-duplicate.xml"))
	c := NewClient(srv.URL, "test-key", "test")

	record := DSRecord{KeyTag: 12345, Algorithm: 8, DigestType: 2, Digest: "A94F2C81E0D5B7A3F1C6D8E2B4A6C8D0E2F4A6C8D0E2F4A6C8D0E2F4A6C8D0E2"}
	err := c.AddDSRecord(context.Background(), fixtureDomain, record)
	if err == nil {
		t.Fatal("AddDSRecord with code 210: nil error, want *APIError")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %v (%T), want *APIError", err, err)
	}
	if apiErr.Code != "210" {
		t.Errorf("Code = %q, want %q", apiErr.Code, "210")
	}
	if apiErr.Operation != "dnsSecAddRecord" {
		t.Errorf("Operation = %q, want %q", apiErr.Operation, "dnsSecAddRecord")
	}
	if !strings.Contains(apiErr.Detail, "Parameter value policy error") {
		t.Errorf("Detail = %q, want the captured policy-error detail", apiErr.Detail)
	}
}

func TestListDSRecordsMalformedInt(t *testing.T) {
	// Synthetic fault injection: a non-integer keyTag, which the capture does
	// not contain.
	const digest = "B1C2D3E4F5A6B7C8D9E0F1A2B3C4D5E6F7A8B9C0D1E2F3A4B5C6D7E8F9A0B1C2"
	const fixture = `<namesilo><reply><code>300</code><detail>success</detail>
		<ds_record>
			<keyTag>abc</keyTag>
			<algorithm>8</algorithm>
			<digestType>2</digestType>
			<digest>` + digest + `</digest>
		</ds_record>
		</reply></namesilo>`
	srv := serveXML(t, fixture)
	c := NewClient(srv.URL, "test-key", "test")

	records, err := c.ListDSRecords(context.Background(), "example.com")
	if err == nil {
		t.Fatalf("ListDSRecords = %+v, want an error for a malformed keyTag", records)
	}
	if !strings.Contains(err.Error(), "keyTag") {
		t.Errorf("error %q does not name the field", err)
	}
	if !strings.Contains(err.Error(), "dnsSecListRecords") {
		t.Errorf("error %q does not name the operation", err)
	}
	// The error names the field only (§8.4): never the record's digest and
	// never the API key.
	if strings.Contains(err.Error(), digest) {
		t.Errorf("error %q contains the record's digest", err)
	}
	if strings.Contains(err.Error(), "test-key") {
		t.Errorf("error %q leaks the API key", err)
	}
}

func TestAddDeleteDSRecordRequests(t *testing.T) {
	record := DSRecord{
		KeyTag:     12345,
		Algorithm:  8,
		DigestType: 2,
		// Uppercase on purpose: the request carries the digest unchanged and
		// the provider layer normalizes it at plan time (§8.4).
		Digest: "A94F2C81E0D5B7A3F1C6D8E2B4A6C8D0E2F4A6C8D0E2F4A6C8D0E2F4A6C8D0E2",
	}

	for _, tc := range []struct {
		name string
		call func(c *Client) error
	}{
		{name: "dnsSecAddRecord", call: func(c *Client) error {
			return c.AddDSRecord(context.Background(), fixtureDomain, record)
		}},
		{name: "dnsSecDeleteRecord", call: func(c *Client) error {
			return c.DeleteDSRecord(context.Background(), fixtureDomain, record)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := loadFixture(t, tc.name+".xml")
			requests := make(chan capturedRequest, 1)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests <- capturedRequest{path: r.URL.Path, query: r.URL.Query()}
				w.Header().Set("Content-Type", "text/xml")
				fmt.Fprint(w, body)
			}))
			defer srv.Close()
			c := NewClient(srv.URL, "test-key", "test")

			if err := tc.call(c); err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}

			got := <-requests
			if got.path != "/"+tc.name {
				t.Errorf("path = %q, want %q", got.path, "/"+tc.name)
			}
			wantParams := map[string]string{
				"domain":     fixtureDomain,
				"digest":     record.Digest,
				"keyTag":     "12345",
				"digestType": "2",
				"alg":        "8",
			}
			for k, want := range wantParams {
				if v := got.query.Get(k); v != want {
					t.Errorf("%s = %q, want %q", k, v, want)
				}
			}
			for k := range got.query {
				switch k {
				case "version", "type", "key", "domain", "digest", "keyTag", "digestType", "alg":
				default:
					t.Errorf("unexpected parameter %q in query %v", k, got.query)
				}
			}
			// The abbreviated alg parameter is the request-side contract
			// (§12.1): the API takes alg on writes and answers with the
			// full-word <algorithm> element on lists.
			if _, ok := got.query["algorithm"]; ok {
				t.Error("the request carried algorithm; the request parameter is alg")
			}
		})
	}
}
