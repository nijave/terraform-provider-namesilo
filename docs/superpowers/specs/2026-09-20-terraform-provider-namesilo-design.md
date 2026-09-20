# terraform-provider-namesilo — design

Date: 2026-09-20
Status: proposed (awaiting review)

## 1. Motivation

Some DNS settings live at the registrar, not in a zone file: the nameservers a
domain is delegated to, and the DS records that tell validating resolvers which
keys sign the zone. They are the two halves of a self-hosted DNS setup. An
operator who runs their own authoritative servers (BIND, PowerDNS, Knot, a
Kubernetes operator) needs to point the domain at those servers and publish a DS
record for each signing key, and neither belongs in the zone itself.

The NameSilo web UI can do both, and the NameSilo API exposes both, but the API
has no Terraform provider. The same gap exists for WHOIS privacy: whether
PrivacyGuardian is on is registrar state, and the API can toggle it with
`addPrivacy` and `removePrivacy`, so the provider manages that too. Contact
profiles are the third piece of registrar state: ICANN requires a registrant,
administrative, technical, and billing contact for every domain, the API manages
profiles with `contactAdd`/`contactList`/`contactUpdate`/`contactDelete` and
associates them with `contactDomainAssociate`, and there is no way to keep that
in version control without a provider. An existing community Go client
(`github.com/nrdcg/namesilo`) covers the operations but is generated from 2019
documentation and returns raw responses in errors; this provider owns its small
client instead, for control over diagnostics and API-key redaction.

```hcl
resource "namesilo_nameservers" "example" {
  domain      = "example.com"
  nameservers = ["ns1.example.net", "ns2.example.net"]
}

resource "namesilo_dnssec_records" "example" {
  domain = "example.com"

  records = [{
    key_tag     = 12345
    algorithm   = 13
    digest_type = 2
    digest      = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
  }]
}

resource "namesilo_contact" "registrant" {
  first_name = "Ada"
  last_name  = "Lovelace"
  address    = "1 Analytical Engine Way"
  city       = "London"
  state      = "Greater London"
  zip        = "EC1A 1BB"
  country    = "GB"
  email      = "ada@example.net"
  phone      = "+44.2079460123"
}

resource "namesilo_domain_contacts" "example" {
  domain    = "example.com"
  registrant = namesilo_contact.registrant.id
}

resource "namesilo_privacy" "example" {
  domain  = "example.com"
  enabled = true
}
```

## 2. Scope

**In scope:**

- The provider binary, its configuration, and an `internal/namesilo` HTTP client
  for the operations needed.
- Five resources: `namesilo_nameservers`, `namesilo_dnssec_records`,
  `namesilo_privacy`, `namesilo_contact`, and `namesilo_domain_contacts`.
- Four data sources: `namesilo_nameservers`, `namesilo_dnssec_records`,
  `namesilo_privacy`, and `namesilo_contacts`.
- Import for every resource.
- Unit tests, hermetic harness tests against a fake API server, and opt-in live
  acceptance tests.
- Generated documentation, build, CI, and release pipeline.

**Out of scope:**

- Zone-level DNS records (A, AAAA, CNAME, MX, TXT, and NS records inside
  NameSilo's managed zone). Only registrar-level settings are managed. The
  client is structured so an operation can be added later without rework.
- Domain registration, renewal, transfer, and locking.
- NameSilo's "registered nameservers" feature (`addRegisteredNameServer` and
  friends), which manages glue/child nameservers rather than delegation.
- DNSSEC key management. The provider publishes DS records; it does not generate
  or rotate keys. Key material stays with whatever signs the zone.

**Explicit non-goals:**

- Making the API transactional. `records` reconciles with individual add and
  delete calls, so a partial failure leaves a partial result; the next apply
  converges it.
- Automatic retries. NameSilo's code 400 ("existing API request is still
  processing") is surfaced as an error the operator re-runs. See §17.

## 3. Stack and repository layout

Go with `terraform-plugin-framework` (protocol 6). SDKv2 is not considered: it
is in maintenance mode, and both sibling providers
(`terraform-provider-accumulator`, `terraform-provider-pki`) use the framework.

**OpenTofu is the primary target.** The support floor is OpenTofu ≥ 1.10, the
oldest release line still receiving security support per
<https://endoflife.date/opentofu> (as of 2026-09-20: 1.10 and 1.12). The code
uses no OpenTofu-only or Terraform-only features, so Terraform ≥ 1.10 works too,
but Terraform is not tested and is not the reference platform: CI drives `tofu`
exclusively, which keeps BUSL-licensed binaries out of the build.

Provider address: `registry.opentofu.org/nijave/namesilo`; short name in
configuration and in the test factories: `namesilo`. Module path:
`github.com/nijave/terraform-provider-namesilo`. Go directive `go 1.25.12`
(matching the siblings, and above the `go 1.25.8` that terraform-plugin-testing
v1.16.0 requires).

There is no OpenTofu fork of `terraform-plugin-framework`,
`terraform-plugin-go`, `terraform-plugin-testing`, or `terraform-plugin-docs`,
and none is needed. OpenTofu implements the same protocol and reads the same
`docs/` layout. Those remain MPL-2.0 libraries; see §15.

