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

	"github.com/nijave/terraform-provider-namesilo/internal/namesilo"
)

// The DS records the dnssec harness tests move through the fake. Digests are
// lowercase because that is what Read writes after normalization.
var (
	dsRecordA = namesilo.DSRecord{
		KeyTag:     12345,
		Algorithm:  8,
		DigestType: 2,
		Digest:     "a94f2c81e0d5b7a3f1c6d8e2b4a6c8d0e2f4a6c8d0e2f4a6c8d0e2f4a6c8d0e2",
	}
	dsRecordB = namesilo.DSRecord{
		KeyTag:     65535,
		Algorithm:  13,
		DigestType: 1,
		Digest:     "0f1e2d3c4b5a69788796a5b4c3d2e1f0a9b8c7d6",
	}
	dsRecordC = namesilo.DSRecord{
		KeyTag:     2371,
		Algorithm:  13,
		DigestType: 2,
		Digest:     "2bb183af5f22588179a53b0a98631fad1a292118",
	}
)

// dnssecRecordsConfig renders one namesilo_dnssec_records block. records is a
// raw HCL expression so a test can spell the set however it needs.
func dnssecRecordsConfig(endpoint, records string) string {
	return namesiloProviderConfig(endpoint) + `
resource "namesilo_dnssec_records" "test" {
  domain  = "example.com"
  records = ` + records + `
}
`
}

// dsRecordHCL renders one DS record as the object literal the set expects.
func dsRecordHCL(record namesilo.DSRecord) string {
	return fmt.Sprintf(`{ key_tag = %d, algorithm = %d, digest_type = %d, digest = %q }`,
		record.KeyTag, record.Algorithm, record.DigestType, record.Digest)
}

