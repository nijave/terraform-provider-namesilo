// SPDX-License-Identifier: GPL-3.0-or-later

package provider_test

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/nijave/terraform-provider-namesilo/internal/namesilo"
)

// domainContactsConfig renders one namesilo_domain_contacts block. roles maps
// role attribute names to contact IDs; a role absent from the map is absent
// from the configuration, which is what partial management keys on. Attributes
// are sorted for deterministic output.
func domainContactsConfig(endpoint string, roles map[string]string) string {
	names := make([]string, 0, len(roles))
	for name := range roles {
		names = append(names, name)
	}
	sort.Strings(names)

	var b strings.Builder
	b.WriteString(namesiloProviderConfig(endpoint))
	b.WriteString("resource \"namesilo_domain_contacts\" \"test\" {\n")
	b.WriteString("  domain = \"example.com\"\n")
	for _, name := range names {
		b.WriteString("  " + name + " = " + quoteHCL(roles[name]) + "\n")
	}
	b.WriteString("}\n")
	return b.String()
}

// seedDomainContacts adds example.com with the given associations and seeds
// the named contact profiles, so the fake has both a domain and the profiles
// the associations reference before the provider ever runs.
func seedDomainContacts(fake *fakeNamesilo, roles namesilo.ContactRoles, contactIDs ...string) {
	d := fake.addDomain("example.com")
	d.roles = roles
	for _, id := range contactIDs {
		fake.seedContact(id, namesilo.Contact{
			FirstName: "Test",
			LastName:  "Contact",
			Email:     "test@example.com",
		})
	}
}

// expectDomainContactsString checks one association attribute's applied value.
func expectDomainContactsString(attr, value string) statecheck.StateCheck {
	return statecheck.ExpectKnownValue("namesilo_domain_contacts.test",
		tfjsonpath.New(attr), knownvalue.StringExact(value))
}

// TestAccDomainContactsPartial covers §12.2 "association partial", §6.5, and
// §4 invariant 9: only the configured role is sent, the omitted roles are
// stored as computed from the API's current values, and the plan is quiet.
func TestAccDomainContactsPartial(t *testing.T) {
	requireTofu(t)
	fake := newFakeNamesilo()
	defer fake.Close()
	seedDomainContacts(fake,
		namesilo.ContactRoles{Registrant: "1001", Administrative: "1002", Technical: "1003", Billing: "1004"},
		"1001", "1002", "1003", "1004")

	config := domainContactsConfig(fake.URL(), map[string]string{"registrant": "1001"})

	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: config,
			ConfigStateChecks: []statecheck.StateCheck{
				expectDomainContactsString("id", "example.com"),
				expectDomainContactsString("registrant", "1001"),
				expectDomainContactsString("administrative", "1002"),
				expectDomainContactsString("technical", "1003"),
				expectDomainContactsString("billing", "1004"),
			},
			ConfigPlanChecks: expectEmptyAfterRefresh(),
			PostApplyFunc: func() {
				if got := fake.count("contactDomainAssociate"); got != 1 {
					t.Errorf("contactDomainAssociate called %d times, want 1", got)
				}
				last := fake.lastRequest("contactDomainAssociate")
				if !strings.Contains(last, "registrant=1001") {
					t.Errorf("the call did not carry the configured registrant: %s", last)
				}
				for _, absent := range []string{"administrative=", "technical=", "billing="} {
					if strings.Contains(last, absent) {
						t.Errorf("the call carried the unmanaged role %q: %s", absent, last)
					}
				}
				if strings.Contains(last, "test-key") {
					t.Errorf("the request log leaked the API key: %s", last)
				}
			},
		}},
	})
}

// TestAccDomainContactsUpdate covers §12.2 "association update": changing one
// configured role issues one call with that role, and the roles that are not
// configured are left untouched.
func TestAccDomainContactsUpdate(t *testing.T) {
	requireTofu(t)
	fake := newFakeNamesilo()
	defer fake.Close()
	seedDomainContacts(fake,
		namesilo.ContactRoles{Registrant: "1001", Administrative: "1002", Technical: "1003", Billing: "1004"},
		"1001", "1002", "1003", "1004")

	before := domainContactsConfig(fake.URL(), map[string]string{"registrant": "1001"})
	after := domainContactsConfig(fake.URL(), map[string]string{"registrant": "1002"})

	var baseline int
	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:            before,
				ConfigStateChecks: []statecheck.StateCheck{expectDomainContactsString("registrant", "1001")},
				ConfigPlanChecks:  expectEmptyAfterRefresh(),
				PostApplyFunc:     func() { baseline = len(fake.ops()) },
			},
			{
				Config: after,
				ConfigStateChecks: []statecheck.StateCheck{
					expectDomainContactsString("registrant", "1002"),
					expectDomainContactsString("administrative", "1002"),
					expectDomainContactsString("technical", "1003"),
					expectDomainContactsString("billing", "1004"),
				},
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("namesilo_domain_contacts.test",
							plancheck.ResourceActionUpdate),
					},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				PostApplyFunc: func() {
					step := fake.ops()[baseline:]
					if got := countOps(step, "contactDomainAssociate"); got != 1 {
						t.Errorf("the update issued %d contactDomainAssociate calls, want 1: %v", got, step)
					}
					last := fake.lastRequest("contactDomainAssociate")
					if !strings.Contains(last, "registrant=1002") {
						t.Errorf("the update call did not carry the new registrant: %s", last)
					}
					for _, absent := range []string{"administrative=", "technical=", "billing="} {
						if strings.Contains(last, absent) {
							t.Errorf("the update call carried the unmanaged role %q: %s", absent, last)
						}
					}
				},
			},
		},
	})
}

