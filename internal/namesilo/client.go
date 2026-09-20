// SPDX-License-Identifier: GPL-3.0-or-later

// Package namesilo is a client for the NameSilo registrar API. It is pure Go:
// no Terraform imports and no third-party dependencies, so every decision it
// makes is unit-testable without a plugin harness.
package namesilo

import (
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