```
terraform-provider-namesilo/
  main.go                              # providerserver.Serve, ldflags-injected version
  internal/
    namesilo/                          # pure Go, zero Terraform imports
      client.go                        # Client, request building, XML envelope
      errors.go                        # APIError, reply-code classification
      domain.go                        # GetDomainInfo, ChangeNameServers
      dnssec.go                        # ListDSRecords, AddDSRecord, DeleteDSRecord
      privacy.go                       # AddPrivacy, RemovePrivacy
      contact.go                       # ListContacts, AddContact, UpdateContact, DeleteContact, AssociateContacts
      normalize.go                     # NormalizeNameservers, SameNameservers, DiffDSRecords
      boundary_test.go                 # fails `go test` if a terraform-plugin-* import appears
      *_test.go
    provider/
      provider.go                      # provider.New, Metadata, Schema, Configure, registration
      client.go                        # Configure wiring: resolves config, builds *namesilo.Client
      model.go                         # types.Set/types.List <-> client types
      resource_nameservers.go          # namesilo_nameservers
      resource_dnssec_records.go       # namesilo_dnssec_records
      resource_privacy.go              # namesilo_privacy
      resource_contact.go              # namesilo_contact
      resource_domain_contacts.go      # namesilo_domain_contacts
      data_source_nameservers.go       # namesilo_nameservers
      data_source_dnssec_records.go    # namesilo_dnssec_records
      data_source_privacy.go           # namesilo_privacy
      data_source_contacts.go          # namesilo_contacts
      provider_test.go                 # in-process protocol 6 factories, TestMain, shared guards
      fakeserver_test.go               # in-memory NameSilo API for harness tests
      *_test.go
  docs/
    index.md                           # generated
    resources/nameservers.md           # generated; tfplugindocs strips the provider prefix
    resources/dnssec_records.md        # generated
    resources/privacy.md               # generated
    resources/contact.md               # generated
    resources/domain_contacts.md       # generated
    data-sources/nameservers.md        # generated
    data-sources/dnssec_records.md     # generated
    data-sources/privacy.md            # generated
    data-sources/contacts.md           # generated
    superpowers/specs/                 # this document (hand-written, never clobbered)
  examples/
    provider/provider.tf
    resources/namesilo_nameservers/{resource.tf,import.sh}
    resources/namesilo_dnssec_records/{resource.tf,import.sh}
    resources/namesilo_privacy/{resource.tf,import.sh}
    resources/namesilo_contact/{resource.tf,import.sh}
    resources/namesilo_domain_contacts/{resource.tf,import.sh}
    data-sources/namesilo_nameservers/data-source.tf
    data-sources/namesilo_dnssec_records/data-source.tf
    data-sources/namesilo_privacy/data-source.tf
    data-sources/namesilo_contacts/data-source.tf
  templates/index.md.tmpl              # source for docs/index.md
  tools/                               # separate Go module: tfplugindocs only
  GNUmakefile
  .goreleaser.yml
  terraform-registry-manifest.json     # {"version": 1, "metadata": {"protocol_versions": ["6.0"]}}
  .github/workflows/{test.yml,release.yml}
  .github/dependabot.yml
  .gitignore
  LICENSE                              # GPL-3.0
  README.md
```

`internal/namesilo` holds the HTTP client and every decision that does not need
the framework, so those are unit-testable without `TF_ACC`. `internal/provider`
is a thin translation between `types.*` values and those functions.

## 4. Concepts and invariants

- **Registrar delegation** is the set of nameservers in the domain's NS records
  at the registry. NameSilo replaces the whole set with one `changeNameServers`
  call, so the resource is authoritative: it manages the complete list.
- **A DS record** is a tuple `(key_tag, algorithm, digest_type, digest)`. It is
  the API's own key for add and delete. Records are case-insensitive: digests
  are hex and nameservers are DNS names, so both are normalized to lowercase.
- **WHOIS privacy** is a boolean on the domain. The API reports it as
  `getDomainInfo`'s `private` field and toggles it with `addPrivacy` and
  `removePrivacy`. Both operations are idempotent from the provider's point of
  view: the reply codes for "already private" and "already not private" are
  treated as success.
- **One resource per domain.** `domain` forces replacement; changing it points
  the resource at a different domain rather than renaming anything.
- **`id` is the domain.** It is inert (Terraform keys state by resource
  address), it makes `tofu state show` readable, and it is the import ID. The
  one exception is `namesilo_contact`, whose `id` is the API's `contact_id`.
- **A contact profile** is an account-level record identified by `contact_id`. A
  domain references profiles by ID in four roles: registrant, administrative,
  technical, and billing. Contact profiles are not owned by a domain, so the
  profile resource and the association resource are separate.
- **`default_profile`** is an account-level flag on one profile (`1` or `0` in
  the API); it cannot be set or deleted through these operations, so it is
  computed and informational.

Invariants that harness and acceptance tests assert:

1. A second plan after a successful apply is empty (no attribute recomputes on
   refresh), including after a nameserver list that the API returns in a
   different case or order.
2. `records = []` is a valid configuration meaning "no DS records"; it is not
   the same as omitting `records` (which is not allowed; the attribute is
   required).
3. Create, Read, Update, and Delete never fail because of an idempotent
   operation: adding a record that already exists or deleting one that is
   already gone is reconciled before the call, not attempted.
4. Destroying `namesilo_nameservers` leaves the domain on NameSilo's default
   nameservers (`ns1.dnsowl.com`, `ns2.dnsowl.com`, `ns3.dnsowl.com`).
5. Destroying `namesilo_dnssec_records` removes every managed DS record, which
   disables DNSSEC for the domain.
6. `enabled = false` is a valid privacy configuration that issues no API call
   when the domain is already not private, and `enabled = true` issues no call
   when it is already private.
7. Destroying `namesilo_privacy` calls `removePrivacy` only when privacy was
   enabled; destroying a resource that declared `enabled = false` makes no API
   call.
8. A contact profile round-trips: Create writes `contact_id` to `id`, Read
   returns the same field values, and a second plan is empty, including for
   fields the API returns as empty elements.
9. `namesilo_domain_contacts` updates only the roles present in configuration;
   a role that is omitted is reported by the API and stored as computed without
   appearing as drift.
10. Destroying `namesilo_domain_contacts` leaves the domain's associations in
    place: the API has no disassociate operation, so there is nothing to undo.
11. Destroying `namesilo_contact` deletes the profile, and a profile still
    associated with a domain fails with the API's error rather than silently
    remaining.

## 5. Provider configuration

| Attribute | Type | Required/Optional/Computed | Notes |
| --- | --- | --- | --- |
| `api_key` | `string` | Optional, Sensitive | Falls back to `NAMESILO_API_KEY`. Configure fails if the resolved value is empty. |
| `endpoint` | `string` | Optional | Falls back to `NAMESILO_API_ENDPOINT`, then `https://www.namesilo.com/api`. Trailing slash is trimmed. Sandbox and OTE endpoints are accepted from NameSilo support. |

`Configure` resolves both, constructs one `*namesilo.Client`, and passes it to
resources and data sources through `resp.ResourceData` / `resp.DataSourceData`.
A consumer that finds the wrong type adds an error diagnostic naming the type it
got, the same guard the framework documentation recommends.

`Metadata.TypeName` is `namesilo`. Resource and data source type names are
built from `req.ProviderTypeName` plus a constant suffix.

The client carries the provider version for its `User-Agent`
(`terraform-provider-namesilo/<version>`); `provider.New(version)` already
threads it from `main.go`.

There is no `timeout` attribute in v1. The client's HTTP timeout is 30 seconds,
which covers every operation this provider calls.

## 6. Resources

### 6.1 `namesilo_nameservers`

| Attribute | Type | Required/Optional/Computed | Modifiers and validators |
| --- | --- | --- | --- |
| `domain` | `string` | Required | `stringplanmodifier.RequiresReplace()` |
| `nameservers` | `set(string)` | Required | `setvalidator.SizeAtLeast(2)`, `setvalidator.SizeAtMost(13)` |
| `id` | `string` | Computed | `stringplanmodifier.UseStateForUnknown()` |