// dsRecordsHCL renders a set of DS records as an HCL list literal.
func dsRecordsHCL(records ...namesilo.DSRecord) string {
	parts := make([]string, 0, len(records))
	for _, record := range records {
		parts = append(parts, dsRecordHCL(record))
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// dnssecRecordsUnknownDigestConfig renders a nameservers resource and a
// dnssec_records resource created in one plan, with the record's digest
// referencing the nameservers resource's id. That id is unknown during the
// first plan, so the planned records set holds a known element with an unknown
// digest attribute. The domain is deadbeef: a name that is also valid
// hexadecimal, so the id the reference resolves to satisfies the digest
// validator and the configuration settles after the apply.
func dnssecRecordsUnknownDigestConfig(endpoint string, keyTag, algorithm, digestType int64) string {
	return namesiloProviderConfig(endpoint) + `
resource "namesilo_nameservers" "test" {
  domain      = "deadbeef"
  nameservers = ["ns1.example.net", "ns2.example.net"]
}

resource "namesilo_dnssec_records" "test" {
  domain  = "deadbeef"
  records = [
    {
      key_tag     = ` + fmt.Sprintf("%d", keyTag) + `
      algorithm   = ` + fmt.Sprintf("%d", algorithm) + `
      digest_type = ` + fmt.Sprintf("%d", digestType) + `
      digest      = namesilo_nameservers.test.id
    },
  ]
}
`
}

// expectDSRecords checks the applied state's set equals the given records.
func expectDSRecords(records ...namesilo.DSRecord) statecheck.StateCheck {
	checks := make([]knownvalue.Check, 0, len(records))
	for _, record := range records {
		checks = append(checks, knownvalue.ObjectExact(map[string]knownvalue.Check{
			"key_tag":     knownvalue.Int64Exact(record.KeyTag),
			"algorithm":   knownvalue.Int64Exact(record.Algorithm),
			"digest_type": knownvalue.Int64Exact(record.DigestType),
			"digest":      knownvalue.StringExact(record.Digest),
		}))
	}
	return statecheck.ExpectKnownValue("namesilo_dnssec_records.test",
		tfjsonpath.New("records"), knownvalue.SetExact(checks))
}

// countOps counts the entries for one operation in a slice of fake log
// entries.
func countOps(ops []string, operation string) int {
	n := 0
	for _, entry := range ops {
		if opOf(entry) == operation {
			n++
		}
	}
	return n
}

// firstOpIndex is the index of the first entry for the operation, or -1.
func firstOpIndex(ops []string, operation string) int {
	for i, entry := range ops {
		if opOf(entry) == operation {
			return i
		}
	}
	return -1
}

// lastOpRequest is the most recent entry for the operation in ops, or "".
func lastOpRequest(ops []string, operation string) string {
	for i := len(ops) - 1; i >= 0; i-- {
		if opOf(ops[i]) == operation {
			return ops[i]
		}
	}
	return ""
}

// TestAccDNSSecRecordsCreateAdopt covers §12.2 "dnssec create with adopt": the
// domain may already carry DS records from before the resource existed, so
// Create lists first and only adds the missing record. The pre-existing record
// is neither re-added nor deleted.
func TestAccDNSSecRecordsCreateAdopt(t *testing.T) {
	requireTofu(t)
	fake := newFakeNamesilo()
	defer fake.Close()
	fake.addDomain("example.com").dsRecords = []namesilo.DSRecord{dsRecordA}

	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config:            dnssecRecordsConfig(fake.URL(), dsRecordsHCL(dsRecordA, dsRecordB)),
			ConfigStateChecks: []statecheck.StateCheck{expectDSRecords(dsRecordA, dsRecordB)},
			ConfigPlanChecks:  expectEmptyAfterRefresh(),
			PostApplyFunc: func() {
				if got := fake.count("dnsSecAddRecord"); got != 1 {
					t.Errorf("dnsSecAddRecord called %d times, want 1 (only the missing record is added)", got)
				}
				if got := fake.count("dnsSecDeleteRecord"); got != 0 {
					t.Errorf("dnsSecDeleteRecord called %d times, want 0 (the adopted record must not be deleted)", got)
				}
				last := fake.lastRequest("dnsSecAddRecord")
				for _, want := range []string{"keyTag=65535", "alg=13", "digestType=1", "digest=" + dsRecordB.Digest} {
					if !strings.Contains(last, want) {
						t.Errorf("dnsSecAddRecord did not carry %q: %s", want, last)
					}
				}
				if strings.Contains(last, "keyTag=12345") {
					t.Errorf("dnsSecAddRecord re-added the pre-existing record: %s", last)
				}
				if strings.Contains(last, "test-key") {
					t.Errorf("the request log leaked the API key: %s", last)
				}
			},
		}},
	})
}

// TestAccDNSSecRecordsCreateWithUnknownDigest covers the ModifyPlan guard for
// a partially-unknown planned element (§6.8). Both resources are created in one
// plan, and digest references the nameservers resource's id, which is unknown
// during the first plan. The planned records set is known and holds a known
// element — but that element's digest attribute is unknown, a shape the
// element-level IsUnknown check cannot see. ModifyPlan must leave the value
// alone: normalizing it would stamp a known empty digest over the reference,
// and the apply would publish a record with no digest instead of the real one.
func TestAccDNSSecRecordsCreateWithUnknownDigest(t *testing.T) {
	requireTofu(t)
	fake := newFakeNamesilo()
	defer fake.Close()
	// Seed the delegation so the nameservers resource's refresh has state to
	// read; the create still repoints it.
	fake.addDomain("deadbeef").nameservers = []string{"ns1.dnsowl.com", "ns2.dnsowl.com", "ns3.dnsowl.com"}

	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: dnssecRecordsUnknownDigestConfig(fake.URL(), 1, 8, 2),
			ConfigStateChecks: []statecheck.StateCheck{
				expectDSRecords(namesilo.DSRecord{KeyTag: 1, Algorithm: 8, DigestType: 2, Digest: "deadbeef"}),
			},
			ConfigPlanChecks: expectEmptyAfterRefresh(),
			PostApplyFunc: func() {
				stored := fake.dsRecordsFor("deadbeef")
				if len(stored) != 1 {
					t.Fatalf("the fake holds %d DS records, want 1: %+v", len(stored), stored)
				}
				if stored[0].Digest != "deadbeef" {
					t.Errorf("the applied record's digest is %q, want the referenced id \"deadbeef\" "+
						"(an empty digest means ModifyPlan overwrote the unknown value)", stored[0].Digest)
				}
			},
		}},
	})
}

