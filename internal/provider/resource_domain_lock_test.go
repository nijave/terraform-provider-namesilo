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

// domainLockConfig renders one namesilo_domain_lock block. locked is a raw HCL
// literal so a test can spell it however it needs.
func domainLockConfig(endpoint, locked string) string {
	return namesiloProviderConfig(endpoint) + `
resource "namesilo_domain_lock" "test" {
  domain = "example.com"
  locked = ` + locked + `
}
`
}

// expectLockLocked checks the applied state's locked flag.
func expectLockLocked(locked bool) statecheck.StateCheck {
	return statecheck.ExpectKnownValue("namesilo_domain_lock.test",
		tfjsonpath.New("locked"), knownvalue.Bool(locked))
}

// flipLockedPlanCheck flips the fake's registrar lock after the plan has run
// but before the apply, the way a change in NameSilo's web UI racing the apply
// would. The toggle tests use it to make the apply's call land on the real
// handler's already-in-state reply (252 for domainLock, 253 for domainUnlock)
// while the plan still proposes the change: the plan refreshes first, so a
// PreConfig flip would be picked up by the refresh and the plan would be
// empty. The reply must be tolerated, not failed on.
type flipLockedPlanCheck struct {
	fake   *fakeNamesilo
	domain string
	locked bool
}

func (c flipLockedPlanCheck) CheckPlan(context.Context, plancheck.CheckPlanRequest, *plancheck.CheckPlanResponse) {
	c.fake.setLocked(c.domain, c.locked)
}

// TestAccDomainLockCreateLocked covers §6.6 Create with locked = true: exactly
// one domainLock call for the configured domain, the domain as id, and a quiet
// plan afterwards.
func TestAccDomainLockCreateLocked(t *testing.T) {
	requireTofu(t)
	fake := newFakeNamesilo()
	defer fake.Close()
	fake.addDomain("example.com")

	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: domainLockConfig(fake.URL(), "true"),
			ConfigStateChecks: []statecheck.StateCheck{
				expectLockLocked(true),
				statecheck.ExpectKnownValue("namesilo_domain_lock.test", tfjsonpath.New("id"),
					knownvalue.StringExact("example.com")),
			},
			ConfigPlanChecks: expectEmptyAfterRefresh(),
			// PostApplyFunc runs per step, before resource.Test's automatic
			// destroy, so the log here holds exactly the create's call.
			PostApplyFunc: func() {
				if got := fake.count("domainLock"); got != 1 {
					t.Errorf("domainLock called %d times, want 1", got)
				}
				if got := fake.count("domainUnlock"); got != 0 {
					t.Errorf("domainUnlock called %d times, want 0", got)
				}
				last := fake.lastRequest("domainLock")
				if !strings.Contains(last, "domain=example.com") {
					t.Errorf("domainLock not called with the configured domain: %s", last)
				}
				if strings.Contains(last, "test-key") {
					t.Errorf("the request log leaked the API key: %s", last)
				}
			},
		}},
	})
}

// TestAccDomainLockCreateAlreadyLocked covers the §12.2 "lock create" row's
// already-locked half: the fake's domainLock answers a domain that is already
// locked with code 252, and the create must still succeed — a race with the
// web UI cannot produce a spurious failure.
func TestAccDomainLockCreateAlreadyLocked(t *testing.T) {
	requireTofu(t)
	fake := newFakeNamesilo()
	defer fake.Close()
	fake.addDomain("example.com").locked = true

	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config:            domainLockConfig(fake.URL(), "true"),
			ConfigStateChecks: []statecheck.StateCheck{expectLockLocked(true)},
			ConfigPlanChecks:  expectEmptyAfterRefresh(),
			PostApplyFunc: func() {
				if got := fake.count("domainLock"); got != 1 {
					t.Errorf("domainLock called %d times, want 1", got)
				}
			},
		}},
	})
}

// TestAccDomainLockCreateUnlocked covers §6.6 Create with locked = false: no
// lock call is made at all, and the state records locked = false — resource
// existence does not imply the domain is locked (§4 invariant 12).
func TestAccDomainLockCreateUnlocked(t *testing.T) {
	requireTofu(t)
	fake := newFakeNamesilo()
	defer fake.Close()
	fake.addDomain("example.com")

	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config:            domainLockConfig(fake.URL(), "false"),
			ConfigStateChecks: []statecheck.StateCheck{expectLockLocked(false)},
			ConfigPlanChecks:  expectEmptyAfterRefresh(),
			PostApplyFunc: func() {
				if got := fake.count("domainLock"); got != 0 {
					t.Errorf("domainLock called %d times, want 0", got)
				}
				if got := fake.count("domainUnlock"); got != 0 {
					t.Errorf("domainUnlock called %d times, want 0", got)
				}
			},
		}},
	})
}

