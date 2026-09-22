// SPDX-License-Identifier: GPL-3.0-or-later

package provider_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
)

// namesiloProviderConfig renders the provider block the harness tests point at
// the fake server. test-key is the key the fake accepts; every test asserts
// separately that it never appears in a diagnostic or in the request log.
func namesiloProviderConfig(endpoint string) string {
	return `
provider "namesilo" {
  api_key  = "test-key"
  endpoint = "` + endpoint + `"
}
`
}

// nameserversConfig renders one namesilo_nameservers block. nameservers is a
// raw HCL list literal so a test can spell the values however it needs.
func nameserversConfig(endpoint, nameservers string) string {
	return namesiloProviderConfig(endpoint) + `
resource "namesilo_nameservers" "test" {
  domain      = "example.com"
  nameservers = ` + nameservers + `
}
`
}

// expectNameservers checks the applied state's set equals the given values.
func expectNameservers(values ...string) statecheck.StateCheck {
	return statecheck.ExpectKnownValue("namesilo_nameservers.test",
		tfjsonpath.New("nameservers"), knownvalue.SetExact(stringSet(values...)))
}

// checkDestroyRepoints returns a CheckDestroy that asserts the final
// changeNameServers carried the dnsowl defaults and nothing else. CheckDestroy
// runs at the end of the resource's destroy, before resource.Test returns, so
// it sees the destroy call rather than any later teardown.
func checkDestroyRepoints(fake *fakeNamesilo) resource.TestCheckFunc {
	return func(_ *terraform.State) error {
		last := fake.lastRequest("changeNameServers")
		for _, want := range []string{"ns1=ns1.dnsowl.com", "ns2=ns2.dnsowl.com", "ns3=ns3.dnsowl.com"} {
			if !strings.Contains(last, want) {
				return fmt.Errorf("destroy did not repoint at %s: %s", want, last)
			}
		}
		return nil
	}
}

// TestAccNameserversCreate covers §6.1 Create: one changeNameServers call
// carrying the configured set, the domain as id, and a quiet plan afterwards.
func TestAccNameserversCreate(t *testing.T) {
	requireTofu(t)
	fake := newFakeNamesilo()
	defer fake.Close()
	fake.addDomain("example.com").nameservers = []string{"ns1.dnsowl.com", "ns2.dnsowl.com", "ns3.dnsowl.com"}

	// Count the configured call before the automatic destroy runs, then let the
	// CheckDestroy assert the destroy's own call.
	createdWithConfiguredSet := false
	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             checkDestroyRepoints(fake),
		Steps: []resource.TestStep{{
			Config: nameserversConfig(fake.URL(), `["ns1.example.net", "ns2.example.net"]`),
			ConfigStateChecks: []statecheck.StateCheck{
				expectNameservers("ns1.example.net", "ns2.example.net"),
				statecheck.ExpectKnownValue("namesilo_nameservers.test", tfjsonpath.New("id"),
					knownvalue.StringExact("example.com")),
			},
			ConfigPlanChecks: expectEmptyAfterRefresh(),
			PostApplyFunc: func() {
				last := fake.lastRequest("changeNameServers")
				createdWithConfiguredSet = strings.Contains(last, "ns1=ns1.example.net") &&
					strings.Contains(last, "ns2=ns2.example.net")
				if strings.Contains(last, "test-key") {
					t.Errorf("the request log leaked the API key: %s", last)
				}
			},
		}},
	})

	if !createdWithConfiguredSet {
		t.Errorf("changeNameServers was not called with the configured set: %s", fake.lastRequest("changeNameServers"))
	}
}

// TestAccNameserversReadNormalizes covers §4 invariant 1 against an API that
// returns uppercase, trailing-dotted names: the lowercase configuration plans
// and applies quietly, because ModifyPlan and Read both normalize.
func TestAccNameserversReadNormalizes(t *testing.T) {
	requireTofu(t)
	fake := newFakeNamesilo()
	defer fake.Close()
	fake.addDomain("example.com").nameservers = []string{"NS1.EXAMPLE.NET.", "NS2.EXAMPLE.NET."}

	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: nameserversConfig(fake.URL(), `["ns1.example.net", "ns2.example.net"]`),
				ConfigStateChecks: []statecheck.StateCheck{
					expectNameservers("ns1.example.net", "ns2.example.net"),
				},
				ConfigPlanChecks: expectEmptyAfterRefresh(),
			},
			{
				// A second identical step: the normalized state must still
				// match the configuration with no diff.
				Config:           nameserversConfig(fake.URL(), `["ns1.example.net", "ns2.example.net"]`),
				ConfigPlanChecks: expectEmptyAfterRefresh(),
			},
		},
	})
}

