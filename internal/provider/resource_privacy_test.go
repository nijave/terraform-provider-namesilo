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

// privacyConfig renders one namesilo_privacy block. enabled is a raw HCL
// literal so a test can spell it however it needs.
func privacyConfig(endpoint, enabled string) string {
	return namesiloProviderConfig(endpoint) + `
resource "namesilo_privacy" "test" {
  domain  = "example.com"
  enabled = ` + enabled + `
}
`
}

// expectPrivacyEnabled checks the applied state's enabled flag.
func expectPrivacyEnabled(enabled bool) statecheck.StateCheck {
	return statecheck.ExpectKnownValue("namesilo_privacy.test",
		tfjsonpath.New("enabled"), knownvalue.Bool(enabled))
}

// flipPrivatePlanCheck flips the fake's WHOIS privacy after the plan has run
// but before the apply, the way a change in NameSilo's web UI racing the apply
// would. The toggle tests use it to make the apply's call land on the real
// handler's already-in-state reply (255 for addPrivacy, 256 for removePrivacy)
// while the plan still proposes the change: the plan refreshes first, so a
// PreConfig flip would be picked up by the refresh and the plan would be
// empty. The reply must be tolerated, not failed on.
type flipPrivatePlanCheck struct {
	fake    *fakeNamesilo
	domain  string
	private bool
}

func (c flipPrivatePlanCheck) CheckPlan(context.Context, plancheck.CheckPlanRequest, *plancheck.CheckPlanResponse) {
	c.fake.setPrivate(c.domain, c.private)
}

// TestAccPrivacyCreateEnabled covers §6.3 Create with enabled = true: exactly
// one addPrivacy call for the configured domain, the domain as id, and a quiet
// plan afterwards.
func TestAccPrivacyCreateEnabled(t *testing.T) {
	requireTofu(t)
	fake := newFakeNamesilo()
	defer fake.Close()
	fake.addDomain("example.com")

	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: privacyConfig(fake.URL(), "true"),
			ConfigStateChecks: []statecheck.StateCheck{
				expectPrivacyEnabled(true),
				statecheck.ExpectKnownValue("namesilo_privacy.test", tfjsonpath.New("id"),
					knownvalue.StringExact("example.com")),
			},
			ConfigPlanChecks: expectEmptyAfterRefresh(),
			// PostApplyFunc runs per step, before resource.Test's automatic
			// destroy, so the log here holds exactly the create's call.
			PostApplyFunc: func() {
				if got := fake.count("addPrivacy"); got != 1 {
					t.Errorf("addPrivacy called %d times, want 1", got)
				}
				if got := fake.count("removePrivacy"); got != 0 {
					t.Errorf("removePrivacy called %d times, want 0", got)
				}
				last := fake.lastRequest("addPrivacy")
				if !strings.Contains(last, "domain=example.com") {
					t.Errorf("addPrivacy not called with the configured domain: %s", last)
				}
				if strings.Contains(last, "test-key") {
					t.Errorf("the request log leaked the API key: %s", last)
				}
			},
		}},
	})
}

// TestAccPrivacyCreateAlreadyPrivate covers the §12.2 "privacy create enabled"
// row's already-private half: the fake's addPrivacy answers a domain that is
// already private with code 255, and the create must still succeed — a race
// with the web UI cannot produce a spurious failure.
func TestAccPrivacyCreateAlreadyPrivate(t *testing.T) {
	requireTofu(t)
	fake := newFakeNamesilo()
	defer fake.Close()
	fake.addDomain("example.com").private = true

	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config:            privacyConfig(fake.URL(), "true"),
			ConfigStateChecks: []statecheck.StateCheck{expectPrivacyEnabled(true)},
			ConfigPlanChecks:  expectEmptyAfterRefresh(),
			PostApplyFunc: func() {
				if got := fake.count("addPrivacy"); got != 1 {
					t.Errorf("addPrivacy called %d times, want 1", got)
				}
			},
		}},
	})
}

// TestAccPrivacyCreateDisabled covers §6.3 Create with enabled = false: no
// privacy call is made at all, and the state records enabled = false —
// resource existence does not imply privacy is on.
func TestAccPrivacyCreateDisabled(t *testing.T) {
	requireTofu(t)
	fake := newFakeNamesilo()
	defer fake.Close()
	fake.addDomain("example.com")

	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config:            privacyConfig(fake.URL(), "false"),
			ConfigStateChecks: []statecheck.StateCheck{expectPrivacyEnabled(false)},
			ConfigPlanChecks:  expectEmptyAfterRefresh(),
			PostApplyFunc: func() {
				if got := fake.count("addPrivacy"); got != 0 {
					t.Errorf("addPrivacy called %d times, want 0", got)
				}
				if got := fake.count("removePrivacy"); got != 0 {
					t.Errorf("removePrivacy called %d times, want 0", got)
				}
			},
		}},
	})
}