// TestAccDomainLockToggle covers §12.2 "lock create"'s unlocked-half idempotence
// and the toggle direction changes: each issues exactly one call, and a toggle
// that lands on an already-in-state reply (252 for domainLock, 253 for
// domainUnlock) is accepted as success. The already-in-state reply is produced
// by the fake's real handlers: a PreApply plan check flips the fake's locked
// flag after the plan and before the apply, so the apply's call hits the 252
// or 253 branch while the plan still proposes the change.
func TestAccDomainLockToggle(t *testing.T) {
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
				Config:            domainLockConfig(fake.URL(), "true"),
				ConfigStateChecks: []statecheck.StateCheck{expectLockLocked(true)},
				ConfigPlanChecks:  expectEmptyAfterRefresh(),
				PostApplyFunc:     func() { baseline = len(fake.ops()) },
			},
			{
				// Unlock, racing the apply: the plan still proposes the
				// update, the fake is already unlocked, domainUnlock is
				// answered 253 and must be tolerated.
				Config:            domainLockConfig(fake.URL(), "false"),
				ConfigStateChecks: []statecheck.StateCheck{expectLockLocked(false)},
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("namesilo_domain_lock.test",
							plancheck.ResourceActionUpdate),
						flipLockedPlanCheck{fake: fake, domain: "example.com", locked: false},
					},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				PostApplyFunc: func() {
					step := fake.ops()[baseline:]
					if got := countOps(step, "domainUnlock"); got != 1 {
						t.Errorf("the unlock toggle issued %d domainUnlock calls, want 1: %v", got, step)
					}
					if got := countOps(step, "domainLock"); got != 0 {
						t.Errorf("the unlock toggle issued %d domainLock calls, want 0: %v", got, step)
					}
					baseline = len(fake.ops())
				},
			},
			{
				// Lock, racing the apply: domainLock is answered 252 and
				// must be tolerated.
				Config:            domainLockConfig(fake.URL(), "true"),
				ConfigStateChecks: []statecheck.StateCheck{expectLockLocked(true)},
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("namesilo_domain_lock.test",
							plancheck.ResourceActionUpdate),
						flipLockedPlanCheck{fake: fake, domain: "example.com", locked: true},
					},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				PostApplyFunc: func() {
					step := fake.ops()[baseline:]
					if got := countOps(step, "domainLock"); got != 1 {
						t.Errorf("the lock toggle issued %d domainLock calls, want 1: %v", got, step)
					}
					if got := countOps(step, "domainUnlock"); got != 0 {
						t.Errorf("the lock toggle issued %d domainUnlock calls, want 0: %v", got, step)
					}
				},
			},
		},
	})
}

// TestAccDomainLockDrift covers §12.2 "lock drift" in both directions: the
// fake's locked flag changes out of band, the next plan proposes an update in
// the opposite direction (the direction that restores the configuration), and
// the apply converges.
func TestAccDomainLockDrift(t *testing.T) {
	requireTofu(t)

	t.Run("lock", func(t *testing.T) {
		fake := newFakeNamesilo()
		defer fake.Close()
		fake.addDomain("example.com")

		var baseline int
		resource.Test(t, resource.TestCase{
			IsUnitTest:               true,
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:            domainLockConfig(fake.URL(), "true"),
					ConfigStateChecks: []statecheck.StateCheck{expectLockLocked(true)},
					ConfigPlanChecks:  expectEmptyAfterRefresh(),
					PostApplyFunc:     func() { baseline = len(fake.ops()) },
				},
				{
					PreConfig: func() {
						// Out-of-band change, as the web UI would make it.
						fake.setLocked("example.com", false)
					},
					Config:            domainLockConfig(fake.URL(), "true"),
					ConfigStateChecks: []statecheck.StateCheck{expectLockLocked(true)},
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{
							plancheck.ExpectResourceAction("namesilo_domain_lock.test",
								plancheck.ResourceActionUpdate),
						},
						PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
					},
					PostApplyFunc: func() {
						step := fake.ops()[baseline:]
						if got := countOps(step, "domainLock"); got != 1 {
							t.Errorf("the drift re-lock issued %d domainLock calls, want 1: %v", got, step)
						}
						if got := countOps(step, "domainUnlock"); got != 0 {
							t.Errorf("the drift re-lock issued %d domainUnlock calls, want 0: %v", got, step)
						}
					},
				},
			},
		})
	})

	t.Run("unlock", func(t *testing.T) {
		fake := newFakeNamesilo()
		defer fake.Close()
		fake.addDomain("example.com")

		var baseline int
		resource.Test(t, resource.TestCase{
			IsUnitTest:               true,
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:            domainLockConfig(fake.URL(), "false"),
					ConfigStateChecks: []statecheck.StateCheck{expectLockLocked(false)},
					ConfigPlanChecks:  expectEmptyAfterRefresh(),
					PostApplyFunc:     func() { baseline = len(fake.ops()) },
				},
				{
					PreConfig: func() {
						// Out-of-band change, as the web UI would make it.
						fake.setLocked("example.com", true)
					},
					Config:            domainLockConfig(fake.URL(), "false"),
					ConfigStateChecks: []statecheck.StateCheck{expectLockLocked(false)},
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{
							plancheck.ExpectResourceAction("namesilo_domain_lock.test",
								plancheck.ResourceActionUpdate),
						},
						PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
					},
					PostApplyFunc: func() {
						step := fake.ops()[baseline:]
						if got := countOps(step, "domainUnlock"); got != 1 {
							t.Errorf("the drift unlock issued %d domainUnlock calls, want 1: %v", got, step)
						}
						if got := countOps(step, "domainLock"); got != 0 {
							t.Errorf("the drift unlock issued %d domainLock calls, want 0: %v", got, step)
						}
					},
				},
			},
		})
	})
}