// TestAccDNSSecRecordsUpdateKeyRoll covers §12.2 "dnssec update roll" and the
// §16 "Adds before deletes" decision. The step's own request log is isolated
// with a baseline taken after the previous step, so the first add in the slice
// is this roll's add. If the loops ran deletes first, the add would follow the
// delete and the index assertion fails.
func TestAccDNSSecRecordsUpdateKeyRoll(t *testing.T) {
	requireTofu(t)
	fake := newFakeNamesilo()
	defer fake.Close()
	fake.addDomain("example.com")

	var baseline int
	var rollOps []string
	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:            dnssecRecordsConfig(fake.URL(), dsRecordsHCL(dsRecordA)),
				ConfigStateChecks: []statecheck.StateCheck{expectDSRecords(dsRecordA)},
				ConfigPlanChecks:  expectEmptyAfterRefresh(),
				PostApplyFunc:     func() { baseline = len(fake.ops()) },
			},
			{
				Config:            dnssecRecordsConfig(fake.URL(), dsRecordsHCL(dsRecordB)),
				ConfigStateChecks: []statecheck.StateCheck{expectDSRecords(dsRecordB)},
				ConfigPlanChecks:  expectEmptyAfterRefresh(),
				PostApplyFunc:     func() { rollOps = fake.ops()[baseline:] },
			},
		},
	})

	if got := countOps(rollOps, "dnsSecAddRecord"); got != 1 {
		t.Fatalf("key roll issued %d adds, want 1: %v", got, rollOps)
	}
	if got := countOps(rollOps, "dnsSecDeleteRecord"); got != 1 {
		t.Fatalf("key roll issued %d deletes, want 1: %v", got, rollOps)
	}
	addAt := firstOpIndex(rollOps, "dnsSecAddRecord")
	deleteAt := firstOpIndex(rollOps, "dnsSecDeleteRecord")
	if addAt > deleteAt {
		t.Errorf("dnsSecAddRecord ran at index %d, after dnsSecDeleteRecord at index %d; a key roll must "+
			"publish the new DS before removing the old one (§16): %v", addAt, deleteAt, rollOps)
	}
	if last := lastOpRequest(rollOps, "dnsSecAddRecord"); !strings.Contains(last, "keyTag=65535") {
		t.Errorf("the roll added the wrong record: %s", last)
	}
	if last := lastOpRequest(rollOps, "dnsSecDeleteRecord"); !strings.Contains(last, "keyTag=12345") {
		t.Errorf("the roll deleted the wrong record: %s", last)
	}
}

