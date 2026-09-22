// SPDX-License-Identifier: GPL-3.0-or-later

package provider_test

import (
	"context"
	"fmt"
	"math/big"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/nijave/terraform-provider-namesilo/internal/provider"
)

// domainsPerPage is the page size the domains tests configure. It must be one
// of the values the provider schema allows (§5), so the tests seed more fake
// domains than one page holds instead of shrinking the page size below 20.
const domainsPerPage = 20

// domainsTotal is the size of the fake's domain set for the domains tests.
const domainsTotal = 25

// domainSummaryType is the tftypes object type of one namesilo_domains set
// element, spelled the way the framework builds it from the schema.
var domainSummaryType = tftypes.Object{
	AttributeTypes: map[string]tftypes.Type{
		"name":    tftypes.String,
		"created": tftypes.String,
		"expires": tftypes.String,
	},
}

// domainsDataSourceType is the whole data source's implied type.
var domainsDataSourceType = tftypes.Object{
	AttributeTypes: map[string]tftypes.Type{
		"domains": tftypes.Set{ElementType: domainSummaryType},
		"total":   tftypes.Number,
		"id":      tftypes.String,
	},
}

// seedFakeDomains fills the fake with n domains and returns their sorted
// names. Every domain carries the same dates, so the checks can spell them.
func seedFakeDomains(fake *fakeNamesilo, n int) []string {
	names := make([]string, 0, n)
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("example%02d.net", i)
		d := fake.addDomain(name)
		d.created = "2020-01-01"
		d.expires = "2026-01-01"
		names = append(names, name)
	}
	return names
}

// domainSummaryCheck builds the ObjectExact check for one inventory row.
func domainSummaryCheck(name string) knownvalue.Check {
	return knownvalue.ObjectExact(map[string]knownvalue.Check{
		"name":    knownvalue.StringExact(name),
		"created": knownvalue.StringExact("2020-01-01"),
		"expires": knownvalue.StringExact("2026-01-01"),
	})
}

// domainsProviderConfig renders the provider block with the page size the
// domains tests drive paging with, followed by the data block.
func domainsProviderConfig(endpoint string, pageSize int64) string {
	return fmt.Sprintf(`
provider "namesilo" {
  api_key   = "test-key"
  endpoint  = %q
  page_size = %d
}

data "namesilo_domains" "test" {}
`, endpoint, pageSize)
}

// listDomainsPages extracts the page parameter of every listDomains request in
// ops, in arrival order. OpenTofu reads a data source during plan, again during
// apply, and once more on refresh, so the exact call count varies with the
// harness; the pages requested are what prove the paging behaviour.
func listDomainsPages(ops []string) []string {
	pages := make([]string, 0, len(ops))
	for _, entry := range ops {
		if opOf(entry) != "listDomains" {
			continue
		}
		page := ""
		for _, param := range strings.Split(entry, "|")[1:] {
			for _, kv := range strings.Split(param, "&") {
				if strings.HasPrefix(kv, "page=") {
					page = strings.TrimPrefix(kv, "page=")
				}
			}
		}
		pages = append(pages, page)
	}
	return pages
}