A set, not a list: order has no meaning for delegation, the API assigns
positions itself, and a list would diff on every refresh because NameSilo
returns nameservers in an arbitrary order.

Markdown description must state the two behaviours that surprise operators:

- Values are stored lowercased. A configuration that writes them in another
  case is normalized at plan time, so it does not diff.
- Destroy does not remove delegation; it sets NameSilo's default nameservers.
  Removing the resource from configuration repoints the domain's DNS, which is
  an outage if the zone is hosted elsewhere.

**Create/Update:** `ChangeNameServers(domain, nameservers)`. Create and Update
are the same operation; the API has no "add one nameserver" call.

**Read:** `GetDomainInfo(domain)`, take the nameservers, normalize
(lowercase, trim space, strip one trailing dot), write the set. An empty list
from the API is a state that cannot be represented (`SizeAtLeast(2)` rejects
it), so it is an error diagnostic rather than a silent empty set.

**Delete:** `ChangeNameServers(domain, [ns1.dnsowl.com, ns2.dnsowl.com,
ns3.dnsowl.com])`. On API error, the diagnostic is returned and state is kept.

**ModifyPlan:** when the planned `nameservers` is known, normalize it so the
plan matches the normalized state. Unknown planning (elements unknown, or a
destroy plan) is left alone.

### 6.2 `namesilo_dnssec_records`

| Attribute | Type | Required/Optional/Computed | Modifiers and validators |
| --- | --- | --- | --- |
| `domain` | `string` | Required | `stringplanmodifier.RequiresReplace()` |
| `records` | `set(object)` | Required | none at the set level |
| `records.key_tag` | `int64` | Required | `int64validator.Between(0, 65535)` |
| `records.algorithm` | `int64` | Required | `int64validator.Between(0, 255)` |
| `records.digest_type` | `int64` | Required | `int64validator.Between(0, 255)` |
| `records.digest` | `string` | Required | `stringvalidator.RegexMatches` for hex (`^[0-9a-fA-F]+$`) |
| `id` | `string` | Computed | `stringplanmodifier.UseStateForUnknown()` |

Ranges are deliberately wider than the currently assigned IANA values: a
validator that rejects algorithm 17 the day IANA assigns it would break working
configurations. The IANA registries are linked from the attribute descriptions.

An empty set is the way to declare "this domain has no DS records", and it is
also what an import of an unsigned domain produces. `records` is a set so two
identical tuples in one configuration collapse instead of causing a confusing
API error.

Markdown description must state that destroying the resource deletes the DS
records and disables DNSSEC for the domain, and that publishing a DS that does
not match the zone's keys makes the domain fail validation.

**Create:** reconcile (see below). Create still lists first, because the domain
may already carry DS records from before the resource existed.

**Update:** reconcile.

**Reconcile:**

```
current = ListDSRecords(domain)
toAdd, toRemove = DiffDSRecords(current, plan.records)
for r in toAdd:    AddDSRecord(domain, r)
for r in toRemove: DeleteDSRecord(domain, r)
```

Adds run before deletes so a key roll never leaves a window with neither the
old nor the new DS published. `DiffDSRecords` compares canonical tuples, so a
record differing only in digest case or integer formatting is not re-added.

**Read:** `ListDSRecords(domain)`, normalize each record, write the set.

**Delete:** list, then delete every record that is present. A record already
removed outside Terraform is not deleted again; an empty list makes Delete a
no-op.

**ModifyPlan:** when the planned `records` set is fully known, normalize it.

### 6.3 `namesilo_privacy`

| Attribute | Type | Required/Optional/Computed | Modifiers and validators |
| --- | --- | --- | --- |
| `domain` | `string` | Required | `stringplanmodifier.RequiresReplace()` |
| `enabled` | `bool` | Required | none |
| `id` | `string` | Computed | `stringplanmodifier.UseStateForUnknown()` |

Resource existence does not imply privacy is on: `enabled` is the desired state,
so `enabled = false` is how a domain is kept unprivate without deleting the
resource. Drift detection is the normal refresh. If privacy is turned off in the
web UI while `enabled = true`, Read writes `false` and the next plan turns it
back on; if it is turned on while `enabled = false`, the next plan turns it off.

**Create:** when `enabled` is true, `AddPrivacy(domain)`; when false, no API
call and the state records `false`.

**Update:** `AddPrivacy` or `RemovePrivacy` for the direction of the change.
Both are called only when the plan and state disagree, and both tolerate the
API's "already private" (255) and "already not private" (256) replies, so a race
with the web UI cannot produce a spurious failure.

**Read:** `GetDomainInfo(domain)`, parse `private`, write `enabled`.

**Delete:** when state says enabled, `RemovePrivacy(domain)`; otherwise no API
call. Removing the resource from configuration therefore disables privacy only
if it was on.

Markdown description must state that privacy can be unavailable or billable for
some TLDs (the API error is surfaced unchanged), and that a private WHOIS hides
registrant contact data.

### 6.4 `namesilo_contact`

A contact profile is an account-level object: create makes a new one on every
apply that starts from a create, and there is no name-based adoption, so import
by `contact_id` is the only way to take over an existing profile.

| Attribute | Type | Required/Optional/Computed | API field | Notes |
| --- | --- | --- | --- | --- |
| `first_name` | `string` | Required, Sensitive | `fn` | `LengthAtLeast(1)` |
| `last_name` | `string` | Required, Sensitive | `ln` | `LengthAtLeast(1)` |
| `address` | `string` | Required, Sensitive | `ad` | `LengthAtLeast(1)` |
| `address2` | `string` | Optional, Sensitive | `ad2` | `LengthAtMost(128)` |
| `city` | `string` | Required, Sensitive | `cy` | `LengthAtLeast(1)` |
| `state` | `string` | Required, Sensitive | `st` | `LengthAtLeast(1)` |
| `zip` | `string` | Required, Sensitive | `zp` | `LengthAtLeast(1)` |
| `country` | `string` | Required, Sensitive | `ct` | `LengthBetween(2, 2)`, uppercased |
| `email` | `string` | Required, Sensitive | `em` | `LengthAtLeast(1)` |
| `phone` | `string` | Required, Sensitive | `ph` | `LengthAtLeast(1)` |
| `fax` | `string` | Optional, Sensitive | `fx` | `LengthAtMost(32)` |
| `company` | `string` | Optional, Sensitive | `cp` | `LengthAtMost(64)` |
| `nickname` | `string` | Optional, Sensitive | `nn` | `LengthAtMost(24)` |
| `us_nexus_category` | `string` | Optional, Sensitive | `usnc` | `LengthAtMost(3)` |
| `us_application_purpose` | `string` | Optional, Sensitive | `usap` | `LengthAtMost(2)` |
| `ca_legal_form` | `string` | Optional, Sensitive | `calf` | CIRA |
| `ca_language` | `string` | Optional, Sensitive | `caln` | CIRA |
| `ca_agreement_version` | `string` | Optional, Sensitive | `caag` | CIRA |
| `ca_whois_display` | `string` | Optional, Sensitive | `cawd` | CIRA |
| `eu_citizenship_country` | `string` | Optional, Sensitive | `eucs` | `.eu` |
| `default_profile` | `bool` | Computed | `default_profile` | `1` is the account default |
| `id` | `string` | Computed | `contact_id` | `UseStateForUnknown` |

