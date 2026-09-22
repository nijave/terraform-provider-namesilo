// SPDX-License-Identifier: GPL-3.0-or-later

// Package namesilo is a client for the NameSilo registrar API. It is pure Go:
// no Terraform imports and no third-party dependencies, so every decision it
// makes is unit-testable without a plugin harness.
package namesilo

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DefaultEndpoint is the production NameSilo API.
const DefaultEndpoint = "https://www.namesilo.com/api"

// Client talks to the NameSilo API. Every operation is one HTTP GET whose
// query carries the fixed version and type parameters, the API key, and the
// operation's own parameters.
type Client struct {
	endpoint string
	apiKey   string
	version  string
	// PageSize is the provider-level page size used by ListDomains; the
	// provider sets it at Configure time.
	PageSize int64
	http     *http.Client
}

// NewClient builds a Client. The endpoint is stored without a trailing
// slash; version lands in the User-Agent as
// terraform-provider-namesilo/<version>. The HTTP timeout is 30 seconds,
// which covers every operation this provider calls (§5: no timeout attribute
// in v1).
func NewClient(endpoint, apiKey, version string) *Client {
	return &Client{
		endpoint: strings.TrimSuffix(endpoint, "/"),
		apiKey:   apiKey,
		version:  version,
		http:     &http.Client{Timeout: 30 * time.Second},
	}
}

// replyHeader is the part of every reply the client classifies: the code every
// operation returns and the human-readable detail that accompanies it.
type replyHeader struct {
	Code   string `xml:"code"`
	Detail string `xml:"detail"`
}

// call issues one operation and classifies its reply. params are sent
// verbatim (empty values included), and out, when non-nil, receives the whole
// XML body so an operation can decode its own fields.
//
// Errors are built from the operation, the reply code, the detail, and the
// HTTP status only, never from the request URL or its query, so the API key
// cannot leak into a diagnostic.
func (c *Client) call(ctx context.Context, operation string, params map[string]string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint+"/"+operation, nil)
	if err != nil {
		return fmt.Errorf("%s: building the request: %w", operation, err)
	}
	q := req.URL.Query()
	q.Set("version", "1")
	q.Set("type", "xml")
	q.Set("key", c.apiKey)
	for k, v := range params {
		q.Set(k, v)
	}
	req.URL.RawQuery = q.Encode()
	req.Header.Set("User-Agent", "terraform-provider-namesilo/"+c.version)

	resp, err := c.http.Do(req)
	if err != nil {
		// http.Client.Do returns a *url.Error whose text embeds the full
		// request URL, and the URL's query carries the API key, so the cause
		// is deliberately dropped rather than wrapped or echoed.
		return fmt.Errorf("%s: request failed", operation)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: unexpected HTTP status %d", operation, resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("%s: reading the response body: %w", operation, err)
	}
	var header struct {
		Reply replyHeader `xml:"reply"`
	}
	if err := xml.Unmarshal(body, &header); err != nil {
		return fmt.Errorf("%s: decoding the XML reply: %w", operation, err)
	}
	if header.Reply.Code == "" {
		return fmt.Errorf("%s: the reply has no <code> element", operation)
	}
	if !successCodes[header.Reply.Code] && alreadyInState[operation] != header.Reply.Code {
		return &APIError{Operation: operation, Code: header.Reply.Code, Detail: header.Reply.Detail}
	}
	if out == nil {
		return nil
	}
	if err := xml.Unmarshal(body, out); err != nil {
		return fmt.Errorf("%s: decoding the XML reply: %w", operation, err)
	}
	return nil
}
