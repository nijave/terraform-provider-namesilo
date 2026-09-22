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
	"testing"
)

// writeReply writes a minimal NameSilo envelope carrying one reply code.
func writeReply(w http.ResponseWriter, code, detail string) {
	w.Header().Set("Content-Type", "text/xml")
	fmt.Fprintf(w, "<namesilo><reply><code>%s</code><detail>%s</detail></reply></namesilo>", code, detail)
}

// capturedRequest is a copy of the request the handler saw. The handler runs on
// its own goroutine, so the request is copied before it is sent on a channel.
type capturedRequest struct {
	path      string
	query     url.Values
	userAgent string
}

func TestCallRequestConstruction(t *testing.T) {
	requests := make(chan capturedRequest, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- capturedRequest{
			path:      r.URL.Path,
			query:     r.URL.Query(),
			userAgent: r.Header.Get("User-Agent"),
		}
		writeReply(w, "300", "success")
	}))
	defer srv.Close()

	// The trailing slash on the endpoint must be trimmed, so the path is
	// "/probe" and not "//probe".
	c := NewClient(srv.URL+"/", "test-key", "test")
	params := map[string]string{
		"domain": "example.com",
		"empty":  "",
	}
	if err := c.call(context.Background(), "probe", params, nil); err != nil {
		t.Fatalf("call: %v", err)
	}

	got := <-requests
	if got.path != "/probe" {
		t.Errorf("path = %q, want %q", got.path, "/probe")
	}
	if v := got.query.Get("version"); v != "1" {
		t.Errorf("version = %q, want %q", v, "1")
	}
	if v := got.query.Get("type"); v != "xml" {
		t.Errorf("type = %q, want %q", v, "xml")
	}
	if v := got.query.Get("key"); v != "test-key" {
		t.Errorf("key = %q, want %q", v, "test-key")
	}
	if v := got.query.Get("domain"); v != "example.com" {
		t.Errorf("domain = %q, want %q", v, "example.com")
	}
	// An explicitly empty parameter is still sent, so the request is
	// deterministic.
	if _, ok := got.query["empty"]; !ok {
		t.Error("the empty parameter was not sent")
	}
	if want := "terraform-provider-namesilo/test"; got.userAgent != want {
		t.Errorf("User-Agent = %q, want %q", got.userAgent, want)
	}
}

func TestReplyCodeClassification(t *testing.T) {
	cases := []struct {
		name      string
		operation string
		code      string
		detail    string
		wantErr   bool
	}{
		{name: "300 is success", operation: "probe", code: "300", detail: "success"},
		{name: "301 is success", operation: "probe", code: "301", detail: "success"},
		{name: "302 is success", operation: "probe", code: "302", detail: "success"},
		{name: "250 succeeds for addAutoRenewal", operation: "addAutoRenewal", code: "250", detail: "already set to AutoRenew"},
		{name: "251 succeeds for removeAutoRenewal", operation: "removeAutoRenewal", code: "251", detail: "already set not to AutoRenew"},
		{name: "252 succeeds for domainLock", operation: "domainLock", code: "252", detail: "already locked"},
		{name: "253 succeeds for domainUnlock", operation: "domainUnlock", code: "253", detail: "already unlocked"},
		{name: "255 succeeds for addPrivacy", operation: "addPrivacy", code: "255", detail: "already private"},
		{name: "256 succeeds for removePrivacy", operation: "removePrivacy", code: "256", detail: "already not private"},
		{name: "250 from probe is an error", operation: "probe", code: "250", detail: "already set to AutoRenew", wantErr: true},
		{name: "251 from probe is an error", operation: "probe", code: "251", detail: "already set not to AutoRenew", wantErr: true},
		{name: "252 from probe is an error", operation: "probe", code: "252", detail: "already locked", wantErr: true},
		{name: "253 from probe is an error", operation: "probe", code: "253", detail: "already unlocked", wantErr: true},
		{name: "255 from probe is an error", operation: "probe", code: "255", detail: "already private", wantErr: true},
		{name: "256 from probe is an error", operation: "probe", code: "256", detail: "already not private", wantErr: true},
		{name: "110 invalid API key", operation: "probe", code: "110", detail: "Invalid API key", wantErr: true},
		{name: "200 domain not active", operation: "probe", code: "200", detail: "Domain is not active", wantErr: true},
		{name: "400 still processing", operation: "probe", code: "400", detail: "Existing API request is still processing", wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				writeReply(w, tc.code, tc.detail)
			}))
			defer srv.Close()
			c := NewClient(srv.URL, "test-key", "test")

			err := c.call(context.Background(), tc.operation, nil, nil)
			if !tc.wantErr {
				if err != nil {
					t.Fatalf("call: %v, want success", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("call: nil error, want *APIError (code %s)", tc.code)
			}
			var apiErr *APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("error = %v (%T), want *APIError", err, err)
			}
			if apiErr.Operation != tc.operation {
				t.Errorf("Operation = %q, want %q", apiErr.Operation, tc.operation)
			}
			if apiErr.Code != tc.code {
				t.Errorf("Code = %q, want %q", apiErr.Code, tc.code)
			}
			if apiErr.Detail != tc.detail {
				t.Errorf("Detail = %q, want %q", apiErr.Detail, tc.detail)
			}
		})
	}
}

func TestReplyMissingCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "<namesilo><reply><detail>success</detail></reply></namesilo>")
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "test-key", "test")

	err := c.call(context.Background(), "probe", nil, nil)
	if err == nil {
		t.Fatal("call: nil error, want an error for a reply without <code>")
	}
	if !strings.Contains(err.Error(), "probe") {
		t.Errorf("error %q does not name the operation", err)
	}
}

func TestHTTPErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "test-key", "test")

	err := c.call(context.Background(), "probe", nil, nil)
	if err == nil {
		t.Fatal("call: nil error, want an error for a non-200 status")
	}
	if !strings.Contains(err.Error(), "probe") {
		t.Errorf("error %q does not name the operation", err)
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error %q does not name the HTTP status", err)
	}
	if strings.Contains(err.Error(), srv.URL) {
		t.Errorf("error %q leaks the request URL", err)
	}
}

func TestNonXMLBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "<namesilo><reply><code>300")
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "test-key", "test")

	err := c.call(context.Background(), "probe", nil, nil)
	if err == nil {
		t.Fatal("call: nil error, want an error for a non-XML body")
	}
	if !strings.Contains(err.Error(), "probe") {
		t.Errorf("error %q does not name the operation", err)
	}
}

func TestErrorsNeverContainTheAPIKey(t *testing.T) {
	const apiKey = "super-secret-api-key"
	shapes := []struct {
		name    string
		handler http.HandlerFunc
		// prepare, when set, runs after the server is created and may stop it
		// to force a transport failure.
		prepare func(*httptest.Server)
	}{
		{
			name: "HTTP 500",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			},
		},
		{
			name: "non-XML body",
			handler: func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, "<namesilo><reply><code>300")
			},
		},
		{
			name: "API error",
			handler: func(w http.ResponseWriter, r *http.Request) {
				writeReply(w, "110", "Invalid API key")
			},
		},
		{
			// Closing the server forces a transport failure. http.Client.Do
			// returns a *url.Error whose text embeds the full request URL,
			// whose query carries the key, so this shape pins the redaction
			// contract on the path where the leak actually happened.
			name:    "connection refused",
			handler: func(w http.ResponseWriter, r *http.Request) {},
			prepare: func(srv *httptest.Server) { srv.Close() },
		},
	}

	for _, shape := range shapes {
		t.Run(shape.name, func(t *testing.T) {
			srv := httptest.NewServer(shape.handler)
			defer srv.Close()
			if shape.prepare != nil {
				shape.prepare(srv)
			}
			c := NewClient(srv.URL, apiKey, "test")

			err := c.call(context.Background(), "probe", map[string]string{"domain": "example.com"}, nil)
			if err == nil {
				t.Fatal("call: nil error, want an error")
			}
			if strings.Contains(err.Error(), apiKey) {
				t.Fatalf("error leaks the API key: %q", err)
			}
			if strings.Contains(err.Error(), srv.URL) {
				t.Errorf("error leaks the request URL: %q", err)
			}
		})
	}
}