// TestAccDomainsDataSourceMultiPage covers §12.2 "domains data source" and
// §4 invariant 13: 25 fake domains with a page size of 20 are fetched as two
// pages, combined into one set, and total reports the API's count.
func TestAccDomainsDataSourceMultiPage(t *testing.T) {
	requireTofu(t)
	fake := newFakeNamesilo()
	defer fake.Close()
	names := seedFakeDomains(fake, domainsTotal)

	checks := make([]knownvalue.Check, 0, len(names))
	for _, name := range names {
		checks = append(checks, domainSummaryCheck(name))
	}

	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: domainsProviderConfig(fake.URL(), domainsPerPage),
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue("data.namesilo_domains.test",
					tfjsonpath.New("domains"), knownvalue.SetExact(checks)),
				statecheck.ExpectKnownValue("data.namesilo_domains.test",
					tfjsonpath.New("total"), knownvalue.Int64Exact(domainsTotal)),
				statecheck.ExpectKnownValue("data.namesilo_domains.test",
					tfjsonpath.New("id"), knownvalue.StringExact("domains")),
			},
			PostApplyFunc: func() {
				// Every fetch is two pages. The harness re-reads the data
				// source for plan, apply, and refresh, so the count is a
				// multiple of the two calls one read makes; a page beyond 2
				// would mean the client kept paging past the API's total.
				pages := listDomainsPages(fake.ops())
				if len(pages) == 0 || len(pages)%2 != 0 {
					t.Errorf("listDomains called %d times, want a multiple of 2 (one per page): %v",
						len(pages), pages)
				}
				for _, page := range pages {
					if page != "1" && page != "2" {
						t.Errorf("listDomains requested page %q, want only pages 1 and 2: %v", page, pages)
					}
				}
				last := fake.lastRequest("listDomains")
				if !strings.Contains(last, "page=2") || !strings.Contains(last, "pageSize=20") {
					t.Errorf("the second page was not requested with page=2 and pageSize=20: %s", last)
				}
				if strings.Contains(last, "test-key") {
					t.Errorf("the request log leaked the API key: %s", last)
				}
			},
		}},
	})
}

// TestAccDomainsDataSourcePagingGuard covers §12.2 "domains paging guard" and
// §4 invariant 13's failure half: a fake that ignores the page parameter must
// not loop. The client stops after one repeated page, the data source returns
// the first page's domains with the API's total, and the test completing at
// all is the proof that the read terminated.
func TestAccDomainsDataSourcePagingGuard(t *testing.T) {
	requireTofu(t)
	fake := newFakeNamesilo()
	defer fake.Close()
	seedFakeDomains(fake, domainsTotal)
	fake.setIgnorePaging(true)

	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: domainsProviderConfig(fake.URL(), domainsPerPage),
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue("data.namesilo_domains.test",
					tfjsonpath.New("domains"), knownvalue.SetSizeExact(domainsPerPage)),
				statecheck.ExpectKnownValue("data.namesilo_domains.test",
					tfjsonpath.New("total"), knownvalue.Int64Exact(domainsTotal)),
			},
			PostApplyFunc: func() {
				// The client asks for page 1, then page 2; the fake answers
				// both with page 1's rows, the duplicate-page guard stops the
				// read, and no page 3 is ever requested. The harness re-reads
				// for plan, apply, and refresh, so the count is a multiple of
				// the two calls one read makes. Unbounded calls, or a page
				// beyond 2, would mean the guard did not fire.
				pages := listDomainsPages(fake.ops())
				if len(pages) == 0 || len(pages)%2 != 0 {
					t.Errorf("listDomains called %d times, want a multiple of 2 (page 1 plus the "+
						"repeated page, per read): %v", len(pages), pages)
				}
				for _, page := range pages {
					if page != "1" && page != "2" {
						t.Errorf("listDomains requested page %q, want only pages 1 and 2; a third "+
							"page would mean the duplicate-page guard did not stop the read: %v",
							page, pages)
					}
				}
			},
		}},
	})
}

