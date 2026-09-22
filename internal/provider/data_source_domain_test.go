// SPDX-License-Identifier: GPL-3.0-or-later

package provider_test

import (
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/nijave/terraform-provider-namesilo/internal/namesilo"
)

// domainDataSourceConfig renders one namesilo_domain data block.
func domainDataSourceConfig(endpoint, domain string) string {
	return namesiloProviderConfig(endpoint) + `
data "namesilo_domain" "test" {
  domain = "` + domain + `"
}
`
}

// expectDomainField checks one data attribute of data.namesilo_domain.test.
func expectDomainField(attr string, check knownvalue.Check) statecheck.StateCheck {
	return statecheck.ExpectKnownValue("data.namesilo_domain.test",
		tfjsonpath.New(attr), check)
}

// TestAccDomainDataSource covers §12.2 "domain data source" and §7's namesilo_domain
// contract: every getDomainInfo field is returned, the Yes/No replies are
// booleans, the dates stay strings in the API's YYYY-MM-DD form, the
// nameservers are lowercased, and contact_ids holds the four roles. Step 1
// asserts the API's "no forwarding" value passes through as the literal "N/A";
// step 2 rewrites the forwarding fields out of band and asserts they pass
// through unchanged too, because forward_url and forward_type are exposed
// exactly as the API sends them.
func TestAccDomainDataSource(t *testing.T) {
	requireTofu(t)
	fake := newFakeNamesilo()
	defer fake.Close()
	d := fake.addDomain("example.com")
	d.created = "2020-01-01"
	d.expires = "2026-01-01"
	d.status = "active"
	d.locked = true
	d.private = true
	d.autoRenew = true
	d.nameservers = []string{"NS1.EXAMPLE.NET.", "NS2.EXAMPLE.NET"}
	d.roles = namesilo.ContactRoles{Registrant: "1001", Technical: "1003"}

	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: domainDataSourceConfig(fake.URL(), "example.com"),
				ConfigStateChecks: []statecheck.StateCheck{
					expectDomainField("id", knownvalue.StringExact("example.com")),
					expectDomainField("created", knownvalue.StringExact("2020-01-01")),
					expectDomainField("expires", knownvalue.StringExact("2026-01-01")),
					expectDomainField("status", knownvalue.StringExact("active")),
					expectDomainField("locked", knownvalue.Bool(true)),
					expectDomainField("private", knownvalue.Bool(true)),
					expectDomainField("auto_renew", knownvalue.Bool(true)),
					// The fake leaves traffic_type and portfolio empty; the
					// data source exposes exactly what the API sent.
					expectDomainField("traffic_type", knownvalue.StringExact("")),
					expectDomainField("email_verification_required", knownvalue.Bool(false)),
					expectDomainField("portfolio", knownvalue.StringExact("")),
					// Forwarding is off, so the API says N/A, and the data
					// source says N/A: mapping it to null would invent a
					// contract (§7).
					expectDomainField("forward_url", knownvalue.StringExact("N/A")),
					expectDomainField("forward_type", knownvalue.StringExact("N/A")),
					expectDomainField("nameservers",
						knownvalue.ListExact(stringSet("ns1.example.net", "ns2.example.net"))),
					expectDomainField("contact_ids", knownvalue.ObjectExact(map[string]knownvalue.Check{
						"registrant":     knownvalue.StringExact("1001"),
						"administrative": knownvalue.Null(),
						"technical":      knownvalue.StringExact("1003"),
						"billing":        knownvalue.Null(),
					})),
				},
				PostApplyFunc: func() {
					if last := fake.lastRequest("getDomainInfo"); strings.Contains(last, "test-key") {
						t.Errorf("the request log leaked the API key: %s", last)
					}
				},
			},
			{
				PreConfig: func() {
					fake.setForward("example.com", "https://example.org", "STD-REDIRECT-302")
				},
				Config: domainDataSourceConfig(fake.URL(), "example.com"),
				ConfigStateChecks: []statecheck.StateCheck{
					expectDomainField("forward_url", knownvalue.StringExact("https://example.org")),
					expectDomainField("forward_type", knownvalue.StringExact("STD-REDIRECT-302")),
				},
			},
		},
	})
}