// TestAccNameserversUpdate covers §6.1 Update: each changed step issues one
// changeNameServers call with that step's set, and the plan afterwards is
// quiet.
func TestAccNameserversUpdate(t *testing.T) {
	requireTofu(t)
	fake := newFakeNamesilo()
	defer fake.Close()
	fake.addDomain("example.com").nameservers = []string{"ns1.dnsowl.com", "ns2.dnsowl.com", "ns3.dnsowl.com"}

	var afterCreate, afterUpdate string
	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:            nameserversConfig(fake.URL(), `["ns1.example.net", "ns2.example.net"]`),
				ConfigStateChecks: []statecheck.StateCheck{expectNameservers("ns1.example.net", "ns2.example.net")},
				ConfigPlanChecks:  expectEmptyAfterRefresh(),
				PostApplyFunc:     func() { afterCreate = fake.lastRequest("changeNameServers") },
			},
			{
				Config:            nameserversConfig(fake.URL(), `["ns3.example.net", "ns4.example.net"]`),
				ConfigStateChecks: []statecheck.StateCheck{expectNameservers("ns3.example.net", "ns4.example.net")},
				ConfigPlanChecks:  expectEmptyAfterRefresh(),
				PostApplyFunc:     func() { afterUpdate = fake.lastRequest("changeNameServers") },
			},
		},
	})

	if !strings.Contains(afterCreate, "ns1=ns1.example.net") || !strings.Contains(afterCreate, "ns2=ns2.example.net") {
		t.Errorf("the first step did not carry the first set: %s", afterCreate)
	}
	if !strings.Contains(afterUpdate, "ns1=ns3.example.net") || !strings.Contains(afterUpdate, "ns2=ns4.example.net") {
		t.Errorf("the update did not carry the new set: %s", afterUpdate)
	}
}

// TestAccNameserversDrift covers §10: a nameserver change made out of band
// refreshes into state and plans an in-place update.
func TestAccNameserversDrift(t *testing.T) {
	requireTofu(t)
	fake := newFakeNamesilo()
	defer fake.Close()
	fake.addDomain("example.com").nameservers = []string{"ns1.dnsowl.com", "ns2.dnsowl.com", "ns3.dnsowl.com"}

	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:            nameserversConfig(fake.URL(), `["ns1.example.net", "ns2.example.net"]`),
				ConfigStateChecks: []statecheck.StateCheck{expectNameservers("ns1.example.net", "ns2.example.net")},
				ConfigPlanChecks:  expectEmptyAfterRefresh(),
			},
			{
				PreConfig: func() {
					// Out-of-band change, as the web UI would make it.
					fake.setNameservers("example.com", []string{"ns5.example.net", "ns6.example.net"})
				},
				Config:            nameserversConfig(fake.URL(), `["ns1.example.net", "ns2.example.net"]`),
				ConfigStateChecks: []statecheck.StateCheck{expectNameservers("ns1.example.net", "ns2.example.net")},
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("namesilo_nameservers.test",
							plancheck.ResourceActionUpdate),
					},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
		},
	})
}

// TestAccNameserversDestroy covers §4 invariant 4: destroying the resource
// repoints the domain at NameSilo's default nameservers rather than clearing
// delegation.
func TestAccNameserversDestroy(t *testing.T) {
	requireTofu(t)
	fake := newFakeNamesilo()
	defer fake.Close()
	fake.addDomain("example.com").nameservers = []string{"ns1.dnsowl.com", "ns2.dnsowl.com", "ns3.dnsowl.com"}

	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		// resource.Test runs the destroy after the steps; CheckDestroy sees
		// the destroy's own changeNameServers call.
		CheckDestroy: checkDestroyRepoints(fake),
		Steps: []resource.TestStep{{
			Config:            nameserversConfig(fake.URL(), `["ns1.example.net", "ns2.example.net"]`),
			ConfigStateChecks: []statecheck.StateCheck{expectNameservers("ns1.example.net", "ns2.example.net")},
			ConfigPlanChecks:  expectEmptyAfterRefresh(),
		}},
	})
}

// TestAccNameserversImport covers §10: importing by domain seeds id and
// domain, Read fills nameservers, and a settling apply against a matching
// configuration is quiet.
func TestAccNameserversImport(t *testing.T) {
	requireTofu(t)
	fake := newFakeNamesilo()
	defer fake.Close()
	fake.addDomain("example.com").nameservers = []string{"ns1.example.net", "ns2.example.net"}

	config := nameserversConfig(fake.URL(), `["ns1.example.net", "ns2.example.net"]`)

	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:             config,
				ResourceName:       "namesilo_nameservers.test",
				ImportState:        true,
				ImportStateId:      "example.com",
				ImportStatePersist: true,
				ImportStateVerify:  false,
			},
			{
				// The settling step: Read produced the fake's nameservers and
				// the configuration matches, so the plan is empty.
				Config:            config,
				ConfigStateChecks: []statecheck.StateCheck{expectNameservers("ns1.example.net", "ns2.example.net")},
				ConfigPlanChecks:  expectEmptyAfterRefresh(),
			},
		},
	})
}
