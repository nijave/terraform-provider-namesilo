# Terraform Provider NameSilo Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build `terraform-provider-namesilo`, an OpenTofu (protocol 6) provider managing registrar-level state at NameSilo: delegation nameservers, DS records, WHOIS privacy, contact profiles and associations, registry lock, auto-renew, plus six read-only data sources.

**Architecture:** Three layers. `internal/namesilo` is a stdlib-only HTTP/XML client with pure normalization and diff helpers, unit-tested with `httptest` and XML fixtures, guarded by a boundary test against Terraform imports. `internal/provider` is a thin translation between framework `types.*` values and those client operations. `internal/provider` tests run three tiers: pure unit, hermetic harness tests (`IsUnitTest` + in-process protocol-6 factories + an in-memory fake NameSilo API) and opt-in live acceptance tests.

**Tech Stack:** Go 1.25.12, terraform-plugin-framework v1.19.0, terraform-plugin-framework-validators v0.19.0, terraform-plugin-go v0.31.0, terraform-plugin-testing v1.16.0, tfplugindocs v0.25.0 (separate `tools` module), goreleaser, OpenTofu ≥ 1.10 (`tofu` only in CI).

**Spec:** `docs/superpowers/specs/2026-09-20-terraform-provider-namesilo-design.md` — the plan argues from it and the executor reads both side-by-side. Section references below (§) point into it.

## Execution configuration

- **Implementer dispatches** run through the opencode-delegate relay (`opencode` CLI, `build` agent, auto-approve). Two model lanes only: mechanical tasks → `openrouter/z-ai/glm-5.3-flash`; harder tasks → `opencode-go/deepseek-v4.1-flash`.
- **Task mapping:** `glm-5.3-flash` → Tasks 1, 3, 5, 6, 9, 10, 13, 14, 15, 16. `deepseek-v4.1-flash` → Tasks 2, 4, 7, 8, 11, 12.
- **Task reviewers:** read-only dispatches (`--read-only`, plan agent) on `deepseek-v4.1-flash`; scoped re-reviews of small mechanical fix diffs on `glm-5.3-flash`.
- **Fix rounds 1-3:** `--resume-last` (same model). **Rounds 4-5:** fresh implementer one tier up (flash → deepseek-v4.1-flash). If a deepseek implementer exhausts the loop, stop and surface to the human.
- **Final whole-branch review:** `openrouter/z-ai/glm-5.3` with high reasoning (human-authorized metered dispatch, decision recorded 2026-09-20).
- **Commits land directly on main** (human consent, recorded 2026-09-20). OpenCode never commits; the controller re-runs gates, reads the diff, and lands each task's commit after its review passes.
- **Worktree:** none (working on main by consent). Ledger: `.superpowers/sdd/2026-09-20-terraform-provider-namesilo/progress.md`.

## Global Constraints

- Module `github.com/nijave/terraform-provider-namesilo`; `go 1.25.12` directive; dependency set exactly: framework v1.19.0, framework-validators v0.19.0, plugin-go v0.31.0, plugin-testing v1.16.0 (same as the siblings).
- Provider address `registry.opentofu.org/nijave/namesilo`; type name `namesilo`; protocol 6; OpenTofu ≥ 1.10 floor; CI drives `tofu` only.
- Every `.go` file starts (first line) with `// SPDX-License-Identifier: GPL-3.0-or-later`; LICENSE is GPL-3.0-or-later (copy from `../terraform-provider-accumulator/LICENSE`).
- `internal/namesilo` imports **zero** `terraform-plugin-*`/`github.com/opentofu/*` packages and **zero** third-party packages (stdlib only) — enforced by `boundary_test.go` and the CI license job.
- The API key must never appear in any error string, diagnostic, or log line — enforced by a dedicated unit test (§8.4).
- User-facing string literals (descriptions, diagnostics, skip messages) use a real em dash `—`, never `--` — enforced by the house-style test. Go *comments* may use `--`.
- Reply codes are matched as strings, never parsed as ints (§8.2). 250/251/252/253/255/256 are success **only for their own operation**.
- `id` is the domain for six resources; `namesilo_contact.id` is the API's `contact_id`. `domain` forces replacement everywhere it exists.
- No automatic retries; code 400 is surfaced (§2 non-goals).
- Read never removes a resource from state on an API error except `namesilo_contact` (§11).
- All tests hermetic except `TestAccLive*`, which are opt-in via environment variables (§12.3).
- `make test` (or plain `go test ./...`) must pass on a machine with tofu on PATH and also on one without (harness tests skip). `gofmt -l` empty and `go vet` clean before every commit.
- Reference sibling: `/home/nick/Documents/workspace/go/src/github.com/nijave/terraform-provider-accumulator` (paths below as `$ACC`). Copy-then-rename is the established adaptation pattern.

---

### Task 1: Module bootstrap, provider configuration, house-style tests

**Files:**
- Create: `go.mod`, `main.go`, `LICENSE`, `.gitignore`, `GNUmakefile`, `README.md` (one-paragraph placeholder, expanded in Task 15)
- Create: `internal/namesilo/client.go` (struct + `NewClient` only), `internal/namesilo/boundary_test.go`
- Create: `internal/provider/provider.go`, `internal/provider/client.go`, `internal/provider/provider_test.go`, `internal/provider/client_test.go`