**PII policy (GDPR).** Every attribute that describes the contact person is
marked `Sensitive`: names, addresses, city, state, zip, country, email, phone,
fax, company, nickname, and the TLD-specific attestations. That matches GDPR
Article 4(1), where information relating to an identifiable natural person
includes names, location data, and online identifiers. Excluded from masking,
with reasons: `id`/`contact_id` is an opaque account-scoped identifier that is
needed as a plain reference and as the import value, `domain` does not exist on
this resource, and `default_profile` is account metadata rather than personal
data. `Sensitive` masks CLI output only; state still contains the data, which
the documentation states plainly, and practitioners are told they can add
`sensitive = true` to their own contact input variables for stronger masking.

**ModifyPlan:** optional fields whose planned value is `""` are normalized to
null, because the API returns unset fields as empty elements and Read writes
null. Required fields cannot be empty (validators), so nothing there needs
normalizing. `country` is uppercased to match the API.

**Create:** `AddContact(contact)` returns the new `contact_id`, which becomes
`id`. State echoes the configured fields, and `default_profile` is written as
`false`; the refresh that follows the apply corrects it, and because the
attribute is computed, that correction produces no plan diff. A second API call
in Create was rejected: if it failed after a successful add, the profile would
be orphaned with no ID in state.

**Update:** `UpdateContact(contact)` with `id` from state, then state is the
plan with `default_profile` carried over. `contactUpdate` takes the same fields
as `contactAdd`, so every managed field is sent on every update.

**Read:** `ListContacts(id)`. Exactly one profile is expected. Zero profiles
means the profile no longer exists, so the resource is removed from state with a
warning rather than left to fail every future plan; a profile absent from the
account is unambiguous, unlike a domain (see §11). API errors are diagnostics.

**Delete:** `DeleteContact(id)`. The API refuses to delete a profile still
associated with a domain, and the error is surfaced unchanged: the fix is to
reassign the domain's contacts first, which is what the dependency graph
expresses when `namesilo_domain_contacts` references this resource.

### 6.5 `namesilo_domain_contacts`

| Attribute | Type | Required/Optional/Computed | Notes |
| --- | --- | --- | --- |
| `domain` | `string` | Required | `stringplanmodifier.RequiresReplace()` |
| `registrant` | `string` | Optional, Computed | `UseStateForUnknown` |
| `administrative` | `string` | Optional, Computed | `UseStateForUnknown` |
| `technical` | `string` | Optional, Computed | `UseStateForUnknown` |
| `billing` | `string` | Optional, Computed | `UseStateForUnknown` |
| `id` | `string` | Computed | domain value, `UseStateForUnknown` |

Partial management is deliberate: a role that is omitted from configuration is
left alone, and the API's current value is stored as computed. Removing a role
from configuration stops managing it; it does not reassign the domain.

**Create/Update:** the set of roles to send is decided from `req.Config` (which
is null for omitted roles), and the values come from `req.Plan` (which is
unknown for omitted roles because they are `Computed`). Those roles are sent in
one `contactDomainAssociate` call. A following Read populates the computed
roles so state is complete and the plan is quiet.

**Read:** `GetDomainInfo(domain)` → `contact_ids` → all four roles.

**Delete:** a no-op. The API has no disassociate operation and a domain must
have all four roles, so destroying the resource stops managing the association
and leaves it intact. The description says this.

**Import:** by domain; `Read` fills all four roles.

Changing the registrant contact can trigger a registry contact-verification
email and, for some TLDs, is restricted; the API error is surfaced unchanged.
The resource description says so.

### 6.6 ModifyPlan and drift

`namesilo_nameservers`, `namesilo_dnssec_records`, and `namesilo_contact` use
`ModifyPlan` only to normalize configured values to the shape Read writes:
lowercase for nameserver names and digests, empty-to-null for optional contact
fields, and an uppercased country code. Nothing else is computed in `ModifyPlan`
beyond `namesilo_contact`'s `default_profile`, which is set in Create as
described in §6.4; `id` uses `UseStateForUnknown`, and no attribute is
`Computed` other than `id`, `default_profile`, and the four optional roles of
`namesilo_domain_contacts`. `namesilo_privacy` and `namesilo_domain_contacts`
need no plan modifier: a boolean and opaque contact IDs have nothing to
normalize.

Normalization at plan time is what keeps a lowercase-only API from producing a
perpetual diff. The plan, the applied state, and the next plan's normalized
configuration all agree, so invariant 1 holds.

Unknown values are skipped, not guessed: normalization runs only when the whole
collection and every element are known; anything unknown stays unknown and the
apply resolves it.

## 7. Data sources

| Data source | Input | Outputs |
| --- | --- | --- |
| `namesilo_nameservers` | `domain` | `nameservers` — `list(string)`, lowercased, in the API's position order |
| `namesilo_dnssec_records` | `domain` | `records` — `set(object)`, the same four fields as the resource |
| `namesilo_privacy` | `domain` | `enabled` — `bool` |
| `namesilo_contacts` | none | `contacts` — `set(object)`: `contact_id`, `default_profile`, and every profile field |

The data sources exist for the "list" half of the request: reading current
delegation, DS records, privacy, and contact profiles without managing them, for
outputs, comparisons, or drift detection in a plan. Each has its own `Read` that
calls the same client operations as the resources and writes normalized values.
The three domain-scoped schemas carry `id = domain`; `namesilo_contacts` has the
fixed `id` `contacts`, because it is account-scoped.

A data source whose `domain` is not in the account fails with the API error
rather than returning an empty result: an empty result would be indistinguishable
from a name typo.

`namesilo_contacts` returns full profiles, including the PII fields, because the
resource and the data source are the only way to read account contacts; the
same GDPR-driven `Sensitive` markings as §6.4 apply to the nested attributes.
An account with no contacts (never seen in practice; every account has at least
one default profile) returns an empty set rather than an error.

