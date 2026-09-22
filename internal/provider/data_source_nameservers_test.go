// SPDX-License-Identifier: GPL-3.0-or-later

package provider_test

import (
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
)

// nameserversDataSourceConfig renders one namesilo_nameservers data block.
func nameserversDataSourceConfig(endpoint, domain string) string {
	return namesiloProviderConfig(endpoint) + `
data "namesilo_nameservers" "test" {
  domain = "` + domain + `"
}
`
}

// expectNameserversDataSourceOrder checks the data source's list equals the
// given values in exactly this order: nameservers is a list on purpose, and
// the position order is the delegation's real order (§7).
func expectNameserversDataSourceOrder(values ...string) statecheck.StateCheck {
	return statecheck.ExpectKnownValue("data.namesilo_nameservers.test",
		tfjsonpath.New("nameservers"), knownvalue.ListExact(stringSet(values...)))
}

// TestAccNameserversDataSource covers §12.2 "data sources" for the delegation
// read: the fake seeds nameservers in the API's raw shape, uppercase with a
// trailing dot, and the data source must return them lowercased and in the
// same position order. A set would lose the order, which is why §7 spells the
// attribute as a list.
func TestAccNameserversDataSource(t *testing.T) {
	requireTofu(t)
	fake := newFakeNamesilo()
	defer fake.Close()
	fake.addDomain("example.com").nameservers = []string{"NS2.EXAMPLE.NET.", "NS1.EXAMPLE.NET"}

	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: nameserversDataSourceConfig(fake.URL(), "example.com"),
			ConfigStateChecks: []statecheck.StateCheck{
				// Deliberately not sorted: position two's nameserver is
				// returned in position two, so a list, not a set.
				expectNameserversDataSourceOrder("ns2.example.net", "ns1.example.net"),
				statecheck.ExpectKnownValue("data.namesilo_nameservers.test",
					tfjsonpath.New("id"), knownvalue.StringExact("example.com")),
			},
			PostApplyFunc: func() {
				if last := fake.lastRequest("getDomainInfo"); strings.Contains(last, "test-key") {
					t.Errorf("the request log leaked the API key: %s", last)
				}
			},
		}},
	})
}