// TestAccDomainContactsAddRole covers adding a second configured role: the one
// call carries both configured roles, and the roles that stay omitted are
// untouched.
func TestAccDomainContactsAddRole(t *testing.T) {
	requireTofu(t)
	fake := newFakeNamesilo()
	defer fake.Close()
	seedDomainContacts(fake,
		namesilo.ContactRoles{Registrant: "1001"},
		"1001", "1002")

	before := domainContactsConfig(fake.URL(), map[string]string{"registrant": "1001"})
	after := domainContactsConfig(fake.URL(), map[string]string{"registrant": "1001", "administrative": "1002"})

	var baseline int
	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:            before,
				ConfigStateChecks: []statecheck.StateCheck{expectDomainContactsString("registrant", "1001")},
				ConfigPlanChecks:  expectEmptyAfterRefresh(),
				PostApplyFunc:     func() { baseline = len(fake.ops()) },
			},
			{
				Config: after,
				ConfigStateChecks: []statecheck.StateCheck{
					expectDomainContactsString("registrant", "1001"),
					expectDomainContactsString("administrative", "1002"),
				},
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("namesilo_domain_contacts.test",
							plancheck.ResourceActionUpdate),
					},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				PostApplyFunc: func() {
					step := fake.ops()[baseline:]
					if got := countOps(step, "contactDomainAssociate"); got != 1 {
						t.Errorf("adding a role issued %d contactDomainAssociate calls, want 1: %v", got, step)
					}
					last := fake.lastRequest("contactDomainAssociate")
					for _, want := range []string{"registrant=1001", "administrative=1002"} {
						if !strings.Contains(last, want) {
							t.Errorf("the add-role call is missing %q: %s", want, last)
						}
					}
				},
			},
		},
	})
}

// TestAccDomainContactsRemoveRole covers §6.5's partial-management rule and §4
// invariant 9 from the other direction: dropping a role from configuration
// makes no API call, because there is no way to clear an association; the role
// becomes computed and follows the API's current value without being managed.
func TestAccDomainContactsRemoveRole(t *testing.T) {
	requireTofu(t)
	fake := newFakeNamesilo()
	defer fake.Close()
	seedDomainContacts(fake,
		namesilo.ContactRoles{Registrant: "1001", Administrative: "1002", Technical: "1003", Billing: "1004"},
		"1001", "1002", "1003", "1004")

	before := domainContactsConfig(fake.URL(), map[string]string{"registrant": "1001", "administrative": "1002"})
	after := domainContactsConfig(fake.URL(), map[string]string{"registrant": "1001"})

	var baseline int
	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:            before,
				ConfigStateChecks: []statecheck.StateCheck{expectDomainContactsString("administrative", "1002")},
				ConfigPlanChecks:  expectEmptyAfterRefresh(),
				PostApplyFunc:     func() { baseline = len(fake.ops()) },
			},
			{
				PreConfig: func() {
					// The now-unmanaged role changes out of band. Removing it
					// from configuration must not try to undo this: the role
					// is computed and follows the API.
					fake.setRoles("example.com", namesilo.ContactRoles{
						Registrant: "1001", Administrative: "1003", Technical: "1003", Billing: "1004",
					})
				},
				Config: after,
				ConfigStateChecks: []statecheck.StateCheck{
					expectDomainContactsString("registrant", "1001"),
					expectDomainContactsString("administrative", "1003"),
				},
				ConfigPlanChecks: expectEmptyAfterRefresh(),
				PostApplyFunc: func() {
					step := fake.ops()[baseline:]
					if got := countOps(step, "contactDomainAssociate"); got != 0 {
						t.Errorf("dropping a role issued %d contactDomainAssociate calls, want 0: %v", got, step)
					}
					roles, ok := fake.rolesFor("example.com")
					if !ok {
						t.Fatal("the fake lost example.com")
					}
					if roles.Administrative != "1003" {
						t.Errorf("the unmanaged role was reassigned: administrative = %q, want %q",
							roles.Administrative, "1003")
					}
				},
			},
		},
	})
}