// TestAccPrivacyToggle covers §12.2 "privacy toggle": each direction change
// issues exactly one call, and a toggle that lands on an already-in-state
// reply is accepted as success. The already-in-state reply is produced by the
// fake's real handlers: a PreApply plan check flips the fake's private flag
// after the plan and before the apply, so the apply's call hits the 255 or 256
// branch while the plan still proposes the change.
func TestAccPrivacyToggle(t *testing.T) {
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
				Config:            privacyConfig(fake.URL(), "true"),
				ConfigStateChecks: []statecheck.StateCheck{expectPrivacyEnabled(true)},
				ConfigPlanChecks:  expectEmptyAfterRefresh(),
				PostApplyFunc:     func() { baseline = len(fake.ops()) },
			},
			{
				// Disable, racing the apply: the plan still proposes the
				// update, the fake is already not private, removePrivacy is
				// answered 256 and must be tolerated.
				Config:            privacyConfig(fake.URL(), "false"),
				ConfigStateChecks: []statecheck.StateCheck{expectPrivacyEnabled(false)},
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("namesilo_privacy.test",
							plancheck.ResourceActionUpdate),
						flipPrivatePlanCheck{fake: fake, domain: "example.com", private: false},
					},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				PostApplyFunc: func() {
					step := fake.ops()[baseline:]
					if got := countOps(step, "removePrivacy"); got != 1 {
						t.Errorf("the disable toggle issued %d removePrivacy calls, want 1: %v", got, step)
					}
					if got := countOps(step, "addPrivacy"); got != 0 {
						t.Errorf("the disable toggle issued %d addPrivacy calls, want 0: %v", got, step)
					}
					baseline = len(fake.ops())
				},
			},
			{
				// Enable, racing the apply: addPrivacy is answered 255 and
				// must be tolerated.
				Config:            privacyConfig(fake.URL(), "true"),
				ConfigStateChecks: []statecheck.StateCheck{expectPrivacyEnabled(true)},
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("namesilo_privacy.test",
							plancheck.ResourceActionUpdate),
						flipPrivatePlanCheck{fake: fake, domain: "example.com", private: true},
					},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				PostApplyFunc: func() {
					step := fake.ops()[baseline:]
					if got := countOps(step, "addPrivacy"); got != 1 {
						t.Errorf("the enable toggle issued %d addPrivacy calls, want 1: %v", got, step)
					}
					if got := countOps(step, "removePrivacy"); got != 0 {
						t.Errorf("the enable toggle issued %d removePrivacy calls, want 0: %v", got, step)
					}
				},
			},
		},
	})
}

// TestAccPrivacyDrift covers §12.2 "privacy drift" in both directions: the
// fake's private flag changes out of band, the next plan proposes an update in
// the opposite direction (the direction that restores the configuration), and
// the apply converges.
func TestAccPrivacyDrift(t *testing.T) {
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
					Config:            privacyConfig(fake.URL(), "true"),
					ConfigStateChecks: []statecheck.StateCheck{expectPrivacyEnabled(true)},
					ConfigPlanChecks:  expectEmptyAfterRefresh(),
					PostApplyFunc:     func() { baseline = len(fake.ops()) },
				},
				{
					PreConfig: func() {
						// Out-of-band change, as the web UI would make it.
						fake.setPrivate("example.com", false)
					},
					Config:            privacyConfig(fake.URL(), "true"),
					ConfigStateChecks: []statecheck.StateCheck{expectPrivacyEnabled(true)},
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{
							plancheck.ExpectResourceAction("namesilo_privacy.test",
								plancheck.ResourceActionUpdate),
						},
						PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
					},
					PostApplyFunc: func() {
						step := fake.ops()[baseline:]
						if got := countOps(step, "addPrivacy"); got != 1 {
							t.Errorf("the drift re-enable issued %d addPrivacy calls, want 1: %v", got, step)
						}
						if got := countOps(step, "removePrivacy"); got != 0 {
							t.Errorf("the drift re-enable issued %d removePrivacy calls, want 0: %v", got, step)
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
					Config:            privacyConfig(fake.URL(), "false"),
					ConfigStateChecks: []statecheck.StateCheck{expectPrivacyEnabled(false)},
					ConfigPlanChecks:  expectEmptyAfterRefresh(),
					PostApplyFunc:     func() { baseline = len(fake.ops()) },
				},
				{
					PreConfig: func() {
						// Out-of-band change, as the web UI would make it.
						fake.setPrivate("example.com", true)
					},
					Config:            privacyConfig(fake.URL(), "false"),
					ConfigStateChecks: []statecheck.StateCheck{expectPrivacyEnabled(false)},
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{
							plancheck.ExpectResourceAction("namesilo_privacy.test",
								plancheck.ResourceActionUpdate),
						},
						PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
					},
					PostApplyFunc: func() {
						step := fake.ops()[baseline:]
						if got := countOps(step, "removePrivacy"); got != 1 {
							t.Errorf("the drift disable issued %d removePrivacy calls, want 1: %v", got, step)
						}
						if got := countOps(step, "addPrivacy"); got != 0 {
							t.Errorf("the drift disable issued %d addPrivacy calls, want 0: %v", got, step)
						}
					},
				},
			},
		})
	})
}

