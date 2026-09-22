// SPDX-License-Identifier: GPL-3.0-or-later

package provider_test

import (
	"context"
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

// autoRenewConfig renders one namesilo_auto_renew block. enabled is a raw HCL
// literal so a test can spell it however it needs.
func autoRenewConfig(endpoint, enabled string) string {
	return namesiloProviderConfig(endpoint) + `
resource "namesilo_auto_renew" "test" {
  domain  = "example.com"
  enabled = ` + enabled + `
}
`
}

// expectAutoRenewEnabled checks the applied state's enabled flag.
func expectAutoRenewEnabled(enabled bool) statecheck.StateCheck {
	return statecheck.ExpectKnownValue("namesilo_auto_renew.test",
		tfjsonpath.New("enabled"), knownvalue.Bool(enabled))
}

// flipAutoRenewPlanCheck flips the fake's auto-renew flag after the plan has
// run but before the apply, the way a change in NameSilo's web UI racing the
// apply would. The toggle tests use it to make the apply's call land on the
// real handler's already-in-state reply (250 for addAutoRenewal, 251 for
// removeAutoRenewal) while the plan still proposes the change: the plan
// refreshes first, so a PreConfig flip would be picked up by the refresh and
// the plan would be empty. The reply must be tolerated, not failed on.
type flipAutoRenewPlanCheck struct {
	fake    *fakeNamesilo
	domain  string
	enabled bool
}

func (c flipAutoRenewPlanCheck) CheckPlan(context.Context, plancheck.CheckPlanRequest, *plancheck.CheckPlanResponse) {
	c.fake.setAutoRenew(c.domain, c.enabled)
}

// TestAccAutoRenewCreateEnabled covers §6.7 Create with enabled = true:
// exactly one addAutoRenewal call for the configured domain, the domain as id,
// and a quiet plan afterwards.
func TestAccAutoRenewCreateEnabled(t *testing.T) {
	requireTofu(t)
	fake := newFakeNamesilo()
	defer fake.Close()
	fake.addDomain("example.com")

	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: autoRenewConfig(fake.URL(), "true"),
			ConfigStateChecks: []statecheck.StateCheck{
				expectAutoRenewEnabled(true),
				statecheck.ExpectKnownValue("namesilo_auto_renew.test", tfjsonpath.New("id"),
					knownvalue.StringExact("example.com")),
			},
			ConfigPlanChecks: expectEmptyAfterRefresh(),
			// PostApplyFunc runs per step, before resource.Test's automatic
			// destroy, so the log here holds exactly the create's call.
			PostApplyFunc: func() {
				if got := fake.count("addAutoRenewal"); got != 1 {
					t.Errorf("addAutoRenewal called %d times, want 1", got)
				}
				if got := fake.count("removeAutoRenewal"); got != 0 {
					t.Errorf("removeAutoRenewal called %d times, want 0", got)
				}
				last := fake.lastRequest("addAutoRenewal")
				if !strings.Contains(last, "domain=example.com") {
					t.Errorf("addAutoRenewal not called with the configured domain: %s", last)
				}
				if strings.Contains(last, "test-key") {
					t.Errorf("the request log leaked the API key: %s", last)
				}
			},
		}},
	})
}

// TestAccAutoRenewCreateAlreadyEnabled covers the §12.2 "auto-renew create"
// row's already-enabled half: the fake's addAutoRenewal answers a domain that
// already has auto-renew with code 250, and the create must still succeed — a
// race with the web UI cannot produce a spurious failure.
func TestAccAutoRenewCreateAlreadyEnabled(t *testing.T) {
	requireTofu(t)
	fake := newFakeNamesilo()
	defer fake.Close()
	fake.addDomain("example.com").autoRenew = true

	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config:            autoRenewConfig(fake.URL(), "true"),
			ConfigStateChecks: []statecheck.StateCheck{expectAutoRenewEnabled(true)},
			ConfigPlanChecks:  expectEmptyAfterRefresh(),
			PostApplyFunc: func() {
				if got := fake.count("addAutoRenewal"); got != 1 {
					t.Errorf("addAutoRenewal called %d times, want 1", got)
				}
			},
		}},
	})
}