// TestAccDomainContactsDrift covers §12.2 "association drift": reassigning a
// managed role in the fake out of band plans an update for that role only, and
// the apply converges to the configuration.
func TestAccDomainContactsDrift(t *testing.T) {
	requireTofu(t)
	fake := newFakeNamesilo()
	defer fake.Close()
	seedDomainContacts(fake,
		namesilo.ContactRoles{Registrant: "1001", Administrative: "1002", Technical: "1003", Billing: "1004"},
		"1001", "1002", "1003", "1004")

	config := domainContactsConfig(fake.URL(), map[string]string{"registrant": "1001"})

	var baseline int
	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:            config,
				ConfigStateChecks: []statecheck.StateCheck{expectDomainContactsString("registrant", "1001")},
				ConfigPlanChecks:  expectEmptyAfterRefresh(),
				PostApplyFunc:     func() { baseline = len(fake.ops()) },
			},
			{
				PreConfig: func() {
					fake.setRoles("example.com", namesilo.ContactRoles{
						Registrant: "1003", Administrative: "1002", Technical: "1003", Billing: "1004",
					})
				},
				Config: config,
				ConfigStateChecks: []statecheck.StateCheck{
					expectDomainContactsString("registrant", "1001"),
					expectDomainContactsString("administrative", "1002"),
				},
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("namesilo_domain_contacts.test",
							plancheck.ResourceActionUpdate),
					},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				PostApplyFunc: func() {
					step := fake.ops()[baseline:]
					if got := countOps(step, "contactDomainAssociate"); got != 1 {
						t.Errorf("the drift correction issued %d contactDomainAssociate calls, want 1: %v", got, step)
					}
					last := fake.lastRequest("contactDomainAssociate")
					if !strings.Contains(last, "registrant=1001") {
						t.Errorf("the drift correction did not restore the registrant: %s", last)
					}
					for _, absent := range []string{"administrative=", "technical=", "billing="} {
						if strings.Contains(last, absent) {
							t.Errorf("the drift correction carried the unmanaged role %q: %s", absent, last)
						}
					}
				},
			},
		},
	})
}

// TestAccDomainContactsDestroy covers §12.2 "association destroy" and §4
// invariant 10: destroy makes no API call and leaves the fake's associations
// intact, because the API has no disassociate operation.
func TestAccDomainContactsDestroy(t *testing.T) {
	requireTofu(t)
	fake := newFakeNamesilo()
	defer fake.Close()
	seedDomainContacts(fake,
		namesilo.ContactRoles{Registrant: "1001", Administrative: "1002", Technical: "1003", Billing: "1004"},
		"1001", "1002", "1003", "1004")

	config := domainContactsConfig(fake.URL(), map[string]string{"registrant": "1001", "administrative": "1002"})

	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: func(_ *terraform.State) error {
			if got := fake.count("contactDomainAssociate"); got != 1 {
				return fmt.Errorf("contactDomainAssociate called %d times across the test, want 1 (the create's only)", got)
			}
			roles, ok := fake.rolesFor("example.com")
			if !ok {
				return fmt.Errorf("the fake lost example.com")
			}
			want := namesilo.ContactRoles{Registrant: "1001", Administrative: "1002", Technical: "1003", Billing: "1004"}
			if roles != want {
				return fmt.Errorf("destroy changed the associations: got %+v, want %+v", roles, want)
			}
			return nil
		},
		Steps: []resource.TestStep{{
			Config:            config,
			ConfigStateChecks: []statecheck.StateCheck{expectDomainContactsString("id", "example.com")},
			ConfigPlanChecks:  expectEmptyAfterRefresh(),
		}},
	})
}

// TestAccDomainContactsImport covers §6.5 and §10: importing by domain seeds
// id and domain, Read fills all four roles from getDomainInfo, and a settling
// step against a configuration that manages none of them is quiet.
func TestAccDomainContactsImport(t *testing.T) {
	requireTofu(t)
	fake := newFakeNamesilo()
	defer fake.Close()
	seedDomainContacts(fake,
		namesilo.ContactRoles{Registrant: "1001", Administrative: "1002", Technical: "1003", Billing: "1004"},
		"1001", "1002", "1003", "1004")

	config := domainContactsConfig(fake.URL(), map[string]string{})

	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:             config,
				ResourceName:       "namesilo_domain_contacts.test",
				ImportState:        true,
				ImportStateId:      "example.com",
				ImportStatePersist: true,
				ImportStateVerify:  false,
			},
			{
				Config: config,
				ConfigStateChecks: []statecheck.StateCheck{
					expectDomainContactsString("id", "example.com"),
					expectDomainContactsString("registrant", "1001"),
					expectDomainContactsString("administrative", "1002"),
					expectDomainContactsString("technical", "1003"),
					expectDomainContactsString("billing", "1004"),
				},
				ConfigPlanChecks: expectEmptyAfterRefresh(),
			},
		},
	})
}
