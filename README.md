# terraform-provider-namesilo

A Terraform and OpenTofu provider for NameSilo registrar state — nameserver
delegation, DS (DNSSEC) records, WHOIS privacy, the registrar lock, auto-renew,
contact profiles, and domain contact associations — with read-only data
sources for the same state, including the account's full domain inventory.

NameSilo's registrar-level settings are exactly what a Terraform/OpenTofu
configuration wants to own: the delegation the registry publishes, whether the
domain is locked and private and renewed automatically, and which of the
account's contact profiles answer for it. The resources are authoritative over
their attributes — the API replaces whole values rather than patching them —
and every resource imports by the domain name (the contact resource imports by
the API's `contact_id`).

## Example

```hcl
provider "namesilo" {
  api_key = var.namesilo_api_key
}

resource "namesilo_nameservers" "delegation" {
  domain = "example.com"

  nameservers = [
    "ns1.example.net",
    "ns2.example.net",
  ]
}

resource "namesilo_dnssec_records" "ds" {
  domain = "example.com"

  records = [{
    key_tag     = 2371
    algorithm   = 13
    digest_type = 2
    digest      = "8f9e1c6f5d5d5c9a1e0d3b7f2a4c6e8d0b2f4a6c8e0d2f4a6b8c0e2d4f6a8c0e"
  }]
}
```

## Resources

- **`namesilo_nameservers`** manages a domain's delegation as a complete set.
  Destroy repoints the domain at NameSilo's default nameservers — an outage if
  the zone is hosted elsewhere.
- **`namesilo_dnssec_records`** manages the domain's DS records as a complete
  set. Destroy deletes them and disables DNSSEC.
- **`namesilo_privacy`** manages WHOIS privacy (PrivacyGuardian) for a domain.
- **`namesilo_domain_lock`** manages the registrar transfer lock. Destroy
  releases the lock.
- **`namesilo_auto_renew`** manages the auto-renew flag. Destroy turns renewal
  off, and the domain can then expire uncaught.
- **`namesilo_contact`** manages an account-level contact profile, identified
  by the API's `contact_id`. Every personal-data attribute is marked
  `Sensitive`.
- **`namesilo_domain_contacts`** points a domain's four contact roles at
  profile `contact_id`s. Omitted roles are left alone, and destroy leaves the
  associations in place.

All seven import with the domain name as the ID, except `namesilo_contact`,
which imports with the `contact_id`:

```sh
tofu import namesilo_nameservers.delegation example.com
tofu import namesilo_contact.profile 1001
```

## Data sources

- **`namesilo_domain`** reads one domain's registrar attributes — dates,
  status, lock, privacy, auto-renew, forwarding, and the current role
  assignments.
- **`namesilo_domains`** reads the account's whole inventory with a total.
- **`namesilo_nameservers`**, **`namesilo_dnssec_records`**,
  **`namesilo_privacy`**, and **`namesilo_contacts`** read the delegation, the
  DS records, the privacy flag, and every contact profile.

## Requirements

- OpenTofu >= 1.10 (the primary target, and what CI tests) or Terraform >= 1.10.
  Terraform is expected to work but is not tested.
- Go >= 1.25.12 to build (the module's `go` directive is the floor).

## Configuration

The provider block takes three optional attributes. `api_key` falls back to
the `NAMESILO_API_KEY` environment variable, and configuration fails when the
resolved value is empty: set the key from the environment or from a sensitive
variable, never in source control. `endpoint` falls back to the
`NAMESILO_API_ENDPOINT` environment variable, then to the production endpoint
`https://www.namesilo.com/api`; a trailing slash is trimmed. `page_size` is
the page size the `namesilo_domains` data source requests for the inventory;
it defaults to 100.

## Destroy semantics

Destroying these resources performs a final API call — it does not simply
stop managing the attribute:

- `namesilo_nameservers` — removing the resource repoints the domain at
  NameSilo's default nameservers (`ns1.dnsowl.com`, `ns2.dnsowl.com`,
  `ns3.dnsowl.com`), because the API has no remove-all-nameservers call. That
  is an outage if the zone is hosted elsewhere. Destroy the resource only when
  the domain is being retired or is moving to a different delegation.
- `namesilo_dnssec_records` — destroying the resource deletes the domain's DS
  records, which disables DNSSEC.
- `namesilo_domain_lock` — destroying the resource releases the registrar
  lock and leaves the domain free to transfer.
- `namesilo_auto_renew` — destroying the resource turns auto-renew off; the
  domain can then expire uncaught.
- `namesilo_domain_contacts` — destroying the resource stops managing the
  role assignments and leaves them in place; the API cannot clear a role.

## Personal data in state

`namesilo_contact` and `namesilo_domain_contacts` store contact data — names,
addresses, phone numbers, email addresses — and `namesilo_contacts` reads all
of it back. Every such attribute is marked `Sensitive`, which masks CLI
output only: the state file still contains the data. Treat the state file as
personal data and store it accordingly. For stronger masking, add
`sensitive = true` to your own input variables and feed the resources from
them.

## Documentation

The [provider documentation](docs/index.md) covers every resource and data
source, with the generated schema for each.

## Development

```sh
make test         # unit tests plus the hermetic fake-API harness
make testacc-live # live acceptance tests; requires tofu >= 1.10 and an API key
make docs         # regenerate docs/ ; requires tofu >= 1.11
```

## License

GPL-3.0-or-later. Every dependency is GPL-compatible (MPL-2.0, BSD-3-Clause,
MIT, or Apache-2.0). See [LICENSE](LICENSE).