// TestAccDNSSecRecordsDrift covers §12.2 "dnssec drift" and §10: a DS record
// deleted out of band plans a re-add, and one added out of band plans a
// removal. The PreApply action check proves the drift is planned before it is
// applied; the baseline-sliced PostApply counts prove only the drifted record
// moved.
func TestAccDNSSecRecordsDrift(t *testing.T) {
	requireTofu(t)
	fake := newFakeNamesilo()
	defer fake.Close()
	fake.addDomain("example.com")

	config := dnssecRecordsConfig(fake.URL(), dsRecordsHCL(dsRecordA, dsRecordB))

	var baseline int
	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:            config,
				ConfigStateChecks: []statecheck.StateCheck{expectDSRecords(dsRecordA, dsRecordB)},
				ConfigPlanChecks:  expectEmptyAfterRefresh(),
				PostApplyFunc:     func() { baseline = len(fake.ops()) },
			},
			{
				PreConfig: func() {
					// Out-of-band delete, as the web UI would make it.
					fake.setDSRecords("example.com", []namesilo.DSRecord{dsRecordB})
				},
				Config:            config,
				ConfigStateChecks: []statecheck.StateCheck{expectDSRecords(dsRecordA, dsRecordB)},
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("namesilo_dnssec_records.test", plancheck.ResourceActionUpdate),
					},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				PostApplyFunc: func() {
					step := fake.ops()[baseline:]
					if got := countOps(step, "dnsSecAddRecord"); got != 1 {
						t.Errorf("re-add of the out-of-band-deleted record issued %d adds, want 1: %v", got, step)
					}
					if got := countOps(step, "dnsSecDeleteRecord"); got != 0 {
						t.Errorf("re-add of the out-of-band-deleted record issued %d deletes, want 0: %v", got, step)
					}
					if last := lastOpRequest(step, "dnsSecAddRecord"); !strings.Contains(last, "keyTag=12345") {
						t.Errorf("the re-add carried the wrong record: %s", last)
					}
					baseline = len(fake.ops())
				},
			},
			{
				PreConfig: func() {
					// Out-of-band add, as the web UI would make it.
					fake.setDSRecords("example.com", []namesilo.DSRecord{dsRecordA, dsRecordB, dsRecordC})
				},
				Config:            config,
				ConfigStateChecks: []statecheck.StateCheck{expectDSRecords(dsRecordA, dsRecordB)},
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("namesilo_dnssec_records.test", plancheck.ResourceActionUpdate),
					},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				PostApplyFunc: func() {
					step := fake.ops()[baseline:]
					if got := countOps(step, "dnsSecDeleteRecord"); got != 1 {
						t.Errorf("removal of the out-of-band-added record issued %d deletes, want 1: %v", got, step)
					}
					if got := countOps(step, "dnsSecAddRecord"); got != 0 {
						t.Errorf("removal of the out-of-band-added record issued %d adds, want 0: %v", got, step)
					}
					if last := lastOpRequest(step, "dnsSecDeleteRecord"); !strings.Contains(last, "keyTag=2371") {
						t.Errorf("the removal deleted the wrong record: %s", last)
					}
					baseline = len(fake.ops())
				},
			},
		},
	})
}

// TestAccDNSSecRecordsEmpty covers §12.2 "dnssec empty": records = [] declares
// the domain unsigned, so the update deletes every managed record and the plan
// is quiet afterwards. An empty set must be storable, unlike nameservers,
// where the API result cannot be emptied.
func TestAccDNSSecRecordsEmpty(t *testing.T) {
	requireTofu(t)
	fake := newFakeNamesilo()
	defer fake.Close()
	fake.addDomain("example.com")

	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:            dnssecRecordsConfig(fake.URL(), dsRecordsHCL(dsRecordA, dsRecordB)),
				ConfigStateChecks: []statecheck.StateCheck{expectDSRecords(dsRecordA, dsRecordB)},
				ConfigPlanChecks:  expectEmptyAfterRefresh(),
			},
			{
				Config:            dnssecRecordsConfig(fake.URL(), "[]"),
				ConfigStateChecks: []statecheck.StateCheck{expectDSRecords()},
				ConfigPlanChecks:  expectEmptyAfterRefresh(),
				PostApplyFunc: func() {
					if got := fake.count("dnsSecDeleteRecord"); got != 2 {
						t.Errorf("records = [] issued %d deletes, want 2 (every managed record)", got)
					}
					if got := fake.count("dnsSecAddRecord"); got != 2 {
						t.Errorf("dnsSecAddRecord called %d times, want 2 (the create's adds only)", got)
					}
					if left := fake.dsRecordsFor("example.com"); len(left) != 0 {
						t.Errorf("records = [] left %+v, want no DS records", left)
					}
				},
			},
		},
	})
}