// TestDomainsDataSourcePagingGuardWarning asserts the incomplete-list warning
// directly. terraform-plugin-testing cannot observe a warning diagnostic from
// a data source read through the harness, so this test drives the protocol 6
// server the way OpenTofu core does: GetProviderSchema, ConfigureProvider with
// the fake's endpoint and a page size of 20, then ReadDataSource. The response
// must carry the warning from the Truncated branch while the state still holds
// the first page's domains and the API's total.
func TestDomainsDataSourcePagingGuardWarning(t *testing.T) {
	fake := newFakeNamesilo()
	defer fake.Close()
	seedFakeDomains(fake, domainsTotal)
	fake.setIgnorePaging(true)

	server, err := providerserver.NewProtocol6WithError(provider.New("test")())()
	if err != nil {
		t.Fatalf("creating the protocol 6 server: %v", err)
	}

	ctx := context.Background()
	if _, err := server.GetProviderSchema(ctx, &tfprotov6.GetProviderSchemaRequest{}); err != nil {
		t.Fatalf("GetProviderSchema returned an error: %v", err)
	}

	providerType := tftypes.Object{
		AttributeTypes: map[string]tftypes.Type{
			"api_key":   tftypes.String,
			"endpoint":  tftypes.String,
			"page_size": tftypes.Number,
		},
	}
	providerValue := tftypes.NewValue(providerType, map[string]tftypes.Value{
		"api_key":   tftypes.NewValue(tftypes.String, "test-key"),
		"endpoint":  tftypes.NewValue(tftypes.String, fake.URL()),
		"page_size": tftypes.NewValue(tftypes.Number, big.NewFloat(domainsPerPage)),
	})
	providerConfig, err := tfprotov6.NewDynamicValue(providerType, providerValue)
	if err != nil {
		t.Fatalf("encoding the provider configuration: %v", err)
	}
	if _, err := server.ConfigureProvider(ctx, &tfprotov6.ConfigureProviderRequest{
		TerraformVersion: "test",
		Config:           &providerConfig,
	}); err != nil {
		t.Fatalf("ConfigureProvider returned an error: %v", err)
	}

	configValue := tftypes.NewValue(domainsDataSourceType, map[string]tftypes.Value{
		"domains": tftypes.NewValue(tftypes.Set{ElementType: domainSummaryType}, nil),
		"total":   tftypes.NewValue(tftypes.Number, nil),
		"id":      tftypes.NewValue(tftypes.String, nil),
	})
	config, err := tfprotov6.NewDynamicValue(domainsDataSourceType, configValue)
	if err != nil {
		t.Fatalf("encoding the data source configuration: %v", err)
	}
	resp, err := server.ReadDataSource(ctx, &tfprotov6.ReadDataSourceRequest{
		TypeName: "namesilo_domains",
		Config:   &config,
	})
	if err != nil {
		t.Fatalf("ReadDataSource returned an error: %v", err)
	}

	var sawWarning bool
	for _, diag := range resp.Diagnostics {
		if diag.Severity != tfprotov6.DiagnosticSeverityWarning {
			t.Errorf("unexpected diagnostic severity %v: %s: %s", diag.Severity, diag.Summary, diag.Detail)
			continue
		}
		if !strings.Contains(diag.Summary, "incomplete") {
			t.Errorf("warning summary %q does not mention the incomplete list", diag.Summary)
			continue
		}
		for _, want := range []string{"ignored the paging parameters",
			fmt.Sprintf("%d of the %d", domainsPerPage, domainsTotal)} {
			if !strings.Contains(diag.Detail, want) {
				t.Errorf("warning detail %q is missing %q", diag.Detail, want)
			}
		}
		sawWarning = true
	}
	if !sawWarning {
		t.Error("ReadDataSource emitted no warning diagnostic for the truncated list")
	}

	// Termination: page 1 plus the one repeat the duplicate-page guard stops
	// on, and not one more.
	if got := fake.count("listDomains"); got != 2 {
		t.Errorf("listDomains called %d times, want 2", got)
	}

	// The state still holds the first page and the API's total.
	if resp.State == nil {
		t.Fatal("ReadDataSource returned no state")
	}
	state, err := resp.State.Unmarshal(domainsDataSourceType)
	if err != nil {
		t.Fatalf("decoding the data source state: %v", err)
	}
	attrs := map[string]tftypes.Value{}
	if err := state.As(&attrs); err != nil {
		t.Fatalf("decoding the state attributes: %v", err)
	}
	setElements := []tftypes.Value{}
	if err := attrs["domains"].As(&setElements); err != nil {
		t.Fatalf("decoding the domains set: %v", err)
	}
	if len(setElements) != domainsPerPage {
		t.Errorf("the state holds %d domains, want the first page's %d", len(setElements), domainsPerPage)
	}
	var total big.Float
	if err := attrs["total"].As(&total); err != nil {
		t.Fatalf("decoding total: %v", err)
	}
	if total.Cmp(big.NewFloat(domainsTotal)) != 0 {
		t.Errorf("total is %v, want %d", &total, domainsTotal)
	}
	var id string
	if err := attrs["id"].As(&id); err != nil {
		t.Fatalf("decoding id: %v", err)
	}
	if id != "domains" {
		t.Errorf("id is %q, want %q", id, "domains")
	}
}