// TestAccAutoRenewCreateDisabled covers §6.7 Create with enabled = false: no
// auto-renew call is made at all, and the state records enabled = false —
// resource existence does not imply auto-renew is on (§4 invariant 14).
func TestAccAutoRenewCreateDisabled(t *testing.T) {
	requireTofu(t)
	fake := newFakeNamesilo()
	defer fake.Close()
	fake.addDomain("example.com")

	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config:            autoRenewConfig(fake.URL(), "false"),
			ConfigStateChecks: []statecheck.StateCheck{expectAutoRenewEnabled(false)},
			ConfigPlanChecks:  expectEmptyAfterRefresh(),
			PostApplyFunc: func() {
				if got := fake.count("addAutoRenewal"); got != 0 {
					t.Errorf("addAutoRenewal called %d times, want 0", got)
				}
				if got := fake.count("removeAutoRenewal"); got != 0 {
					t.Errorf("removeAutoRenewal called %d times, want 0", got)
				}
			},
		}},
	})
}

// TestAccAutoRenewToggle covers the toggle direction changes: each issues
// exactly one call, and a toggle that lands on an already-in-state reply (250
// for addAutoRenewal, 251 for removeAutoRenewal) is accepted as success. The
// already-in-state reply is produced by the fake's real handlers: a PreApply
// plan check flips the fake's auto_renew flag after the plan and before the
// apply, so the apply's call hits the 250 or 251 branch while the plan still
// proposes the change.
func TestAccAutoRenewToggle(t *testing.T) {
	requireTofu(t)
	fake := newFakeNamesilo()
	defer fake.Close()
	fake.addDomain("example.com")

	var baseline int
	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:            autoRenewConfig(fake.URL(), "true"),
				ConfigStateChecks: []statecheck.StateCheck{expectAutoRenewEnabled(true)},
				ConfigPlanChecks:  expectEmptyAfterRefresh(),
				PostApplyFunc:     func() { baseline = len(fake.ops()) },
			},
			{
				// Disable, racing the apply: the plan still proposes the
				// update, the fake is already not auto-renewing,
				// removeAutoRenewal is answered 251 and must be tolerated.
				Config:            autoRenewConfig(fake.URL(), "false"),
				ConfigStateChecks: []statecheck.StateCheck{expectAutoRenewEnabled(false)},
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("namesilo_auto_renew.test",
							plancheck.ResourceActionUpdate),
						flipAutoRenewPlanCheck{fake: fake, domain: "example.com", enabled: false},
					},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				PostApplyFunc: func() {
					step := fake.ops()[baseline:]
					if got := countOps(step, "removeAutoRenewal"); got != 1 {
						t.Errorf("the disable toggle issued %d removeAutoRenewal calls, want 1: %v", got, step)
					}
					if got := countOps(step, "addAutoRenewal"); got != 0 {
						t.Errorf("the disable toggle issued %d addAutoRenewal calls, want 0: %v", got, step)
					}
					baseline = len(fake.ops())
				},
			},
			{
				// Enable, racing the apply: addAutoRenewal is answered 250
				// and must be tolerated.
				Config:            autoRenewConfig(fake.URL(), "true"),
				ConfigStateChecks: []statecheck.StateCheck{expectAutoRenewEnabled(true)},
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("namesilo_auto_renew.test",
							plancheck.ResourceActionUpdate),
						flipAutoRenewPlanCheck{fake: fake, domain: "example.com", enabled: true},
					},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				PostApplyFunc: func() {
					step := fake.ops()[baseline:]
					if got := countOps(step, "addAutoRenewal"); got != 1 {
						t.Errorf("the enable toggle issued %d addAutoRenewal calls, want 1: %v", got, step)
					}
					if got := countOps(step, "removeAutoRenewal"); got != 0 {
						t.Errorf("the enable toggle issued %d removeAutoRenewal calls, want 0: %v", got, step)
					}
				},
			},
		},
	})
}

