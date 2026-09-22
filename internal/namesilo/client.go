// SPDX-License-Identifier: GPL-3.0-or-later

// Package namesilo is a client for the NameSilo registrar API. It is pure Go:
// no Terraform imports and no third-party dependencies, so every decision it
// makes is unit-testable without a plugin harness.
package namesilo

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
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

// throttleRetries is how many times a 503 is retried, and throttleBaseDelay is
// the first wait. The delays double each attempt (1s, 2s, 4s), so the added
// latency is bounded at 7 seconds.
const (
	throttleRetries   = 3
	throttleBaseDelay = time.Second
)

// call issues one operation and classifies its reply. params are sent
// verbatim (empty values included), and out, when non-nil, receives the whole
// XML body so an operation can decode its own fields.
//
// Errors are built from the operation, the reply code, the detail, the HTTP
// status, and (for a transport or request-build failure) the inner cause
// only — never from the request URL or its query, so the API key cannot leak
// into a diagnostic.
func (c *Client) call(ctx context.Context, operation string, params map[string]string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint+"/"+operation, nil)
	if err != nil {
		return fmt.Errorf("%s: building the request: %s", operation, redactedCause(err))
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

	resp, err := c.doThrottled(ctx, operation, req)
	if err != nil {
		return err
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

// doThrottled issues req, retrying an HTTP 503 response with a bounded
// backoff. The API answers request bursts with 503 and its own guidance is to
// retry after a wait, so a 503 is retried up to throttleRetries times with
// throttleBaseDelay, 2x, and 4x waits — about 7 seconds of added latency at
// most. Every other status is returned to the caller immediately, and every
// XML-level error code is classified by call, so only a 503 waits.
//
// A cancelled or expired context stops the wait and is reported as a request
// failure, the same shape as a transport failure. The inner cause is formatted
// rather than the *url.Error, because that error's text embeds the request URL
// whose query carries the API key.
func (c *Client) doThrottled(ctx context.Context, operation string, req *http.Request) (*http.Response, error) {
	for attempt := 0; ; attempt++ {
		resp, err := c.http.Do(req)
		if err != nil {
			// A transport failure's cause (timeout, DNS, TLS) is useful, but
			// http.Client.Do returns a *url.Error whose text embeds the full
			// request URL, and the URL's query carries the API key. Only the
			// inner cause is formatted, never the *url.Error itself.
			return nil, fmt.Errorf("%s: request failed: %s", operation, redactedCause(err))
		}
		if resp.StatusCode != http.StatusServiceUnavailable || attempt >= throttleRetries {
			return resp, nil
		}
		resp.Body.Close()
		if err := sleepContext(ctx, throttleBaseDelay<<attempt); err != nil {
			return nil, fmt.Errorf("%s: request failed: %s", operation, redactedCause(err))
		}
	}
}

// sleepContext waits for d or until ctx is done, whichever comes first.
func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// redactedCause returns the inner cause of a request error without the URL.
// http.Client.Do and url.Parse wrap their failures in *url.Error, whose text is
// "<op> <url>: <cause>": formatting it would echo the request URL, whose query
// carries the API key. The inner cause is both safe and the part an operator
// needs — "context deadline exceeded", "dial tcp: lookup ...", or the parse
// failure. A non-url error falls back to its own text.
func redactedCause(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		if ue.Err != nil {
			return ue.Err.Error()
		}
		return "unknown request error"
	}
	return err.Error()
}