## 8. NameSilo API client

`internal/namesilo` is a self-contained client for the operations above. It has
no Terraform imports and no third-party dependencies beyond the standard
library; the boundary test in the package enforces the first, the license audit
in CI enforces the second.

### 8.1 Requests

Every operation is an HTTP GET:

```
{endpoint}/{operation}?version=1&type=xml&key={api_key}&{params}
```

- `endpoint` defaults to `https://www.namesilo.com/api`; the client stores the
  base URL without a trailing slash.
- `version=1` and `type=xml` are fixed.
- Parameters are always sent, even when empty, so the request is deterministic.
- The API key is passed as a query parameter because that is the only
  authentication the API offers. Every error path must keep it out of
  diagnostics; see §8.4.

### 8.2 Responses

The XML envelope is:

```xml
<namesilo>
  <request><operation>getDomainInfo</operation><ip>...</ip></request>
  <reply>
    <code>300</code>
    <detail>success</detail>
    ...
  </reply>
</namesilo>
```

Codes 300, 301, and 302 are success. Codes 255 ("Domain is already Private - No
update made") and 256 ("Domain is already Not Private - No update made") are
accepted as success by the privacy operations only: they mean the requested
state already holds, and treating them as errors would make an idempotent apply
fail. Anything else is an `*APIError` carrying `Operation`, `Code`, and `Detail`.
The relevant failure codes, from NameSilo's published list:

| Code | Meaning |
| --- | --- |
| 110 | Invalid API key |
| 113 | API account not allowed from this IP |
| 114 | Invalid domain syntax |
| 115 | Central registry not responding |
| 200 | Domain is not active, or does not belong to this user |
| 210 | General error (detail in the reply) |
| 254 | Nameserver update error |
| 280 | DNS modification error |
| 400 | Existing API request is still processing |

Codes are matched as strings; the code is never parsed as an int, because
`APIError` should reproduce exactly what the server said.

### 8.3 Operations

```go
type Client struct { /* endpoint, apiKey, version, *http.Client */ }

func NewClient(endpoint, apiKey, version string) *Client

func (c *Client) GetDomainInfo(ctx context.Context, domain string) (DomainInfo, error)
func (c *Client) ChangeNameServers(ctx context.Context, domain string, nameservers []string) error
func (c *Client) ListDSRecords(ctx context.Context, domain string) ([]DSRecord, error)
func (c *Client) AddDSRecord(ctx context.Context, domain string, record DSRecord) error
func (c *Client) DeleteDSRecord(ctx context.Context, domain string, record DSRecord) error
func (c *Client) AddPrivacy(ctx context.Context, domain string) error
func (c *Client) RemovePrivacy(ctx context.Context, domain string) error
func (c *Client) ListContacts(ctx context.Context, contactID string) ([]Contact, error)
func (c *Client) AddContact(ctx context.Context, contact Contact) (string, error)
func (c *Client) UpdateContact(ctx context.Context, contact Contact) error
func (c *Client) DeleteContact(ctx context.Context, contactID string) error
func (c *Client) AssociateContacts(ctx context.Context, domain string, roles ContactRoles) error
```

```go
type DomainInfo struct {
    Nameservers []string
    Private     bool
    Contacts    ContactRoles
}

// ContactRoles is a set of associations. An empty field means "not managed",
// not "clear this role"; the API has no way to clear a role.
type ContactRoles struct {
    Registrant     string
    Administrative string
    Technical      string
    Billing        string
}

// Contact mirrors the API's contact payload. Fields the API returns as empty
// elements stay empty strings; the provider layer maps them to null.
type Contact struct {
    ID             string
    DefaultProfile bool
    Nickname       string // nn
    Company        string // cp
    FirstName      string // fn
    LastName       string // ln
    Address        string // ad
    Address2       string // ad2
    City           string // cy
    State          string // st
    Zip            string // zp
    Country        string // ct
    Email          string // em
    Phone          string // ph
    Fax            string // fx
    UsNexusCategory       string // usnc
    UsApplicationPurpose  string // usap
    CaLegalForm           string // calf
    CaLanguage            string // caln
    CaAgreementVersion    string // caag
    CaWhoisDisplay        string // cawd
    EuCitizenshipCountry  string // eucs
}

type DSRecord struct {
    KeyTag     int64  // API field keyTag
    Algorithm  int64  // API field alg
    DigestType int64  // API field digestType
    Digest     string // API field digest
}
```

Field mapping: `dnsSecAddRecord` and `dnsSecDeleteRecord` take `domain`,
`digest`, `keyTag`, `digestType`, and `alg`; `dnsSecListRecords` returns
`ds_record` elements with `digest`, `digest_type`, `algorithm`, and `key_tag`.
`getDomainInfo` returns `nameservers > nameserver` elements whose text is the
name and whose `position` attribute is an index, a `private` element whose value
is `Yes` or `No`, and a `contact_ids` element with `registrant`,
`administrative`, `technical`, and `billing` children. `changeNameServers` takes
`domain` plus `ns1`–`ns13`, of which the first two are required. `addPrivacy`
and `removePrivacy` take only `domain`.

`contactAdd` takes the required fields `fn`, `ln`, `ad`, `cy`, `st`, `zp`,
`ct`, `em`, `ph` and the optional fields `nn`, `cp`, `ad2`, `fx`, `usnc`,
`usap`, `calf`, `caln`, `caag`, `cawd`, `eucs`, and returns `contact_id` in the
reply. `contactUpdate` takes the same fields plus `contact_id`. `contactDelete`
takes only `contact_id`. `contactList` takes an optional `contact_id`; with it,
the reply contains that profile (or none); without it, every profile. Each
profile is a `contact` element with `contact_id`, `default_profile` (`1`/`0`),
and the fields above. `contactDomainAssociate` takes `domain` and any subset of
`registrant`, `administrative`, `billing`, and `technical`, each a
`contact_id`; `AssociateContacts` sends only the non-empty roles.

### 8.4 Parsing quirks and error handling

NameSilo's XML is not self-describing, and the client must survive these cases,
each of which gets a fixture:

- A single `nameserver` or `ds_record` element is not a list; `encoding/xml`
  handles that, but the "empty element when there is nothing" form
  (`<ds_record/>` or a whitespace-only name) must be dropped rather than
  produced as an empty record.
- Nameserver names come back uppercased with a trailing dot sometimes; digests
  come back uppercased.
- `private` is `Yes` or `No`, matched case-insensitively. Any other value is an
  error rather than a default: guessing would silently flip a domain's WHOIS
  exposure.
- Unset contact fields come back as empty elements (`<address2/>`), so the
  client normalizes the whole element set to empty strings and the provider
  layer maps those to null. A single `contact` element in a `contactList`
  reply is a one-element result, not a scalar.
- `default_profile` is `1` or `0`; any other value is an error.
- The fixtures use the shape NameSilo's own `contactList` replies have, which is
  the same shape the official WHMCS module parses.
- Unknown elements are ignored (`encoding/xml` does that by default); a reply
  missing `<code>` entirely is an error, not a success.
- A non-200 HTTP status, a non-XML body, or a truncated body each produce an
  error naming the operation and the HTTP status, never the URL.
- The API key must never appear in an error message, a diagnostic, or a log
  line. The client builds errors from `Code` and `Detail` only, and never wraps
  the request URL. A unit test asserts this for HTTP failures, XML failures, and
  API errors by searching the error string for the key.

`strconv` failures while converting `key_tag`, `digest_type`, or `algorithm`
are errors naming the field but not the record's digest (a DS digest is public,
but the rule is simpler to state and test: field names only).