**Interfaces (produced, used by every later task):**
- `namesilo.NewClient(endpoint, apiKey, version string) *Client`; exported consts `namesilo.DefaultEndpoint = "https://www.namesilo.com/api"`; exported field `Client.PageSize int64` (set by Configure so the domains data source can page; `NewClient`'s signature stays as §8.3 specifies)
- `provider.New(version string) func() provider.Provider`; `namesiloProvider` struct
- Test helpers in `provider_test.go`: `testAccProtoV6ProviderFactories` (keyed `"namesilo"`), `TestMain`/`tofuPath`, `requireTofu(t)`, `testAccPreCheck(t)`, `expectEmptyAfterRefresh()`, `stringSet(...)`; expected-type slices `expectedResourceTypes`, `expectedDataSourceTypes` (empty now; every later task appends its type names — **all executors must do this**)

- [ ] **Step 1: Write `go.mod`, `main.go`, `LICENSE`, `.gitignore`, `GNUmakefile`**

`go.mod` (then `go mod tidy`):
```
module github.com/nijave/terraform-provider-namesilo

go 1.25.12

require (
	github.com/hashicorp/terraform-plugin-framework v1.19.0
	github.com/hashicorp/terraform-plugin-framework-validators v0.19.0
	github.com/hashicorp/terraform-plugin-go v0.31.0
	github.com/hashicorp/terraform-plugin-testing v1.16.0
)
```

`main.go`: copy `$ACC/main.go`, change the import path to `.../terraform-provider-namesilo/internal/provider` and `Address: "registry.opentofu.org/nijave/namesilo"`.

`.gitignore`, `LICENSE`: copy from `$ACC/` verbatim.

`GNUmakefile` (test per §14: sets `TF_ACC_TERRAFORM_PATH` when tofu present; `testacc-live` per §12.3; `docs` target is inert until Task 15):
```make
default: test

.PHONY: build
build:
	go build -o dist/ ./...

.PHONY: test
test:
	@if command -v tofu >/dev/null 2>&1; then \
		TF_ACC_TERRAFORM_PATH="$$(command -v tofu)" TF_ACC_PROVIDER_HOST=registry.opentofu.org \
			go test ./... -timeout 20m; \
	else \
		go test ./... -timeout 20m; \
	fi

.PHONY: testacc-live
testacc-live:
	@command -v tofu >/dev/null || (echo "tofu not found in PATH; OpenTofu >= 1.10 is required" && exit 1)
	TF_ACC=1 TF_ACC_TERRAFORM_PATH="$$(command -v tofu)" TF_ACC_PROVIDER_HOST=registry.opentofu.org \
		go test ./internal/provider/ -run 'TestAccLive' -v $(TESTARGS) -timeout 60m

.PHONY: fmt
fmt:
	gofmt -w -l .

.PHONY: vet
vet:
	go vet ./...

.PHONY: release
release:
	@test $${RELEASE_VERSION?Please set environment variable RELEASE_VERSION}
	@git tag $$RELEASE_VERSION
	@git push origin $$RELEASE_VERSION

.PHONY: docs
docs:
	./tools/gen-schema.sh
	cd tools && go generate ./...
```

- [ ] **Step 2: Write `internal/namesilo/client.go` and `boundary_test.go`**

```go
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
```

`boundary_test.go`: copy `$ACC/internal/accumulate/boundary_test.go` verbatim (same three forbidden prefixes `github.com/hashicorp/terraform-`, `github.com/hashicorp/terraform/`, `github.com/opentofu/`).

- [ ] **Step 3: Write `internal/provider/provider.go` and `client.go`**

`provider.go`: `namesiloProvider{version string}`, `New(version)`, `Metadata` (`resp.TypeName = "namesilo"`), `Schema` with the three §5 attributes (`api_key`: Optional+Sensitive; `endpoint`: Optional; `page_size`: Optional, `int64default.StaticInt64(100)`, `int64validator.OneOf(20, 50, 100, 200, 500)`), empty `Resources`/`DataSources` (each later task appends its constructor). Structure copied from `$ACC/internal/provider/provider.go`.

`client.go` — the `Configure` implementation plus the two testable resolvers:

```go
// SPDX-License-Identifier: GPL-3.0-or-later

package provider

import (
	"context"
	"errors"
	"os"

	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	int64d "github.com/hashicorp/terraform-plugin-framework/schema/int64default"

	"github.com/nijave/terraform-provider-namesilo/internal/namesilo"
)

func resolveAPIKey(configured string) (string, error) {
	if configured != "" {
		return configured, nil
	}
	if key := os.Getenv("NAMESILO_API_KEY"); key != "" {
		return key, nil
	}
	return "", errors.New("the api_key attribute is empty and the NAMESILO_API_KEY environment variable is not set; configure one of them")
}

func resolveEndpoint(configured string) string {
	if configured != "" {
		return configured
	}
	if endpoint := os.Getenv("NAMESILO_API_ENDPOINT"); endpoint != "" {
		return endpoint
	}
	return namesilo.DefaultEndpoint
}

func (p *namesiloProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var config providerConfigModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	apiKey, err := resolveAPIKey(config.APIKey.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Missing NameSilo API key", err.Error())
		return
	}
	client := namesilo.NewClient(resolveEndpoint(config.Endpoint.ValueString()), apiKey, p.version)
	pageSize := int64(100)
	if !config.PageSize.IsNull() {
		pageSize = config.PageSize.ValueInt64()
	}
	client.PageSize = pageSize
	resp.ResourceData = client
	resp.DataSourceData = client
}
```
(`providerConfigModel{APIKey, Endpoint types.String; PageSize types.Int64}` with `tfsdk` tags lives here too.)

- [ ] **Step 4: Write `provider_test.go` and `client_test.go`**

`provider_test.go`: assemble from `$ACC/internal/provider/provider_test.go` (TestProviderSchema, TestUserFacingStringsUseEmDashes, TestEveryGoFileHasTheSPDXHeader — copy those three whole), plus the new pieces §12.2 requires (the siblings have none of these; write fresh):

```go
var testAccProtoV6ProviderFactories = map[string]func() (tfprotov6.ProviderServer, error){
	"namesilo": providerserver.NewProtocol6WithError(provider.New("test")()),
}

var expectedResourceTypes []string    // appended by each resource task
var expectedDataSourceTypes []string  // appended by Task 13

var tofuPath string

func TestMain(m *testing.M) {
	if p := os.Getenv("TF_ACC_TERRAFORM_PATH"); p != "" {
		tofuPath = p
	} else if p, err := exec.LookPath("tofu"); err == nil {
		tofuPath = p
		_ = os.Setenv("TF_ACC_TERRAFORM_PATH", p)
	}
	_ = os.Setenv("TF_ACC_PROVIDER_HOST", "registry.opentofu.org")
	os.Exit(m.Run())
}

// requireTofu skips when no OpenTofu binary was found, so `go test ./...`
// still passes on a machine without it. Run `make test` for the full suite.
func requireTofu(t *testing.T) {
	t.Helper()
	if tofuPath == "" {
		t.Skip("tofu not found; the harness tests need OpenTofu. Run `make test` with tofu on PATH.")
	}
}
```

`testAccPreCheck`: copy `$ACC`'s (TF_ACC skip; `TF_ACC_TERRAFORM_PATH` fatal naming `make testacc`; `TF_ACC_PROVIDER_HOST == registry.opentofu.org` fatal). `TestProviderSchema`: copy `$ACC`'s and drive the loop over `expectedResourceTypes`/`expectedDataSourceTypes` plus assert the provider schema itself has `api_key` (Sensitive), `endpoint`, `page_size`. `expectEmptyAfterRefresh()` and `stringSet`: copy `$ACC`'s (`stringList` renamed).

`client_test.go`: table-driven tests for `resolveAPIKey` (configured wins; env fallback; both empty → error) and `resolveEndpoint` (configured wins; env fallback; default), using `t.Setenv`.

- [ ] **Step 5: Run and verify**

Run: `go mod tidy && go test ./...` then `gofmt -l .` and `go vet ./...`
Expected: all pass (boundary/house tests run; nothing skips locally — tofu 1.12.1 is on PATH). `go build ./...` succeeds.

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum main.go LICENSE .gitignore GNUmakefile README.md internal/
git commit -m "feat: module bootstrap, provider schema, and house-style tests"
```

---

### Task 2: Client request/response core and error classification

**Files:**
- Modify: `internal/namesilo/client.go` (add `call`)
- Create: `internal/namesilo/errors.go`, `internal/namesilo/client_test.go`

**Interfaces (produced, used by Tasks 4-6 and the redaction contract):**
- `func (c *Client) call(ctx context.Context, operation string, params map[string]string, out any) error` — unexported; `out` is always a pointer to an operation-specific envelope struct whose reply struct embeds `replyHeader`
- `type replyHeader struct { Code string \`xml:"code"\`; Detail string \`xml:"detail"\` }`
- `type APIError struct { Operation, Code, Detail string }` with `Error() string` = `"NameSilo API error (%s): %s (code %s)"` (Operation, Detail, Code)

- [ ] **Step 1: Write the failing tests** in `client_test.go` (all use `httptest`, no tofu needed):
  1. `TestCallRequestConstruction` — assert path `/probe`, `version=1`, `type=xml`, `key` present, params always sent (assert an explicitly empty param appears), endpoint trailing slash trimmed, `User-Agent` is `terraform-provider-namesilo/test`.
  2. `TestReplyCodeClassification` — table: 300/301/302 succeed; 250 succeeds for `addAutoRenewal` but is `*APIError` from `probe`; likewise 251/`removeAutoRenewal`, 252/`domainLock`, 253/`domainUnlock`, 255/`addPrivacy`, 256/`removePrivacy`; 110/200/400 from `probe` are `*APIError` with `Operation`, `Code`, `Detail` set.
  3. `TestReplyMissingCode` — reply without `<code>` errors.
  4. `TestHTTPErrors` — non-200 status error names the operation and the status, never the URL.
  5. `TestNonXMLBody` — non-XML body errors naming the operation.
  6. `TestErrorsNeverContainTheAPIKey` — the three failure shapes (HTTP 500, non-XML body, API error) produce errors that do not contain the key string (search `err.Error()`).

- [ ] **Step 2: Run** `go test ./internal/namesilo/ -run 'TestCall|TestReply|TestHTTP|TestNonXML|TestErrors' -v` — expected: fail to compile (`call` undefined).

- [ ] **Step 3: Implement** `errors.go` (`APIError`, `successCodes` map {300,301,302}, `alreadyInState` map addAutoRenewal→250, removeAutoRenewal→251, domainLock→252, domainUnlock→253, addPrivacy→255, removePrivacy→256) and in `client.go`:

```go
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
		return fmt.Errorf("%s: request failed: %w", operation, err)
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
```
Errors are built from operation, code, detail, and HTTP status only — never the URL or the params (that is the redaction contract).

- [ ] **Step 4: Run** the Step 1 tests — expected: PASS. Then `go test ./...`, `gofmt -l .`, `go vet ./...`.

- [ ] **Step 5: Commit** — `git commit -m "feat: namesilo client request core and error classification"`

---

### Task 3: Normalization and diff logic

**Files:**
- Create: `internal/namesilo/normalize.go`, `internal/namesilo/normalize_test.go`

**Interfaces (produced, used by Tasks 4, 7, 8, 13):** exactly §9's signatures: `NormalizeNameserver(string) string`, `NormalizeNameservers([]string) []string` (drops empties, keeps order, no dedup), `SameNameservers(a, b []string) bool`, `NormalizeDSRecord(DSRecord) DSRecord`, `(DSRecord) Key() string`, `DiffDSRecords(current, desired []DSRecord) (add, remove []DSRecord)` — both sorted by `Key`. `DSRecord` (from §8.3) is defined here since Tasks 5-8 need it: `{KeyTag, Algorithm, DigestType int64; Digest string}`.

- [ ] **Step 1: Write the failing table-driven tests** per §12.1 "Pure helpers": lowercase+trim+trailing-dot (one dot only); empty-entry dropping with order preserved; `SameNameservers` on case/whitespace/dot variants and different lengths; digest lowercase/trim; `DiffDSRecords` with empty inputs, adds-only, removes-only, both in one diff, case-only digest difference (no diff), duplicate canonical tuples collapsing, and deterministic ordering (assert both results' `Key()` sequences are sorted).

- [ ] **Step 2: Run** `go test ./internal/namesilo/ -run 'TestNormalize|TestSame|TestDiff|TestKey' -v` — expected: compile failure.

- [ ] **Step 3: Implement**. `Key()` joins `KeyTag`, `Algorithm`, `DigestType`, `Digest` with a separator that cannot occur in any field (unit separator `\x1f`); `DiffDSRecords` builds two canonical maps via `NormalizeDSRecord`, set-differences both ways, sorts by `Key()`, returns non-nil slices.

- [ ] **Step 4: Run** tests — PASS; full `go test ./...`, gofmt, vet.

- [ ] **Step 5: Commit** — `git commit -m "feat: nameserver and DS record normalization helpers"`

---

### Task 4: Domain, privacy, and inventory operations

**Files:**
- Create: `internal/namesilo/domain.go`, `internal/namesilo/privacy.go`, `internal/namesilo/domain_test.go`

**Interfaces (produced, used by Tasks 7-13):**
- `type ContactRoles struct { Registrant, Administrative, Technical, Billing string }` (empty string means "not managed", §8.3)
- `type DomainInfo struct { Created, Expires, Status string; Locked, Private, AutoRenew, EmailVerificationRequired bool; TrafficType, Portfolio, ForwardURL, ForwardType string; Nameservers []string; Contacts ContactRoles }`
- `type DomainSummary struct { Name, Created, Expires string }`; `type DomainList struct { Domains []DomainSummary; Total int64; Truncated bool }` — `Truncated` is the addition the §7 warning needs: true when the duplicate-page guard stopped paging short of `pager.total`
- Methods: `GetDomainInfo(ctx, domain) (DomainInfo, error)`, `ChangeNameServers(ctx, domain, nameservers []string) error` (params `domain` + `ns1`…`nsN`), `ListDomains(ctx, pageSize int64) (DomainList, error)` (params `page`, `pageSize`; loops until `total` seen or a page adds no new names → `Truncated`), `DomainLock`/`DomainUnlock`/`AddAutoRenew`/`RemoveAutoRenew(ctx, domain) error`, `AddPrivacy`/`RemovePrivacy(ctx, domain) error` (privacy.go, same shape)

- [ ] **Step 1: Write the failing tests** (§12.1 "Client request/response parsing" + "Privacy and toggles" + "Domain inventory"), fixtures as Go string constants:
  1. `TestGetDomainInfo` — full fixture per §8.3 (all scalar elements, `nameservers > nameserver` with `position` attrs, `contact_ids` with four roles); assert every field.
  2. `TestGetDomainInfoQuirks` — uppercase nameserver names with trailing dots come back raw (provider normalizes); `Yes`/`no`/`YES` accepted case-insensitively; a `locked` value of `maybe` errors naming the operation and `<locked>` (§8.4: no guessing).
  3. `TestChangeNameServers` — request assertions: `ns1`…`nsN` params for 2 and 13 nameservers; reply code 300.
  4. `TestListDomains` — one page; multi-page (page 1 and 2 fixtures, `pager.total` reached); duplicate-page guard (handler always returning page 1: returns one page, `Truncated=true`, `Total` from the reply); empty account (zero `domain` elements, `total` 0, no error); `DomainSummary` reads chardata + `created`/`expires` attrs and ignores `maxBid` (§8.4); request asserts `page`/`pageSize`.
  5. `TestToggles` — for each of `domainLock`, `domainUnlock`, `addAutoRenewal`, `removeAutoRenewal`, `addPrivacy`, `removePrivacy`: request has only `domain`; code 300 succeeds; the operation's own already-in-state code succeeds; a *foreign* already-in-state code is an error (per-operation classification).

- [ ] **Step 2: Run** — expected compile failure.

- [ ] **Step 3: Implement.** Shared helpers: `parseYesNo(operation, field, value string) (bool, error)` (case-insensitive `yes`/`no`, else error naming operation and field — used for `locked`, `private`, `auto_renew`, `email_verification_required`); envelope structs embedding `replyHeader`; nameserver XML struct `{Position string \`xml:"position,attr"\`; Value string \`xml:",chardata"\`}`; pager parsed with `strconv.ParseInt` (error naming `<total>` on failure). Nameservers are returned **raw**; normalization belongs to the provider layer (§6.1). `ChangeNameServers` sends `ns1`..`nsN` for exactly `len(nameservers)` entries.

- [ ] **Step 4: Run** — PASS; full suite, gofmt, vet.

- [ ] **Step 5: Commit** — `git commit -m "feat: domain, privacy, and inventory client operations"`

---

### Task 5: DNSSEC operations

**Files:**
- Create: `internal/namesilo/dnssec.go`, `internal/namesilo/dnssec_test.go`

**Interfaces (produced, used by Tasks 8, 13):** `ListDSRecords(ctx, domain) ([]DSRecord, error)`, `AddDSRecord(ctx, domain, record DSRecord) error`, `DeleteDSRecord(ctx, domain, record DSRecord) error`.

- [ ] **Step 1: Write the failing tests** (§12.1 "DNSSEC request/response asymmetry" is the contract):
  1. `TestListDSRecords` — fixture with two `ds_record` elements (`digest`, `digest_type`, `algorithm`, `key_tag` — snake_case/full-word element names) parses to typed records.
  2. `TestListDSRecordsEmpty` — `<reply>` with only code/detail (zero `ds_record`) → empty, non-nil slice, no error (unsigned domain).
  3. `TestListDSRecordsDropsEmptyElement` — a bare `<ds_record/>` is dropped, not produced as a zero record (§8.4).
  4. `TestListDSRecordsMalformedInt` — `key_tag` of `abc` errors naming `key_tag` and the operation, and the error contains neither the digest nor the key.
  5. `TestAddDeleteDSRecordRequests` — request params are `domain`, `digest`, `keyTag`, `digestType`, and the **abbreviated `alg`** (not `algorithm`) for both `dnsSecAddRecord` and `dnsSecDeleteRecord`; uppercase-digest request sends the value unchanged (normalization is the provider's job at plan time).

- [ ] **Step 2: Run** — expected compile failure.
- [ ] **Step 3: Implement.** `parseDSInt(operation, field, value string) (int64, error)` via `strconv.ParseInt`, error naming field only (§8.4). Empty-element drop test: a record whose XML fields are all empty strings is skipped.
- [ ] **Step 4: Run** — PASS; full suite, gofmt, vet.
- [ ] **Step 5: Commit** — `git commit -m "feat: DNSSEC client operations"`

---

### Task 6: Contact operations

**Files:**
- Create: `internal/namesilo/contact.go`, `internal/namesilo/contact_test.go`

**Interfaces (produced, used by Tasks 11, 12, 13):**
- `type Contact struct` with §8.3's exact fields (ID string, DefaultProfile bool, Nickname, Company, FirstName, LastName, Address, Address2, City, State, Zip, Country, Email, Phone, Fax, UsNexusCategory, UsApplicationPurpose, CaLegalForm, CaLanguage, CaAgreementVersion, CaWhoisDisplay, EuCitizenshipCountry string) — empty string means unset; the provider layer maps empty↔null
- Methods: `ListContacts(ctx, contactID string) ([]Contact, error)`, `AddContact(ctx, contact Contact) (string, error)` (returns the new `contact_id`), `UpdateContact(ctx, contact Contact) error` (sends `contact_id` + every field), `DeleteContact(ctx, contactID string) error`, `AssociateContacts(ctx, domain string, roles ContactRoles) error` (sends **only the non-empty roles**, §8.3)

- [ ] **Step 1: Write the failing tests** (§12.1 "Contacts"):
  1. `TestContactAddUpdateDeleteRequests` — `contactAdd` sends every field including empty optionals (params always sent, §8.1) with the short API names (`fn`, `ln`, `ad`, `ad2`, `cy`, `st`, `zp`, `ct`, `em`, `ph`, `fx`, `cp`, `nn`, `usnc`, `usap`, `calf`, `caln`, `caag`, `cawd`, `eucs`); `contactUpdate` adds `contact_id`; `contactDelete` sends only `contact_id`; `contactAdd` returns the reply's `contact_id`.
  2. `TestContactList` — with a `contact_id` param returns that one profile; without (empty `contact_id` sent, per §8.1 — the live acceptance test in Task 14 is the contract check against the real API) returns every profile; two-profile, one-profile, and zero-profile fixtures.
  3. `TestContactParsing` — unset fields arrive as empty elements (`<address2/>`) and normalize to empty strings; a **single** `contact` element is a one-element result, not a scalar (§8.4); `default_profile` `1`/`0` parse to true/false and any other value errors.
  4. `TestAssociateContacts` — sends `domain` plus only the non-empty roles; the role params are `registrant`, `administrative`, `technical`, `billing`.

- [ ] **Step 2: Run** — expected compile failure.
- [ ] **Step 3: Implement** — envelope struct with `Contact []contactXML \`xml:"contact"\``; `contactXML` with element tags `contact_id`, `default_profile`, `nn`, `cp`, `fn`, `ln`, `ad`, `ad2`, `cy`, `st`, `zp`, `ct`, `em`, `ph`, `fx`, `usnc`, `usap`, `calf`, `caln`, `caag`, `cawd`, `eucs`; `default_profile` strict `1`/`0` parse with error.
- [ ] **Step 4: Run** — PASS; full suite, gofmt, vet.
- [ ] **Step 5: Commit** — `git commit -m "feat: contact client operations"`

---

### Task 7: Fake API server and `namesilo_nameservers` resource

**Files:**
- Create: `internal/provider/fakeserver_test.go`, `internal/provider/model.go`, `internal/provider/resource_nameservers.go`, `internal/provider/resource_nameservers_test.go`
- Modify: `internal/provider/provider.go` (register), `internal/provider/provider_test.go` (append `"namesilo_nameservers"` to `expectedResourceTypes`)

**Interfaces (produced, used by Tasks 8-13):**
- `model.go`: `addAPIError(diags *diag.Diagnostics, resourceType string, err error)` (summary `"NameSilo API error (namesilo_nameservers)"`; `*namesilo.APIError` renders as `"detail (code X)"` — the API-key-free error surface, §11); `setFromStrings(ctx, diags, values []string) types.Set` / `stringsFromSet(ctx, diags, set types.Set) []string`; `nullIfEmpty(string) types.String`
- `fakeserver_test.go`: `newFakeNamesilo() *fakeNamesilo` with `URL()`, `Close()`, `addDomain(name string) *fakeDomain` (seed fields directly), request log `ops() []string`, `count(op string) int`, `lastRequest(op string) string`; per-task mutation helpers. `fakeDomain{created, expires, status string; locked, private, autoRenew bool; nameservers []string; dsRecords []namesilo.DSRecord; roles namesilo.ContactRoles}`
- `resource_nameservers.go`: `NewNameserversResource()`, `nameserversResourceModel{Domain types.String; Nameservers types.Set; ID types.String}`, `defaultNameservers = []string{"ns1.dnsowl.com", "ns2.dnsowl.com", "ns3.dnsowl.com"}`
- Test helper: `namesiloProviderConfig(endpoint string) string` — provider block with `api_key = "test-key"` and `endpoint = <fake URL>`

- [ ] **Step 1: Write `fakeserver_test.go` skeleton.** `httptest.Server` with a mutex-protected in-memory store; handler switches on `r.URL.Path`; every request appends `"op|sorted params (key excluded)"` to the log; `failWith(op, code, detail)` makes an operation always reply with that code. Implement now: `getDomainInfo` (full reply per §8.3 shape, `Yes`/`No` booleans, `position` attrs; missing domain → code 200 detail `"Domain is not active, or does not belong to this user"`) and `changeNameServers` (sets the domain's nameservers from `ns1`…`nsN`; replies 300). Wrong `key` replies code 110.

- [ ] **Step 2: Write the failing harness tests** (`resource_nameservers_test.go`, §12.2 rows): every test does `requireTofu(t)`, builds its own fake, and uses `resource.Test` with `IsUnitTest: true` + `testAccProtoV6ProviderFactories`. Cases:
  1. **create** — fake domain on dnsowl defaults; config `nameservers = ["ns1.example.net","ns2.example.net"]`; state checks `id == "example.com"`, set equals config; `expectEmptyAfterRefresh()`; after `resource.Test` returns assert `fake.count("changeNameServers") == 1` and the logged params carry `ns1=ns1.example.net`, `ns2=ns2.example.net`.
  2. **read normalizes** — fake seeded `["NS1.EXAMPLE.NET.", "NS2.EXAMPLE.NET."]`; lowercase config plans and applies quiet; state lowercase (§4 invariant 1).
  3. **update** — two-step config change; assert one new `changeNameServers` per step and quiet plans.
  4. **drift** — step 2 uses `PreConfig` to call `fake.setNameservers(...)` out of band; `ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourcePlannedAction("namesilo_nameservers.test", plancheck.ResourceActionUpdate)}}`; converges and quiet.
  5. **destroy** — after the test's automatic destroy, assert a final `changeNameServers` whose params contain the three dnsowl defaults (§4 invariant 4).
  6. **import** — fake seeded matching config; step 1 `ImportState: true, ImportStateId: "example.com", ImportStatePersist: true, ImportStateVerify: false`; step 2 re-applies the same config with `expectEmptyAfterRefresh()`.

```go
func TestAccNameserversCreate(t *testing.T) {
	requireTofu(t)
	fake := newFakeNamesilo()
	defer fake.Close()
	fake.addDomain("example.com").nameservers = []string{"ns1.dnsowl.com", "ns2.dnsowl.com", "ns3.dnsowl.com"}

	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: namesiloProviderConfig(fake.URL()) + `
resource "namesilo_nameservers" "test" {
  domain      = "example.com"
  nameservers = ["ns1.example.net", "ns2.example.net"]
}
`,
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue("namesilo_nameservers.test", tfjsonpath.New("id"), knownvalue.StringExact("example.com")),
				statecheck.ExpectKnownValue("namesilo_nameservers.test", tfjsonpath.New("nameservers"),
					knownvalue.SetExact(stringSet("ns1.example.net", "ns2.example.net"))),
			},
			ConfigPlanChecks: expectEmptyAfterRefresh(),
		}},
	})

	if got := fake.count("changeNameServers"); got != 1 {
		t.Errorf("changeNameServers called %d times, want 1", got)
	}
	if last := fake.lastRequest("changeNameServers"); !strings.Contains(last, "ns1=ns1.example.net") {
		t.Errorf("changeNameServers not called with the configured set: %s", last)
	}
}
```

- [ ] **Step 3: Run** `go test ./internal/provider/ -run TestAccNameservers -v` — expected: fail with `provider did not register resource "namesilo_nameservers"`.

- [ ] **Step 4: Implement.** Schema per §6.1 (`domain` Required + `RequiresReplace`; `nameservers` `schema.SetAttribute{ElementType: types.StringType, Required: true, Validators: []validator.Set{setvalidator.SizeAtLeast(2), setvalidator.SizeAtMost(13)}}` — description states lowercasing and destroy-repoints-to-defaults with em dashes); `Create` normalizes via `namesilo.NormalizeNameservers`, calls `ChangeNameServers`, sets `plan.ID = plan.Domain`; `Read` per §6.1 (empty API list → error diagnostic, never a silent empty set); `Update` identical to Create; `Delete` calls `ChangeNameServers(domain, defaultNameservers)` and returns the diagnostic on error (state kept); `ImportState` passthrough on `path.Root("id")` plus `resp.State.SetAttribute(ctx, path.Root("domain"), req.ID)`; `ModifyPlan` normalizes when the set is wholly known (skip destroy plans and unknown values, §6.8). `Configure` implements the `*namesilo.Client` wrong-type guard naming the received type (§5). Register in `Resources`, append to `expectedResourceTypes`.

- [ ] **Step 5: Run** — harness tests PASS; `TestProviderSchema` PASS; full `go test ./...`, gofmt, vet.

- [ ] **Step 6: Commit** — `git commit -m "feat: namesilo_nameservers resource with hermetic harness tests"`

---

### Task 8: `namesilo_dnssec_records` resource

**Files:**
- Create: `internal/provider/resource_dnssec_records.go`, `internal/provider/resource_dnssec_records_test.go`
- Modify: `fakeserver_test.go` (`dnsSecListRecords`, `dnsSecAddRecord`, `dnsSecDeleteRecord` handlers), `provider.go`, `provider_test.go` (append type)

**Interfaces:** `NewDNSSecRecordsResource()`, `dnssecRecordsResourceModel{Domain types.String; Records types.Set; ID types.String}`, nested `dsRecordModel{KeyTag, Algorithm, DigestType types.Int64; Digest types.String}` with `tfsdk` tags; `model.go` gains `dsSetFromRecords` / `recordsFromDSSet`.

- [ ] **Step 1: Fake DS handlers** — list returns `ds_record` elements in the API's snake_case shape; add/delete mutate the fake's `dsRecords` and reply 300; `failWith` remains available.
- [ ] **Step 2: Write the failing harness tests** (§12.2): create-with-adopt (fake pre-seeded with one of the two configured records: only the missing one is added); update key roll (add runs **before** delete — assert via indexes in `fake.ops()`, §16 decision); drift both directions (out-of-band delete plans a re-add; out-of-band add plans a removal — `PreApply` planned-action checks); `records = []` deletes everything and stays quiet; empty-unsigned (fake domain with zero `ds_record` elements and `records = []` plans clean); destroy deletes every managed record; import (empty and non-empty cases).
- [ ] **Step 3: Run** — expected registration failure.
- [ ] **Step 4: Implement** per §6.2. Schema: `domain` + `RequiresReplace`; `records` as `schema.SetNestedAttribute` with the four nested attributes and validators (`int64validator.Between(0, 65535)` / `(0, 255)` × 2, `stringvalidator.RegexMatches(regexp.MustCompile(`^[0-9a-fA-F]+$`), "digest must be hexadecimal")`); description states the destroy-disables-DNSSEC and wrong-DS warnings. Create/Update run the reconcile (list → `DiffDSRecords` → adds → deletes — never re-adding or re-deleting an existing change, §4 invariant 3). Read normalizes each record via `namesilo.NormalizeDSRecord`. Delete lists and deletes everything present. `ModifyPlan` normalizes the nested set (lowercase digests) when wholly known. Register, append type.
- [ ] **Step 5: Run** — PASS; full suite, gofmt, vet.
- [ ] **Step 6: Commit** — `git commit -m "feat: namesilo_dnssec_records resource with reconciling harness tests"`

---

### Task 9: `namesilo_privacy` resource

**Files:**
- Create: `internal/provider/resource_privacy.go`, `internal/provider/resource_privacy_test.go`
- Modify: `fakeserver_test.go` (`addPrivacy`/`removePrivacy` handlers that model the real already-in-state codes 255/256), `provider.go`, `provider_test.go`

**Interfaces:** `NewPrivacyResource()`, `privacyResourceModel{Domain types.String; Enabled types.Bool; ID types.String}`.

- [ ] **Step 1: Fake privacy handlers** — `addPrivacy` on an already-private domain replies code 255, otherwise flips to private and replies 300; `removePrivacy` symmetric with 256 (this is what "idempotent against the fake's already-private reply" means, §12.2).
- [ ] **Step 2: Write the failing harness tests** (§12.2, with the working-tree refinements): create enabled → one `addPrivacy` call, and a create against a fake already-private domain (255 reply) still succeeds; create disabled → zero privacy calls in the log, state `enabled = false`; toggle both directions → exactly one call each, and a toggle landing on an already-in-state reply (seed fake so the flip hits 255/256) is accepted; drift (flip `private` out of band → planned update in the opposite direction); destroy → `removePrivacy` exactly once when enabled, zero privacy calls when the resource declared `enabled = false` (§4 invariants 6, 7); import by domain.
- [ ] **Step 3: Run** — expected registration failure.
- [ ] **Step 4: Implement** per §6.3. Create: `AddPrivacy` only when `enabled` is true; Update: call only when plan and state disagree (the client classifies 255/256 as success for their own operation — no special-casing in the resource); Read parses `DomainInfo.Private`; Delete: `RemovePrivacy` only when state says enabled. Description states TLD availability/billing and WHOIS-hides-registrant facts. No ModifyPlan (§6.8). Register, append type.
- [ ] **Step 5: Run** — PASS; full suite, gofmt, vet.
- [ ] **Step 6: Commit** — `git commit -m "feat: namesilo_privacy resource"`

---

### Task 10: `namesilo_domain_lock` and `namesilo_auto_renew` resources

**Files:**
- Create: `internal/provider/resource_domain_lock.go`, `resource_auto_renew.go`, `resource_domain_lock_test.go`, `resource_auto_renew_test.go`
- Modify: `fakeserver_test.go` (`domainLock`/`domainUnlock` with 252/253; `addAutoRenewal`/`removeAutoRenewal` with 250/251), `provider.go`, `provider_test.go`

**Interfaces:** `NewDomainLockResource()`, `NewAutoRenewResource()`; models `domainLockResourceModel{Domain types.String; Locked types.Bool; ID types.String}`, `autoRenewResourceModel{Domain types.String; Enabled types.Bool; ID types.String}`. Both are the Task 9 pattern applied to their own operation pair and codes (§6.6, §6.7, §4 invariants 12, 14, 15).

- [ ] **Step 1: Fake handlers** for the four toggles, mirroring 252/253 and 250/251 already-in-state replies.
- [ ] **Step 2: Write the failing harness tests** — per §12.2 rows: lock create (both `locked` values, each idempotent against its already-in-state reply), lock drift, lock destroy (unlock only when locked; no call when unlocked); auto-renew create (both `enabled` values, idempotent), auto-renew drift, auto-renew destroy (disable only when enabled; no call when disabled). Imports for both.
- [ ] **Step 3: Run** — expected registration failures.
- [ ] **Step 4: Implement** both resources per §6.6/§6.7 (call only on plan/state disagreement; Read from `DomainInfo.Locked`/`.AutoRenew`; Delete conditional). Descriptions: lock — unlock removes the transfer safeguard, some registry states reject both operations; auto-renew — destroy stops renewal and the domain can expire (the most consequential destroy in the provider, §16). Register, append types.
- [ ] **Step 5: Run** — PASS; full suite, gofmt, vet.
- [ ] **Step 6: Commit** — `git commit -m "feat: namesilo_domain_lock and namesilo_auto_renew resources"`

---

### Task 11: `namesilo_contact` resource

**Files:**
- Create: `internal/provider/resource_contact.go`, `internal/provider/resource_contact_test.go`
- Modify: `fakeserver_test.go` (`contactAdd`/`contactUpdate`/`contactDelete`/`contactList` handlers over an in-memory contact map with `contact_id` assignment, `default_profile`, and empty-element output shape), `provider.go`, `provider_test.go`

**Interfaces:** `NewContactResource()`, `contactResourceModel` with §6.4's 21 attributes as `types.String`/`types.Bool` (`default_profile` Computed) + `ID`; `model.go` gains `contactToModel`/`modelToContact` (empty string ↔ null mapping via `nullIfEmpty`).

- [ ] **Step 1: Fake contact handlers** — `contactAdd` assigns an ID like `1001` and echoes fields; `contactList` with `contact_id` returns that profile or none, without it returns all; unset optional fields are emitted as empty elements; `contactDelete` removes (and a `failWith`-style flag can model "still associated with a domain").
- [ ] **Step 2: Write the failing tests.** Unit-level (no tofu): `TestContactSchemaMasksPII` — instantiate the resource, call `Schema`, assert every §6.4 PII attribute has `Sensitive: true` and `id`/`default_profile` do not (§12.1 — the GDPR mapping cannot drift silently). Harness tests (§12.2): create (every configured field recorded by the fake; returned `contact_id` becomes `id`; quiet plan incl. empty-element optionals — §4 invariant 8); update (one `contactUpdate`, quiet after); empty optional round-trip; drift (edit fake profile out of band → update); gone (fake profile removed between steps → resource removed from state and next plan recreates; the removal path emits an `AddWarning` and `resp.State.RemoveResource`); destroy (`contactDelete` called); import by `contact_id`.
- [ ] **Step 3: Run** — expected failures.
- [ ] **Step 4: Implement** per §6.4. Schema with the exact validators (`stringvalidator.LengthAtLeast(1)` on required strings, `LengthAtMost` on optionals, `LengthBetween(2, 2)` + description "ISO 3166-1 alpha-2" on `country`); all PII `Sensitive`. `ModifyPlan`: known `""` planned optionals → null; `country` uppercased. Create: `AddContact`, `id` = returned `contact_id`, `default_profile` written as `false` (the following refresh corrects it; no second API call — §6.4). Update: `UpdateContact` with `id` from state, `default_profile` carried from state. Read: `ListContacts(id)`; zero profiles → `RemoveResource` + warning (the §11 exception); exactly one → write with empty→null. Delete: `DeleteContact`, error surfaced unchanged. Register, append type.
- [ ] **Step 5: Run** — PASS; full suite, gofmt, vet.
- [ ] **Step 6: Commit** — `git commit -m "feat: namesilo_contact resource"`

---

### Task 12: `namesilo_domain_contacts` resource

**Files:**
- Create: `internal/provider/resource_domain_contacts.go`, `internal/provider/resource_domain_contacts_test.go`
- Modify: `fakeserver_test.go` (`contactDomainAssociate` handler), `provider.go`, `provider_test.go`

**Interfaces:** `NewDomainContactsResource()`, `domainContactsResourceModel{Domain types.String; Registrant, Administrative, Technical, Billing types.String (Optional+Computed); ID types.String}`.

- [ ] **Step 1: Fake associate handler** — sets the roles present in the query; `getDomainInfo` already reports `contact_ids`.
- [ ] **Step 2: Write the failing harness tests** (§12.2): partial (only `registrant` configured → exactly one associate call containing only that role; omitted roles stored as computed from the API; quiet plan — §4 invariant 9); update (change one role → one call with that role; others untouched); drift (reassign a role in the fake → update for that role only); destroy (no API call, fake associations intact — §4 invariant 10); import by domain (all four roles filled by Read).
- [ ] **Step 3: Run** — expected registration failure.
- [ ] **Step 4: Implement** per §6.5. Create/Update: roles to send are the ones **non-null in `req.Config`** (omitted roles are null there and unknown in `req.Plan`, because the attributes are Optional+Computed); values come from the plan; one `AssociateContacts` call sends them (the client sends only non-empty roles). Read: `GetDomainInfo` → `contact_ids` → all four roles. Delete: a no-op (description says destroy stops managing the association and leaves it intact). `domain` + `RequiresReplace`; `id` = domain. Description mentions the registrant-change verification email / TLD restrictions, surfaced as API errors. Register, append type.
- [ ] **Step 5: Run** — PASS; full suite, gofmt, vet.
- [ ] **Step 6: Commit** — `git commit -m "feat: namesilo_domain_contacts resource"`

---

### Task 13: Six data sources

**Files:**
- Create: `internal/provider/data_source_nameservers.go`, `data_source_dnssec_records.go`, `data_source_privacy.go`, `data_source_contacts.go`, `data_source_domain.go`, `data_source_domains.go`, plus one `data_source_*_test.go` per data source (contacts/domains can share a file)
- Modify: `fakeserver_test.go` (`listDomains` handler with page/pageSize honoring and an `ignorePaging` knob), `provider.go` (register all six), `provider_test.go` (append all six type names)

**Interfaces:** `NewNameserversDataSource()` etc.; models per §7 (domain-scoped: `id` = domain; account-scoped: fixed `id` values `"contacts"`/`"domains"`).

- [ ] **Step 1: Fake `listDomains`** — pages the fake's domains honoring `page`/`pageSize`, echoes `pager` with `total`; `ignorePaging = true` always returns page 1 (drives the guard test).
- [ ] **Step 2: Write the failing harness tests** (§12.2 "data sources" rows + §4 invariant 13): nameservers (lowercased, API position order — list(string), not set); dnssec_records (normalized four-field objects); privacy (bool); contacts (every profile, including one with empty optional fields — nested PII `Sensitive` walk asserted in a schema unit test like Task 11's); domain (every `getDomainInfo` field: `Yes`/`No` → bools, dates as strings, `forward_url`/`forward_type` exactly as sent including `N/A`, lowercased nameservers, `contact_ids` object — §7); domains (multi-page fake combined, `total` matches; `ignorePaging` fake: no loop, first-page values, warning diagnostic is emitted by the data source when `DomainList.Truncated` is set — assert values and termination, the client unit test from Task 4 already pinned `Truncated`).
- [ ] **Step 3: Run** — expected registration failures (all six).
- [ ] **Step 4: Implement** each data source per §7: `Metadata` (`req.ProviderTypeName` + suffix), schema, `Read` via `DataSourceData` guard, normalized writes. Missing domain → the API error (never an empty result). Register all six, append all six type names.
- [ ] **Step 5: Run** — PASS; full suite, gofmt, vet.
- [ ] **Step 6: Commit** — `git commit -m "feat: data sources for registrar state reads"`

---

### Task 14: Live acceptance tests

**Files:**
- Create: `internal/provider/acceptance_live_test.go`
- Modify: `GNUmakefile` only if drift is found (already has `testacc-live` from Task 1)

**Interfaces:** `testAccLivePreCheck(t)` (TF_ACC skip → `NAMESILO_API_KEY`/`NAMESILO_TEST_DOMAIN` skip with actionable messages → `TF_ACC_TERRAFORM_PATH`/`TF_ACC_PROVIDER_HOST` fatals naming `make testacc-live`); `liveOptIn(t, env, consequence)` — skips unless `env == "1"`, message names the mutation (§12.3).

- [ ] **Step 1: Write the tests**, all `TestAccLive*`, none `IsUnitTest`, all with `PreCheck` that calls `testAccLivePreCheck(t)`; provider block empty (Configure pulls `NAMESILO_API_KEY` from env). Cases from §12.3 with their opt-ins:
  - `TestAccLiveDataSources` — read all six (no extra opt-in; uses `NAMESILO_TEST_DOMAIN`).
  - `TestAccLiveNameservers` — CRUD + quiet plans + destroy returns to dnsowl defaults.
  - `TestAccLiveNameserversImport` and one import test per remaining resource (import every resource).
  - `TestAccLiveDNSSEC` (`NAMESILO_TEST_DNSSEC=1`) — add, roll, remove.
  - `TestAccLivePrivacy` (`NAMESILO_TEST_PRIVACY=1`) — enable, disable, re-enable.
  - `TestAccLiveContact` (`NAMESILO_TEST_CONTACT=1`) — create, update, delete a throwaway profile.
  - `TestAccLiveDomainContacts` (`NAMESILO_TEST_DOMAIN_CONTACTS=1`) — reassign one role, assert the other roles untouched.
  - `TestAccLiveDomainLock` (`NAMESILO_TEST_LOCK=1`) — lock and unlock.
  - `TestAccLiveAutoRenew` (no extra opt-in, §12.3) — enable and disable.
  - `TestAccLiveDomains` — list contains `NAMESILO_TEST_DOMAIN`; `total` ≥ returned rows.
- [ ] **Step 2: Run** `go test ./internal/provider/ -run TestAccLive -v` with no credentials — expected: every test **skips** with actionable messages; with `TF_ACC` unset they also skip. Confirm zero failures.
- [ ] **Step 3: Commit** — `git commit -m "test: live acceptance tests behind environment opt-ins"`

---

### Task 15: Documentation and examples

**Files:**
- Create: `examples/provider/provider.tf` (api_key from a variable, endpoint commented), `examples/resources/…/{resource.tf,import.sh}` for all seven, `examples/data-sources/…/data-source.tf` for all six, `templates/index.md.tmpl`, `tools/go.mod`, `tools/tools.go`, `tools/gen-schema.sh`, generated `docs/index.md` + `docs/resources/*.md` + `docs/data-sources/*.md`
- Modify: `README.md` (full version: example, requirements, the auto-renew destroy warning, state-contains-PII note, license)

**Interfaces:** none (build artifacts). `docs/superpowers/` is hand-written and must never be touched by generation (§13).

- [ ] **Step 1: Write examples** — one `resource.tf` + `import.sh` per resource (import.sh shows the exact import ID form; domain-keyed resources use `tofu import namesilo_nameservers.test example.com`, contact uses the `contact_id`), one `data-source.tf` per data source. Copy `$ACC/examples` conventions.
- [ ] **Step 2: Write `templates/index.md.tmpl`** — modeled on `$ACC/templates/index.md.tmpl` but **with** the provider-config schema section, because this provider has a configuration schema (the accumulator's comment explains when to re-add it). Content: what the provider manages, requirements (OpenTofu ≥ 1.10, Go ≥ 1.25.12), provider config with env fallbacks, the destroy-semantics warnings (nameservers → dnsowl defaults; DNSSEC disabled; lock released; auto-renew stopped — the domain can expire), PII-in-state note, GPL license.
- [ ] **Step 3: Create the tools module** — copy `$ACC/tools/{go.mod,tools.go,go.sum}`; `tools.go`'s `go:generate` line becomes `… --provider-name namesilo --providers-schema schema.json`. `tools/gen-schema.sh`: copy `$ACC/tools/gen-schema.sh` and replace `accumulator` → `namesilo` everywhere (`terraform-provider-accumulator` build target, `dev_overrides` `"hashicorp/namesilo"`, `required_providers` block, both `sed`/`grep` guards and their messages).
- [ ] **Step 4: Generate** — `make docs` (local tofu is 1.12.1; CI's generate job pins 1.12.4. If the Task 16 generate job reports drift, install tofu 1.12.4 locally and re-run — the pin exists because schema export output shifts across CLI feature thresholds).
- [ ] **Step 5: Verify** — `docs/` contains generated files for exactly the seven resources and six data sources with stripped names (`docs/resources/nameservers.md` etc.); `git status` shows `docs/superpowers/` untouched; `tools/schema.json` is gitignored.
- [ ] **Step 6: Commit** — `git commit -m "docs: examples, templates, and generated documentation"`

---

### Task 16: CI, release pipeline, final sweep

**Files:**
- Create: `.github/workflows/test.yml`, `.github/workflows/release.yml`, `.goreleaser.yml`, `terraform-registry-manifest.json`, `.github/dependabot.yml`

- [ ] **Step 1: Write `test.yml`** — copy `$ACC/.github/workflows/test.yml` and adapt per §14: `paths-ignore` drops `README.md` and `docs/superpowers/**` (no `SPEC.md` here); jobs: **build** (unchanged), **unit** (+ `opentofu/setup-opentofu` pinned 1.12.4 so the harness tests run; keep `go test -v -cover ./internal/...`), **generate** (unchanged: tofu 1.12.4, `make docs`, fail on dirty tree), **acceptance** (needs build; matrix `1.10.*`/`1.12.*`; same `TF_ACC`/`TF_ACC_TERRAFORM_PATH`/`TF_ACC_PROVIDER_HOST` env plumbing), plus the optional **live** job: `env: NAMESILO_API_KEY: ${{ secrets.NAMESILO_API_KEY }}`, `NAMESILO_TEST_DOMAIN: ${{ secrets.NAMESILO_TEST_DOMAIN }}`, steps guarded with `if: env.NAMESILO_API_KEY != ''` running `make testacc-live` (secrets cannot gate a job-level `if`, §14); **license** (same go-licenses invocation, `--ignore=github.com/nijave/terraform-provider-namesilo`).
- [ ] **Step 2: Copy release assets** — `.goreleaser.yml`, `terraform-registry-manifest.json`, `.github/workflows/release.yml`, `.github/dependabot.yml` from `$ACC` unchanged (goreleaser derives the project name from the repo).
- [ ] **Step 3: Full local verification** — `make test` (with tofu: everything runs), `gofmt -l .` empty, `go vet ./...`, `make docs` twice → second run leaves a clean tree. Confirm `git status` after a docs run shows only intended changes.
- [ ] **Step 4: Commit** — `git commit -m "build: CI, release pipeline, and dependency updates"`

---

## Deviations from the spec, deliberate and minimal

1. `DomainList.Truncated` — the spec's §7 warning needs the client to report that the duplicate-page guard fired.
2. `Client.PageSize` exported field — `NewClient`'s §8.3 signature is kept while `page_size` still reaches the one data source that consumes it.
3. `contactList` sends an empty `contact_id` for "all profiles" per §8.1's always-send rule — the live test validates this against the real API.

All three are noted where they land and are reviewer-visible in the diffs that introduce them.

## Self-Review

**Spec coverage:** §3 layout → every task's file list matches it; §4 invariants 1-15 → Tasks 7 (1, 4), 8 (2, 3, 5), 9 (6, 7), 10 (12, 14, 15), 11 (8, 11), 12 (9, 10), 13 (13); §5 → Task 1; §6.1-6.7 → Tasks 7-12; §6.8 → Tasks 7, 8, 11; §7 → Task 13; §8.1-8.4 → Tasks 2, 4, 5, 6; §9 → Task 3; §10 → Tasks 7-12 (per-resource import); §11 → Task 2 + resource tasks (via `addAPIError`, contact exception in Task 11); §12.1 → Tasks 1-6, 11, 13; §12.2 → Tasks 7-13; §12.3 → Task 14; §13 → Task 15; §14 → Tasks 1, 16; §15 → Tasks 1, 16. §16/§17 behaviors are asserted by the tests that reference them.

**Type consistency:** `NewClient(endpoint, apiKey, version)`, `Client.PageSize`, `call(ctx, op, params, out)`, `APIError{Operation, Code, Detail}`, `DomainInfo`/`DomainList{…, Truncated}`, `DSRecord`+`Key()`+`DiffDSRecords`, `Contact`/`ContactRoles`, all resource/data source constructors and models, fake server helpers (`addDomain`, `ops`, `count`, `lastRequest`, `failWith`, `ignorePaging`), and test helpers (`requireTofu`, `testAccProtoV6ProviderFactories`, `expectedResourceTypes`/`expectedDataSourceTypes`, `namesiloProviderConfig`, `expectEmptyAfterRefresh`, `stringSet`, `testAccPreCheck`, `testAccLivePreCheck`, `liveOptIn`) are used with identical names across tasks.