// TestAccDomainLockDestroy covers §12.2 "lock destroy" and §4 invariant 12:
// destroy calls domainUnlock exactly once when the state says locked, and
// makes no lock call at all when the resource declared locked = false.
func TestAccDomainLockDestroy(t *testing.T) {
	requireTofu(t)

	t.Run("locked", func(t *testing.T) {
		fake := newFakeNamesilo()
		defer fake.Close()
		fake.addDomain("example.com")

		resource.Test(t, resource.TestCase{
			IsUnitTest:               true,
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			// CheckDestroy runs after the destroy, so it sees the destroy's
			// own domainUnlock next to the create's domainLock.
			CheckDestroy: func(_ *terraform.State) error {
				if got := fake.count("domainUnlock"); got != 1 {
					return fmt.Errorf("destroy issued %d domainUnlock calls, want 1", got)
				}
				if got := fake.count("domainLock"); got != 1 {
					return fmt.Errorf("domainLock calls across the test: %d, want 1 (the create's only)", got)
				}
				return nil
			},
			Steps: []resource.TestStep{{
				Config:            domainLockConfig(fake.URL(), "true"),
				ConfigStateChecks: []statecheck.StateCheck{expectLockLocked(true)},
				ConfigPlanChecks:  expectEmptyAfterRefresh(),
			}},
		})
	})

	t.Run("unlocked", func(t *testing.T) {
		fake := newFakeNamesilo()
		defer fake.Close()
		fake.addDomain("example.com")

		resource.Test(t, resource.TestCase{
			IsUnitTest:               true,
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			// Destroying a resource that declared locked = false makes no
			// lock call: the domain is already unlocked (§4 invariant 12).
			CheckDestroy: func(_ *terraform.State) error {
				if got := fake.count("domainLock") + fake.count("domainUnlock"); got != 0 {
					return fmt.Errorf("a resource that declared locked = false issued %d lock calls, want 0", got)
				}
				return nil
			},
			Steps: []resource.TestStep{{
				Config:            domainLockConfig(fake.URL(), "false"),
				ConfigStateChecks: []statecheck.StateCheck{expectLockLocked(false)},
				ConfigPlanChecks:  expectEmptyAfterRefresh(),
			}},
		})
	})
}

// TestAccDomainLockImport covers §10: importing by domain seeds id and domain,
// Read fills locked from the API, and a settling apply against a matching
// configuration is quiet.
func TestAccDomainLockImport(t *testing.T) {
	requireTofu(t)

	t.Run("locked", func(t *testing.T) {
		fake := newFakeNamesilo()
		defer fake.Close()
		fake.addDomain("example.com").locked = true

		config := domainLockConfig(fake.URL(), "true")
		resource.Test(t, resource.TestCase{
			IsUnitTest:               true,
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:             config,
					ResourceName:       "namesilo_domain_lock.test",
					ImportState:        true,
					ImportStateId:      "example.com",
					ImportStatePersist: true,
					ImportStateVerify:  false,
				},
				{
					Config: config,
					ConfigStateChecks: []statecheck.StateCheck{
						expectLockLocked(true),
						statecheck.ExpectKnownValue("namesilo_domain_lock.test", tfjsonpath.New("domain"),
							knownvalue.StringExact("example.com")),
						statecheck.ExpectKnownValue("namesilo_domain_lock.test", tfjsonpath.New("id"),
							knownvalue.StringExact("example.com")),
					},
					ConfigPlanChecks: expectEmptyAfterRefresh(),
				},
			},
		})
	})

	t.Run("unlocked", func(t *testing.T) {
		fake := newFakeNamesilo()
		defer fake.Close()
		fake.addDomain("example.com")

		config := domainLockConfig(fake.URL(), "false")
		resource.Test(t, resource.TestCase{
			IsUnitTest:               true,
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:             config,
					ResourceName:       "namesilo_domain_lock.test",
					ImportState:        true,
					ImportStateId:      "example.com",
					ImportStatePersist: true,
					ImportStateVerify:  false,
				},
				{
					Config:            config,
					ConfigStateChecks: []statecheck.StateCheck{expectLockLocked(false)},
					ConfigPlanChecks:  expectEmptyAfterRefresh(),
				},
			},
		})
	})
}