## 9. Pure logic

`internal/namesilo/normalize.go`, covered by table-driven tests with no HTTP
involvement:

```go
// NormalizeNameserver lowercases, trims space, and strips one trailing dot.
func NormalizeNameserver(name string) string

// NormalizeNameservers applies NormalizeNameserver and drops empty entries,
// preserving order. The result is not deduplicated: callers pass it through a
// set.
func NormalizeNameservers(nameservers []string) []string

// SameNameservers reports set equality after normalization and sorting.
func SameNameservers(a, b []string) bool

// NormalizeDSRecord applies the same treatment to a DS record: the digest is
// lowercased and trimmed. Integer fields are already typed.
func NormalizeDSRecord(record DSRecord) DSRecord

// Key returns the canonical identity of a record: the fields joined with a
// separator that cannot appear in any of them. Used for diffing and for
// deterministic ordering.
func (r DSRecord) Key() string

// DiffDSRecords returns what to add and what to remove to turn current into
// desired. Both results are sorted by Key, so a plan and its tests are
// deterministic.
func DiffDSRecords(current, desired []DSRecord) (add, remove []DSRecord)
```

`SameNameservers` exists for harness assertions and future use; the resource
itself does not need it, because Terraform's own set comparison decides whether
to call Update.

## 10. Resource identity, import, and drift

- `id` is the domain, set by Create and by `ImportState`, and preserved by
  `UseStateForUnknown` on update. `namesilo_contact` is the exception: its `id`
  is the API's `contact_id`.
- Import is `tofu import namesilo_nameservers.example example.com` and the same
  form for `namesilo_dnssec_records`, `namesilo_privacy`, and
  `namesilo_domain_contacts`. `namesilo_contact` imports by `contact_id`.
  `ImportState` sets `domain` or `id` from the import ID and leaves everything
  else to `Read`, so an imported resource converges on the real state without a
  config-specific seed (unlike the accumulator, which had no external system to
  read).
- Importing a domain with no DS records produces an empty set, which is a valid
  configuration (`records = []`) and therefore an empty plan once the config
  matches.
- Drift detection is the normal refresh: a nameserver change made in the web UI
  shows as an update, a DS record removed outside Terraform is re-added, a DS
  record added outside Terraform is removed, a privacy toggle made in the web UI
  shows as an update in the opposite direction, a contact profile edited in the
  web UI shows as an update, and a role reassigned in the web UI shows as an
  update when that role is configured.

## 11. Error handling and diagnostics

- Client errors become `AddError` diagnostics titled with the operation, e.g.
  `NameSilo API error (namesilo_nameservers)`. The detail is the API detail text
  plus the code.
- Read does not remove a resource from state on an API error. NameSilo's code
  200 conflates "expired", "inactive", and "not yours", so treating it as "gone"
  would silently drop a domain that is merely expired. `tofu state rm` remains
  the escape hatch for a domain that was transferred away. This is a documented
  limitation (§17). The one exception is `namesilo_contact`: a successful
  `contactList` filtered by ID that returns no profiles is not an API error, and
  it removes the resource from state with a warning (§6.4).
- A `500` or a network failure during Read is a diagnostic and leaves state
  untouched, so a transient outage cannot corrupt state.
- Conversion failures (`ElementsAs`, `SetValueFrom`) produce `AddError`
  diagnostics, as in the sibling providers.

## 12. Testing

Three tiers, each hermetic or opt-in; `make test` runs the first two.

### 12.1 Unit tests (`internal/namesilo`, `internal/provider`)

- Client request construction: `httptest` handlers assert the path, the fixed
  `version`/`type` parameters, the presence of `key`, and the operation
  parameters. A case per operation.
- Client response parsing: fixtures for a normal reply, a single-element list,
  an empty element, uppercase values, a missing `<code>`, a non-XML body, a
  non-200 status, and a malformed integer field.
- Error surface: the API key never appears in any error string; `Detail` and
  `Code` do.
- Pure helpers: `NormalizeNameserver(s)`, `SameNameservers`, `NormalizeDSRecord`,
  `DiffDSRecords` including empty inputs, adds and removes in one diff,
  case-only differences, and deterministic ordering.
- Privacy: `addPrivacy`/`removePrivacy` request shapes, and reply-code
  classification for 255 and 256, which are success for privacy operations and
  errors for every other operation. `private` parsing accepts `Yes`/`No` in any
  case and errors on anything else.
- Contacts: `contactAdd`/`contactUpdate`/`contactDelete` request shapes,
  `contactList` with and without a `contact_id`, fixtures with two profiles,
  one profile, and zero profiles, empty elements normalizing to empty strings,
  `default_profile` `1`/`0` parsing (and an error case), and
  `contactDomainAssociate` sending only the non-empty roles.
- Boundary test: `internal/namesilo` imports no `terraform-plugin-*` or
  `github.com/opentofu/*` package, mirroring the sibling providers.
- Provider guards: `TestProviderSchema` validates the whole schema through
  `GetProviderSchema` on the in-process protocol 6 server (no `TF_ACC`, no CLI),
  `TestEveryGoFileHasTheSPDXHeader`, and the em-dash house-style test, all
  adapted from the accumulator. A schema test also walks the contact resource
  and data source and asserts every PII attribute is marked `Sensitive` and
  `id`/`default_profile` are not, so the GDPR mapping cannot drift silently.

### 12.2 Harness tests (`internal/provider`, hermetic, need `tofu`)

