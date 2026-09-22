// SPDX-License-Identifier: GPL-3.0-or-later

package provider_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
)

// privacyDataSourceConfig renders one namesilo_privacy data block.
func privacyDataSourceConfig(endpoint, domain string) string {
	return namesiloProviderConfig(endpoint) + `
data "namesilo_privacy" "test" {
  domain = "` + domain + `"
}
`
}

// TestAccPrivacyDataSource covers §12.2 "data sources" for the privacy read:
// enabled is the fake domain's private flag, and id is the domain.
func TestAccPrivacyDataSource(t *testing.T) {
	requireTofu(t)
	fake := newFakeNamesilo()
	defer fake.Close()
	fake.addDomain("example.com").private = true

	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: privacyDataSourceConfig(fake.URL(), "example.com"),
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue("data.namesilo_privacy.test",
					tfjsonpath.New("enabled"), knownvalue.Bool(true)),
				statecheck.ExpectKnownValue("data.namesilo_privacy.test",
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

// TestAccPrivacyDataSourceMissingDomain covers §7's closing rule: a domain not
// in the account fails with the API's error, never an empty result, because an
// empty result would be indistinguishable from a name typo. The provider-side
// phrase the detail carries cannot be echoed from the configuration.
func TestAccPrivacyDataSourceMissingDomain(t *testing.T) {
	requireTofu(t)
	fake := newFakeNamesilo()
	defer fake.Close()

	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config:      privacyDataSourceConfig(fake.URL(), "notthere.example.com"),
			ExpectError: regexp.MustCompile(`Domain is not active, or does not belong to this user`),
		}},
	})
}
