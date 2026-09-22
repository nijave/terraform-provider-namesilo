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

// dnssecRecordsDataSourceConfig renders one namesilo_dnssec_records data block.
func dnssecRecordsDataSourceConfig(endpoint, domain string) string {
	return namesiloProviderConfig(endpoint) + `
data "namesilo_dnssec_records" "test" {
  domain = "` + domain + `"
}
`
}

// expectDSRecordsDataSource checks the data source's set equals the given
// records, normalized (lowercased digests).
func expectDSRecordsDataSource(records ...namesilo.DSRecord) statecheck.StateCheck {
	checks := make([]knownvalue.Check, 0, len(records))
	for _, record := range records {
		checks = append(checks, knownvalue.ObjectExact(map[string]knownvalue.Check{
			"key_tag":     knownvalue.Int64Exact(record.KeyTag),
			"algorithm":   knownvalue.Int64Exact(record.Algorithm),
			"digest_type": knownvalue.Int64Exact(record.DigestType),
			"digest":      knownvalue.StringExact(record.Digest),
		}))
	}
	return statecheck.ExpectKnownValue("data.namesilo_dnssec_records.test",
		tfjsonpath.New("records"), knownvalue.SetExact(checks))
}

// TestAccDNSSecRecordsDataSource covers §12.2 "data sources" for the DS read:
// the fake seeds one lowercase and one uppercase digest and the data source
// returns both as the same four-field objects the resource stores, digests
// lowercased.
func TestAccDNSSecRecordsDataSource(t *testing.T) {
	requireTofu(t)
	fake := newFakeNamesilo()
	defer fake.Close()
	upperDigest := namesilo.DSRecord{
		KeyTag:     2371,
		Algorithm:  13,
		DigestType: 2,
		Digest:     "2BB183AF5F22588179A53B0A98631FAD1A292118",
	}
	fake.addDomain("example.com").dsRecords = []namesilo.DSRecord{dsRecordA, upperDigest}

	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: dnssecRecordsDataSourceConfig(fake.URL(), "example.com"),
			ConfigStateChecks: []statecheck.StateCheck{
				expectDSRecordsDataSource(
					dsRecordA,
					namesilo.DSRecord{
						KeyTag:     upperDigest.KeyTag,
						Algorithm:  upperDigest.Algorithm,
						DigestType: upperDigest.DigestType,
						Digest:     strings.ToLower(upperDigest.Digest),
					},
				),
				statecheck.ExpectKnownValue("data.namesilo_dnssec_records.test",
					tfjsonpath.New("id"), knownvalue.StringExact("example.com")),
			},
			PostApplyFunc: func() {
				if last := fake.lastRequest("dnsSecListRecords"); strings.Contains(last, "test-key") {
					t.Errorf("the request log leaked the API key: %s", last)
				}
			},
		}},
	})
}