`resource.Test` with `IsUnitTest: true` and the in-process
`ProtoV6ProviderFactories`, pointed at a fake NameSilo API
(`fakeserver_test.go`: `httptest.Server` plus in-memory domain and contact maps,
association state, a request log, and injectable failures). These exercise real
Create/Read/Update/Delete and import through the framework without credentials
or network access.

`TestMain` resolves `tofu` once and records whether it was found: it sets
`TF_ACC_TERRAFORM_PATH` when the variable is unset and `tofu` is on `PATH`, and
sets `TF_ACC_PROVIDER_HOST` to `registry.opentofu.org`. Harness tests call
`requireTofu(t)`, which skips with a message naming `make test` when no binary
was found, so `go test ./...` on a machine without OpenTofu still passes.

Cases:

| Test | What it asserts |
| --- | --- |
| nameservers create | The fake recorded one `changeNameServers` call with the configured set |
| nameservers read normalizes | A domain whose fake state is uppercase and dotted produces an empty plan |
| nameservers update | Changing the set issues one call with the new set |
| nameservers drift | Changing the fake's state out of band plans an update |
| nameservers destroy | Destroy issues `changeNameServers` with the dnsowl defaults |
| dnssec create with adopt | Pre-existing fake records are not re-added; missing ones are |
| dnssec update roll | A key roll adds the new record before deleting the old one (request order asserted) |
| dnssec drift | A record deleted out of band plans a re-add; one added out of band plans a removal |
| dnssec empty | `records = []` deletes every managed record and is quiet afterwards |
| dnssec destroy | Destroy deletes every managed record |
| privacy create enabled | The fake recorded one `addPrivacy` call |
| privacy create disabled | No privacy call is made |
| privacy toggle | Each direction change issues exactly one call |
| privacy drift | Flipping the fake's `private` out of band plans an update in the opposite direction |
| privacy destroy | Destroy calls `removePrivacy` when enabled, and makes no call when disabled |
| contact create | `contactAdd` records every configured field and the returned `contact_id` becomes `id` |
| contact update | Changing a field issues one `contactUpdate` and the plan is quiet afterwards |
| contact empty optional | Omitted optional fields come back from the fake as empty elements and plan quietly |
| contact drift | Editing the fake profile out of band plans an update |
| contact gone | A profile the fake no longer has is removed from state with a warning |
| contact destroy | Destroy calls `contactDelete` |
| contacts data source | Returns every profile from the fake, including a profile with empty optional fields |
| association partial | Only configured roles are sent; omitted roles are stored as computed |
| association update | Changing one role sends one call with that role and leaves the rest |
| association drift | Reassigning a role in the fake plans an update for that role only |
| association destroy | Destroy makes no API call and leaves the fake's associations intact |
| import all five | Import yields state that matches the fake and plans empty |
| data sources | All four return normalized values that match the fake |

### 12.3 Live acceptance tests (`internal/provider`, opt-in)

`TestAccLive*` run only when the harness precheck finds `TF_ACC=1`,
`NAMESILO_API_KEY`, `NAMESILO_TEST_DOMAIN`, `TF_ACC_TERRAFORM_PATH`, and
`TF_ACC_PROVIDER_HOST`. Missing credentials skip with an actionable message; a
missing `TF_ACC` skips as usual. DS mutation tests additionally require
`NAMESILO_TEST_DNSSEC=1`, privacy mutation tests require
`NAMESILO_TEST_PRIVACY=1`, because privacy is unavailable for some TLDs and
toggles WHOIS exposure, contact mutation tests require `NAMESILO_TEST_CONTACT=1`
because they add and delete profiles in a live account, and association tests
require `NAMESILO_TEST_DOMAIN_CONTACTS=1` because changing a domain's registrant
can trigger registry verification and, for some TLDs, is restricted.

`NAMESILO_TEST_DOMAIN` must be a **disposable domain** in the account: the
nameserver tests repoint its delegation (to `ns1.example.net`/`ns2.example.net`
and back to the dnsowl defaults on destroy), the DS tests publish and delete a
synthetic DS record, which makes the domain fail DNSSEC validation while it
exists, the privacy tests toggle WHOIS privacy, and the association tests
repoint a role at a throwaway profile.

Cases: read all four data sources; create/read/update/destroy the nameservers
resource and assert quiet plans; import every resource; add, roll, and remove a
DS record; enable, disable, and re-enable privacy; create, update, and delete a
contact profile; reassign a domain role and assert the other roles are
untouched. Nothing in CI requires these; they are the contract check that the
XML fixtures match the real API, and they run with `make testacc-live`.

## 13. Documentation

`docs/` is generated by `tfplugindocs` from the schema and `examples/`, then
checked in. `make docs` runs `tools/gen-schema.sh` followed by
`go generate ./...` in the `tools` module, and CI fails on a resulting
`git diff`. `tools/gen-schema.sh` is adapted from the accumulator: it builds
the provider, exports the schema with `tofu providers schema -json` through a
`dev_overrides` CLI config, and rewrites the
`registry.opentofu.org/nijave/namesilo` key to the bare `namesilo` name that
`tfplugindocs --providers-schema` expects.

`docs/superpowers/` is hand-written and is not an input or output of
generation; the generate job's dirty-tree check catches accidental changes, and
`tools/schema.json` is gitignored so exporting the schema is not mistaken for
drift.

Examples: `examples/provider/provider.tf` (api_key from a variable, endpoint
commented), one `resource.tf` plus `import.sh` per resource, and one
`data-source.tf` per data source.

## 14. Build, CI, and release

`GNUmakefile` targets, adapted from the siblings:

- `build`, `fmt`, `vet`, `test` (unit plus hermetic harness; sets
  `TF_ACC_TERRAFORM_PATH` when `tofu` is present), `testacc-live` (sets
  `TF_ACC=1` and the `TF_ACC_*` variables; skips without credentials), `docs`,
  `release`.

`.github/workflows/test.yml` jobs, on pull requests and pushes to `main`:

- **build** — `go build`, `gofmt -l` empty, `go vet`.
- **unit** — installs a pinned OpenTofu, then `go test -v -cover ./internal/...`,
  so the harness tests run against the fake API; `TestMain` points the harness at
  the installed binary.
- **generate** — pinned OpenTofu (1.12.4, as the accumulator pins: schema export
  output changes when the exporting CLI crosses a feature threshold), `make
  docs`, fail if the tree is dirty.
- **acceptance** — matrix `tofu` 1.10.* and 1.12.*, running the same harness
  suite on the oldest supported line and the newest. Live tests are skipped
  without secrets. A separate optional job runs `make testacc-live` when
  `NAMESILO_API_KEY` is configured; because secrets cannot gate a job-level
  `if`, the job sets an env var from the secret and steps use
  `if: env.NAMESILO_API_KEY != ''`. Without the secret the job is a no-op, not a
  failure.
