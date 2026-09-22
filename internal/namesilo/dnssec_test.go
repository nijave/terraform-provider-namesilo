// SPDX-License-Identifier: GPL-3.0-or-later

package namesilo

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// dnsSecListTwoFixture is the §8.3 shape: two ds_record elements whose
// children spell the fields the way the response does, full-word algorithm and
// camelCase keyTag and digestType. The first digest is uppercase on purpose, so
// the rawness contract is visible: the client returns the digest the API sent
// and the provider layer normalizes it.
const dnsSecListTwoFixture = `<namesilo><reply>
  <code>300</code>
  <detail>success</detail>
  <ds_record>
    <keyTag>12345</keyTag>
    <algorithm>8</algorithm>
    <digestType>2</digestType>
    <digest>A94F2C81E0D5B7A3F1C6D8E2B4A6C8D0E2F4A6C8D0E2F4A6C8D0E2F4A6C8D0E2</digest>
  </ds_record>
  <ds_record>
    <keyTag>65535</keyTag>
    <algorithm>13</algorithm>
    <digestType>1</digestType>
    <digest>0f1e2d3c4b5a69788796a5b4c3d2e1f0a9b8c7d6</digest>
  </ds_record>
</reply></namesilo>`

func TestListDSRecords(t *testing.T) {
	requests := make(chan capturedRequest, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- capturedRequest{path: r.URL.Path, query: r.URL.Query()}
		w.Header().Set("Content-Type", "text/xml")
		fmt.Fprint(w, dnsSecListTwoFixture)
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "test-key", "test")

	records, err := c.ListDSRecords(context.Background(), "example.com")
	if err != nil {
		t.Fatalf("ListDSRecords: %v", err)
	}

	got := <-requests
	if got.path != "/dnsSecListRecords" {
		t.Errorf("path = %q, want %q", got.path, "/dnsSecListRecords")
	}
	if v := got.query.Get("domain"); v != "example.com" {
		t.Errorf("domain = %q, want %q", v, "example.com")
	}

	want := []DSRecord{
		{KeyTag: 12345, Algorithm: 8, DigestType: 2, Digest: "A94F2C81E0D5B7A3F1C6D8E2B4A6C8D0E2F4A6C8D0E2F4A6C8D0E2F4A6C8D0E2"},
		{KeyTag: 65535, Algorithm: 13, DigestType: 1, Digest: "0f1e2d3c4b5a69788796a5b4c3d2e1f0a9b8c7d6"},
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

// dnsSecListRecordsLiveGolden is the exact reply the real API returned for a
// domain with one DS record, copied verbatim from the live capture. It pins the
// wire shape the fake used to compensate for: camelCase keyTag and digestType
// on the reply, full-word algorithm.
const dnsSecListRecordsLiveGolden = `<?xml version="1.0"?>
<namesilo><request><operation>dnsSecListRecords</operation><ip>203.0.113.7</ip></request><reply><code>300</code><detail>success</detail><ds_record><keyTag>12345</keyTag><algorithm>13</algorithm><digestType>2</digestType><digest>ABABABABABABABABABABABABABABABABABABABABABABABABABABABABABABAB</digest></ds_record></reply></namesilo>`

// TestListDSRecordsLiveGolden decodes the captured real reply as a raw-string
// golden. The table is deliberately one row today: it exists to make the
// verified wire shape an explicit test case, so a future edit that reverts the
// tags to snake_case fails here rather than only against the live API.
func TestListDSRecordsLiveGolden(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want []DSRecord
	}{
		{
			name: "camelCase reply elements",
			body: dnsSecListRecordsLiveGolden,
			want: []DSRecord{{
				KeyTag:     12345,
				Algorithm:  13,
				DigestType: 2,
				Digest:     "ABABABABABABABABABABABABABABABABABABABABABABABABABABABABABABAB",
			}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := serveXML(t, tc.body)
			c := NewClient(srv.URL, "test-key", "test")

			records, err := c.ListDSRecords(context.Background(), "example.com")
			if err != nil {
				t.Fatalf("ListDSRecords: %v", err)
			}
			if len(records) != len(tc.want) {
				t.Fatalf("records = %+v, want %+v", records, tc.want)
			}
			for i := range tc.want {
				if records[i] != tc.want[i] {
					t.Errorf("records[%d] = %+v, want %+v", i, records[i], tc.want[i])
				}
			}
		})
	}
}

func TestListDSRecordsEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeReply(w, "300", "success")
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "test-key", "test")

	records, err := c.ListDSRecords(context.Background(), "example.com")
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

func TestListDSRecordsMalformedInt(t *testing.T) {
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
			return c.AddDSRecord(context.Background(), "example.com", record)
		}},
		{name: "dnsSecDeleteRecord", call: func(c *Client) error {
			return c.DeleteDSRecord(context.Background(), "example.com", record)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requests := make(chan capturedRequest, 1)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests <- capturedRequest{path: r.URL.Path, query: r.URL.Query()}
				writeReply(w, "300", "success")
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
				"domain":     "example.com",
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