// TestAccAutoRenewDrift covers §12.2 "auto-renew drift" in both directions:
// the fake's auto_renew flag changes out of band, the next plan proposes an
// update in the opposite direction (the direction that restores the
// configuration), and the apply converges.
func TestAccAutoRenewDrift(t *testing.T) {
	requireTofu(t)

	t.Run("re-enable", func(t *testing.T) {
		fake := newFakeNamesilo()
		defer fake.Close()
		fake.addDomain("example.com")

		var baseline int
		resource.Test(t, resource.TestCase{
			IsUnitTest:               true,
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:            autoRenewConfig(fake.URL(), "true"),
					ConfigStateChecks: []statecheck.StateCheck{expectAutoRenewEnabled(true)},
					ConfigPlanChecks:  expectEmptyAfterRefresh(),
					PostApplyFunc:     func() { baseline = len(fake.ops()) },
				},
				{
					PreConfig: func() {
						// Out-of-band change, as the web UI would make it.
						fake.setAutoRenew("example.com", false)
					},
					Config:            autoRenewConfig(fake.URL(), "true"),
					ConfigStateChecks: []statecheck.StateCheck{expectAutoRenewEnabled(true)},
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{
							plancheck.ExpectResourceAction("namesilo_auto_renew.test",
								plancheck.ResourceActionUpdate),
						},
						PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
					},
					PostApplyFunc: func() {
						step := fake.ops()[baseline:]
						if got := countOps(step, "addAutoRenewal"); got != 1 {
							t.Errorf("the drift re-enable issued %d addAutoRenewal calls, want 1: %v", got, step)
						}
						if got := countOps(step, "removeAutoRenewal"); got != 0 {
							t.Errorf("the drift re-enable issued %d removeAutoRenewal calls, want 0: %v", got, step)
						}
					},
				},
			},
		})
	})

	t.Run("disable", func(t *testing.T) {
		fake := newFakeNamesilo()
		defer fake.Close()
		fake.addDomain("example.com")

		var baseline int
		resource.Test(t, resource.TestCase{
			IsUnitTest:               true,
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:            autoRenewConfig(fake.URL(), "false"),
					ConfigStateChecks: []statecheck.StateCheck{expectAutoRenewEnabled(false)},
					ConfigPlanChecks:  expectEmptyAfterRefresh(),
					PostApplyFunc:     func() { baseline = len(fake.ops()) },
				},
				{
					PreConfig: func() {
						// Out-of-band change, as the web UI would make it.
						fake.setAutoRenew("example.com", true)
					},
					Config:            autoRenewConfig(fake.URL(), "false"),
					ConfigStateChecks: []statecheck.StateCheck{expectAutoRenewEnabled(false)},
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{
							plancheck.ExpectResourceAction("namesilo_auto_renew.test",
								plancheck.ResourceActionUpdate),
						},
						PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
					},
					PostApplyFunc: func() {
						step := fake.ops()[baseline:]
						if got := countOps(step, "removeAutoRenewal"); got != 1 {
							t.Errorf("the drift disable issued %d removeAutoRenewal calls, want 1: %v", got, step)
						}
						if got := countOps(step, "addAutoRenewal"); got != 0 {
							t.Errorf("the drift disable issued %d addAutoRenewal calls, want 0: %v", got, step)
						}
					},
				},
			},
		})
	})
}