- **license** — `go-licenses check` with forbidden/restricted/unknown
  disallowed.

`.github/workflows/release.yml` runs goreleaser on `v*` tags with GPG signing
and a draft release, matching the siblings. It requires `GPG_PRIVATE_KEY` and
`PASSPHRASE` repository secrets.

`.goreleaser.yml`, `terraform-registry-manifest.json`, and `dependabot.yml` are
copied from the accumulator with the project name changed. `dependabot.yml`
groups `github.com/hashicorp/terraform-plugin-*` so the plugin modules move in
lockstep, and covers the `tools` module separately.

## 15. License

GPL-3.0-or-later, matching the siblings. Every `.go` file begins with
`// SPDX-License-Identifier: GPL-3.0-or-later`. Dependencies must be
GPLv3-compatible; with the own-client decision the audited set is the
Hashicorp MPL-2.0 framework modules, BSD-3-Clause, MIT, and Apache-2.0.
Nothing under BUSL-1.1 may be linked or downloaded: a provider is a separate
process speaking gRPC, so Terraform CLI is not a dependency, and CI installs
OpenTofu instead.

## 16. Decisions and rejected alternatives

- **Own client over `nrdcg/namesilo`.** The library is maintained and its
  licence is fine, but it is generated from 2019 documentation, embeds raw
  response bodies in errors (an API-key redaction problem, since the request
  carries the key), and wraps nothing we would not write ourselves. Five GET
  operations do not justify the dependency.
- **XML over JSON.** The API serves both, but only XML is documented with
  examples and exercised by every community client; the JSON shape is
  undocumented.
- **One resource per domain for DS records.** The API's own identity for a DS
  record is a tuple, which makes a per-record resource's import ID opaque and
  its key rolls every attribute change at once. A set-managed resource makes a
  roll a two-element diff and keeps state readable. Chosen by the project owner.
- **Adds before deletes.** A DS roll with the old record removed first can fail
  validation for as long as the gap lasts. Reconcile order is part of the
  contract, and a test asserts it.
- **Destroy resets nameservers to NameSilo's defaults.** Chosen by the project
  owner over the "leave delegation untouched" alternative. It means removing the
  resource from configuration repoints the domain at NameSilo's parking DNS, so
  the resource and data source descriptions say so plainly. If the domain hosts
  its zone elsewhere, that is an outage until the resource is re-applied or the
  web UI is used.
- **Destroy deletes DS records.** Standard Terraform semantics, chosen by the
  project owner. Documented as disabling DNSSEC, with a warning that destroying
  the zone's keys and this resource in the wrong order breaks validation.
- **Normalize at plan time, not with diff suppression.** The API's lowercase
  responses are folded into both state and plan, so there is nothing to
  suppress and `tofu plan` output is truthful.
- **No retries.** NameSilo's error codes do not distinguish "try again" from
  "your request is wrong" well enough to retry safely, and a provider apply is
  already a retry loop a human supervises. Code 400 is documented as re-run.
- **Privacy is a boolean on a resource, not a presence-only resource.** Chosen
  by the project owner. `enabled = false` lets configuration assert "this domain
  must not use PrivacyGuardian", which a presence-only resource cannot express,
  and it makes drift visible in both directions.
- **Privacy's 255/256 replies are success.** They mean "already private" and
  "already not private". Treating them as errors, as the community Go client
  does, would make a converging apply fail. The classification is scoped to the
  privacy operations so a 255/256 from another operation is still an error.
- **Contact profiles and contact associations are separate resources.** A
  profile is account-scoped and can be referenced by many domains; an
  association is domain-scoped. Merging them would recreate the profile on
  every association change and make a profile shared between domains
  impossible.
- **Association roles are optional and partially managed.** A role that is
  omitted from configuration is left alone and stored as computed. Requiring
  all four would force every configuration to carry IDs it does not manage, and
  an update could not distinguish "set this role" from "I copied the current
  value".
- **Association destroy is a no-op.** The API has no disassociate operation and
  a domain cannot have a missing role, so there is nothing to restore. The
  resource description says so; the alternative, erroring on destroy, would make
  `tofu destroy` fail on a resource the user is trying to stop managing.
- **Contact profiles are deleted from state when they disappear.** Zero results
  from a `contactList` filtered by ID is unambiguous: the profile is gone.
  This is the opposite of the domain case (§11), where the API's "not active or
  not yours" reply cannot be told apart from an expired but still-owned domain.
- **PII masking is static, and write-only attributes are rejected.** The
  framework cannot set `Sensitive` conditionally, and write-only attributes
  would remove the drift detection that is the point of the resource, need a
  companion version attribute, and are only supported by OpenTofu from 1.11.
  The resource descriptions point practitioners at `sensitive = true` on their
  own input variables for stronger masking.
- **No provider-defined functions or ephemeral resources.** Nothing in the
  scope needs either.

## 17. Limitations

- `api_key` is passed as a query parameter to every API call. That is NameSilo's
  design; the provider keeps it out of diagnostics and state, and the attribute
  is Sensitive, but the key is visible to anything that logs the request URL,
  including proxies on the operator's side.
- DS records are managed in the `(digest, key_tag, digest_type, algorithm)`
  form only. TLDs that require the "pubkey, flags, algorithm" form cannot be
  managed through this API at all.
- No automatic retry for code 400 ("existing API request is still processing");
  re-run the apply.
- A domain transferred away or expired makes Read fail with code 200 rather than
  removing itself from state; use `tofu state rm` if the domain is really gone.
- The provider does not verify that a published DS record matches the zone's
  keys. A correct configuration of the wrong values still breaks validation.
- WHOIS privacy is not available for every TLD, and NameSilo may charge for it
  on some; the API's error is surfaced unchanged and the resource's description
  says so. The provider cannot pre-check support for a TLD.
- `contactDelete` refuses a profile that is still associated with a domain, and
  the account's default profile cannot be deleted. The API error is surfaced;
  the fix is to reassign the domain first, which the dependency graph expresses.
- Changing a domain's registrant contact can trigger an ICANN
  registrant-verification email, and an unverified registrant can have domains
  suspended. The provider does not manage verification.
- Contact profiles are stored in state as plaintext. The `Sensitive` markings
  mask CLI output only; they do not encrypt or redact state. Treat the state
  file as personal data.
- `contactList` is called once with no offset. If NameSilo returns a partial
  list for a large account, the data source surfaces exactly what the API
  returned.
- The sandbox environment requires credentials from NameSilo support; the
  `endpoint` attribute makes it usable, but CI cannot exercise it.