// TestAccPrivacyDestroy covers §12.2 "privacy destroy" and §4 invariants 6 and
// 7: destroy calls removePrivacy exactly once when the state says enabled, and
// makes no privacy call at all when the resource declared enabled = false.
func TestAccPrivacyDestroy(t *testing.T) {
	requireTofu(t)

	t.Run("enabled", func(t *testing.T) {
		fake := newFakeNamesilo()
		defer fake.Close()
		fake.addDomain("example.com")

		resource.Test(t, resource.TestCase{
			IsUnitTest:               true,
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			// CheckDestroy runs after the destroy, so it sees the destroy's
			// own removePrivacy next to the create's addPrivacy.
			CheckDestroy: func(_ *terraform.State) error {
				if got := fake.count("removePrivacy"); got != 1 {
					return fmt.Errorf("destroy issued %d removePrivacy calls, want 1", got)
				}
				if got := fake.count("addPrivacy"); got != 1 {
					return fmt.Errorf("addPrivacy calls across the test: %d, want 1 (the create's only)", got)
				}
				return nil
			},
			Steps: []resource.TestStep{{
				Config:            privacyConfig(fake.URL(), "true"),
				ConfigStateChecks: []statecheck.StateCheck{expectPrivacyEnabled(true)},
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
			// privacy call: the domain is already unprivate (§4 invariant 7).
			CheckDestroy: func(_ *terraform.State) error {
				if got := fake.count("addPrivacy") + fake.count("removePrivacy"); got != 0 {
					return fmt.Errorf("a resource that declared enabled = false issued %d privacy calls, want 0", got)
				}
				return nil
			},
			Steps: []resource.TestStep{{
				Config:            privacyConfig(fake.URL(), "false"),
				ConfigStateChecks: []statecheck.StateCheck{expectPrivacyEnabled(false)},
				ConfigPlanChecks:  expectEmptyAfterRefresh(),
			}},
		})
	})
}

// TestAccPrivacyImport covers §10: importing by domain seeds id and domain,
// Read fills enabled from the API, and a settling apply against a matching
// configuration is quiet.
func TestAccPrivacyImport(t *testing.T) {
	requireTofu(t)

	t.Run("private", func(t *testing.T) {
		fake := newFakeNamesilo()
		defer fake.Close()
		fake.addDomain("example.com").private = true

		config := privacyConfig(fake.URL(), "true")
		resource.Test(t, resource.TestCase{
			IsUnitTest:               true,
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:             config,
					ResourceName:       "namesilo_privacy.test",
					ImportState:        true,
					ImportStateId:      "example.com",
					ImportStatePersist: true,
					ImportStateVerify:  false,
				},
				{
					Config: config,
					ConfigStateChecks: []statecheck.StateCheck{
						expectPrivacyEnabled(true),
						statecheck.ExpectKnownValue("namesilo_privacy.test", tfjsonpath.New("domain"),
							knownvalue.StringExact("example.com")),
						statecheck.ExpectKnownValue("namesilo_privacy.test", tfjsonpath.New("id"),
							knownvalue.StringExact("example.com")),
					},
					ConfigPlanChecks: expectEmptyAfterRefresh(),
				},
			},
		})
	})

	t.Run("not-private", func(t *testing.T) {
		fake := newFakeNamesilo()
		defer fake.Close()
		fake.addDomain("example.com")

		config := privacyConfig(fake.URL(), "false")
		resource.Test(t, resource.TestCase{
			IsUnitTest:               true,
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:             config,
					ResourceName:       "namesilo_privacy.test",
					ImportState:        true,
					ImportStateId:      "example.com",
					ImportStatePersist: true,
					ImportStateVerify:  false,
				},
				{
					Config:            config,
					ConfigStateChecks: []statecheck.StateCheck{expectPrivacyEnabled(false)},
					ConfigPlanChecks:  expectEmptyAfterRefresh(),
				},
			},
		})
	})
}