// TestAccAutoRenewDestroy covers §12.2 "auto-renew destroy" and §4 invariant
// 15: destroy calls removeAutoRenewal exactly once when the state says
// enabled, and makes no auto-renew call at all when the resource declared
// enabled = false.
func TestAccAutoRenewDestroy(t *testing.T) {
	requireTofu(t)

	t.Run("enabled", func(t *testing.T) {
		fake := newFakeNamesilo()
		defer fake.Close()
		fake.addDomain("example.com")

		resource.Test(t, resource.TestCase{
			IsUnitTest:               true,
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			// CheckDestroy runs after the destroy, so it sees the destroy's
			// own removeAutoRenewal next to the create's addAutoRenewal.
			CheckDestroy: func(_ *terraform.State) error {
				if got := fake.count("removeAutoRenewal"); got != 1 {
					return fmt.Errorf("destroy issued %d removeAutoRenewal calls, want 1", got)
				}
				if got := fake.count("addAutoRenewal"); got != 1 {
					return fmt.Errorf("addAutoRenewal calls across the test: %d, want 1 (the create's only)", got)
				}
				return nil
			},
			Steps: []resource.TestStep{{
				Config:            autoRenewConfig(fake.URL(), "true"),
				ConfigStateChecks: []statecheck.StateCheck{expectAutoRenewEnabled(true)},
				ConfigPlanChecks:  expectEmptyAfterRefresh(),
			}},
		})
	})

	t.Run("disabled", func(t *testing.T) {
		fake := newFakeNamesilo()
		defer fake.Close()
		fake.addDomain("example.com")

		resource.Test(t, resource.TestCase{
			IsUnitTest:               true,
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			// Destroying a resource that declared enabled = false makes no
			// auto-renew call: the domain is already not auto-renewing
			// (§4 invariant 15).
			CheckDestroy: func(_ *terraform.State) error {
				if got := fake.count("addAutoRenewal") + fake.count("removeAutoRenewal"); got != 0 {
					return fmt.Errorf("a resource that declared enabled = false issued %d auto-renew calls, want 0", got)
				}
				return nil
			},
			Steps: []resource.TestStep{{
				Config:            autoRenewConfig(fake.URL(), "false"),
				ConfigStateChecks: []statecheck.StateCheck{expectAutoRenewEnabled(false)},
				ConfigPlanChecks:  expectEmptyAfterRefresh(),
			}},
		})
	})
}

// TestAccAutoRenewImport covers §10: importing by domain seeds id and domain,
// Read fills enabled from the API, and a settling apply against a matching
// configuration is quiet.
func TestAccAutoRenewImport(t *testing.T) {
	requireTofu(t)

	t.Run("enabled", func(t *testing.T) {
		fake := newFakeNamesilo()
		defer fake.Close()
		fake.addDomain("example.com").autoRenew = true

		config := autoRenewConfig(fake.URL(), "true")
		resource.Test(t, resource.TestCase{
			IsUnitTest:               true,
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:             config,
					ResourceName:       "namesilo_auto_renew.test",
					ImportState:        true,
					ImportStateId:      "example.com",
					ImportStatePersist: true,
					ImportStateVerify:  false,
				},
				{
					Config: config,
					ConfigStateChecks: []statecheck.StateCheck{
						expectAutoRenewEnabled(true),
						statecheck.ExpectKnownValue("namesilo_auto_renew.test", tfjsonpath.New("domain"),
							knownvalue.StringExact("example.com")),
						statecheck.ExpectKnownValue("namesilo_auto_renew.test", tfjsonpath.New("id"),
							knownvalue.StringExact("example.com")),
					},
					ConfigPlanChecks: expectEmptyAfterRefresh(),
				},
			},
		})
	})

	t.Run("disabled", func(t *testing.T) {
		fake := newFakeNamesilo()
		defer fake.Close()
		fake.addDomain("example.com")

		config := autoRenewConfig(fake.URL(), "false")
		resource.Test(t, resource.TestCase{
			IsUnitTest:               true,
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:             config,
					ResourceName:       "namesilo_auto_renew.test",
					ImportState:        true,
					ImportStateId:      "example.com",
					ImportStatePersist: true,
					ImportStateVerify:  false,
				},
				{
					Config:            config,
					ConfigStateChecks: []statecheck.StateCheck{expectAutoRenewEnabled(false)},
					ConfigPlanChecks:  expectEmptyAfterRefresh(),
				},
			},
		})
	})
}