// TestAccDNSSecRecordsEmptyUnsigned covers §12.2 "dnssec empty unsigned" and
// the client's empty-slice behavior from Task 5 end to end: a domain whose
// fake returns zero ds_record elements parses as an empty set, and records = []
// is quiet. The write count stays zero across both steps, so a refresh or plan
// never invents a record to add or delete.
func TestAccDNSSecRecordsEmptyUnsigned(t *testing.T) {
	requireTofu(t)
	fake := newFakeNamesilo()
	defer fake.Close()
	// addDomain without seeding dsRecords leaves the list empty, so
	// dnsSecListRecords replies with no ds_record element.
	fake.addDomain("example.com")

	config := dnssecRecordsConfig(fake.URL(), "[]")
	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:            config,
				ConfigStateChecks: []statecheck.StateCheck{expectDSRecords()},
				ConfigPlanChecks:  expectEmptyAfterRefresh(),
			},
			{
				Config:            config,
				ConfigStateChecks: []statecheck.StateCheck{expectDSRecords()},
				ConfigPlanChecks: resource.ConfigPlanChecks{
					// A settling plan after the resource exists and the read
					// returns no records must propose nothing.
					PreApply:             []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				PostApplyFunc: func() {
					if got := fake.count("dnsSecAddRecord") + fake.count("dnsSecDeleteRecord"); got != 0 {
						t.Errorf("an unsigned domain with records = [] issued %d write calls, want 0", got)
					}
				},
			},
		},
	})
}

// TestAccDNSSecRecordsDestroy covers §4 invariant 5: destroying the resource
// removes every DS record present, disabling DNSSEC for the domain. The
// CheckDestroy runs after the destroy's own deletes, so the fake must be empty.
func TestAccDNSSecRecordsDestroy(t *testing.T) {
	requireTofu(t)
	fake := newFakeNamesilo()
	defer fake.Close()
	fake.addDomain("example.com").dsRecords = []namesilo.DSRecord{dsRecordA}

	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: func(_ *terraform.State) error {
			if left := fake.dsRecordsFor("example.com"); len(left) != 0 {
				return fmt.Errorf("destroy left DS records behind: %+v", left)
			}
			return nil
		},
		Steps: []resource.TestStep{{
			Config:            dnssecRecordsConfig(fake.URL(), dsRecordsHCL(dsRecordA, dsRecordB)),
			ConfigStateChecks: []statecheck.StateCheck{expectDSRecords(dsRecordA, dsRecordB)},
			ConfigPlanChecks:  expectEmptyAfterRefresh(),
		}},
	})
}

// TestAccDNSSecRecordsImport covers §10: importing by domain seeds id and
// domain, Read fills records, and a settling apply against a matching
// configuration is quiet. The empty case imports a domain with no DS records,
// which produces records = [], a valid configuration.
func TestAccDNSSecRecordsImport(t *testing.T) {
	requireTofu(t)

	t.Run("non-empty", func(t *testing.T) {
		fake := newFakeNamesilo()
		defer fake.Close()
		fake.addDomain("example.com").dsRecords = []namesilo.DSRecord{dsRecordA, dsRecordB}

		config := dnssecRecordsConfig(fake.URL(), dsRecordsHCL(dsRecordA, dsRecordB))
		resource.Test(t, resource.TestCase{
			IsUnitTest:               true,
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:             config,
					ResourceName:       "namesilo_dnssec_records.test",
					ImportState:        true,
					ImportStateId:      "example.com",
					ImportStatePersist: true,
					ImportStateVerify:  false,
				},
				{
					Config:            config,
					ConfigStateChecks: []statecheck.StateCheck{expectDSRecords(dsRecordA, dsRecordB)},
					ConfigPlanChecks:  expectEmptyAfterRefresh(),
				},
			},
		})
	})

	t.Run("empty", func(t *testing.T) {
		fake := newFakeNamesilo()
		defer fake.Close()
		fake.addDomain("example.com")

		config := dnssecRecordsConfig(fake.URL(), "[]")
		resource.Test(t, resource.TestCase{
			IsUnitTest:               true,
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:             config,
					ResourceName:       "namesilo_dnssec_records.test",
					ImportState:        true,
					ImportStateId:      "example.com",
					ImportStatePersist: true,
					ImportStateVerify:  false,
				},
				{
					Config:            config,
					ConfigStateChecks: []statecheck.StateCheck{expectDSRecords()},
					ConfigPlanChecks:  expectEmptyAfterRefresh(),
				},
			},
		})
	})
}
