// SPDX-License-Identifier: GPL-3.0-or-later

package provider_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"

	tfjson "github.com/hashicorp/terraform-json"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/nijave/terraform-provider-namesilo/internal/namesilo"
)

// The live acceptance tests (§12.3) run against the real NameSilo API. They
// skip unless the environment opts in, and every one of them mutates the
// domain in NAMESILO_TEST_DOMAIN, which must be a disposable domain in the
// account: the nameserver tests repoint its delegation, the DS tests publish
// a synthetic DS record that makes the domain fail DNSSEC validation while it
// exists, the privacy, lock, and auto-renew tests toggle their flags, and the
// association tests repoint a role at a throwaway profile.

const (
	envAPIKey              = "NAMESILO_API_KEY"
	envTestDomain          = "NAMESILO_TEST_DOMAIN"
	envOptInDNSSEC         = "NAMESILO_TEST_DNSSEC"
	envOptInPrivacy        = "NAMESILO_TEST_PRIVACY"
	envOptInContact        = "NAMESILO_TEST_CONTACT"
	envOptInDomainContacts = "NAMESILO_TEST_DOMAIN_CONTACTS"
	envOptInLock           = "NAMESILO_TEST_LOCK"
)

// liveDomain is the disposable domain the live tests mutate.
func liveDomain() string {
	return os.Getenv(envTestDomain)
}

// liveClient builds a client against the production API from the environment,
// for the check functions that read the live state back after a destroy.
func liveClient() *namesilo.Client {
	return namesilo.NewClient(namesilo.DefaultEndpoint, os.Getenv(envAPIKey), "live-test")
}

// testAccLivePreCheck gates every live test. TF_ACC unset and missing
// credentials both skip, with messages that name the variable and what it is
// for; the two harness fatals are testAccPreCheck's, whose messages name
// `make testacc-live`. The credential skips come first so an operator without
// credentials never trips a harness misconfiguration fatal.
func testAccLivePreCheck(t *testing.T) {
	t.Helper()
	if os.Getenv("TF_ACC") == "" {
		t.Skip("TF_ACC is not set; skipping the live acceptance test")
	}
	if os.Getenv(envAPIKey) == "" {
		t.Skipf("%s is not set; skipping — the live tests call the real NameSilo API and authenticate "+
			"with this key. Set it to the API key of an account that holds a disposable test domain, "+
			"then run `make testacc-live`.", envAPIKey)
	}
	if liveDomain() == "" {
		t.Skipf("%s is not set; skipping — the live tests run against this domain and mutate it: the "+
			"nameserver tests repoint its delegation, the DS tests publish a synthetic DS record that "+
			"makes the domain fail DNSSEC validation while it exists, and the privacy, lock, and "+
			"auto-renew tests toggle their flags. Set it to a disposable domain in the account.",
			envTestDomain)
	}
	// The two fatals below are the base precheck's: its messages already name
	// `make testacc-live`, which is the target that sets these variables.
	testAccPreCheck(t)
}

// liveOptIn skips unless the named per-mutation opt-in is set to "1", with a
// message naming the variable and the consequence of running it.
func liveOptIn(t *testing.T, env, consequence string) {
	t.Helper()
	if os.Getenv(env) != "1" {
		t.Skipf("%s is not set; skipping (%s). Set %s=1 to opt in.", env, consequence, env)
	}
}

// liveProviderConfig is the provider block every live test uses: empty,
// because Configure resolves the API key from NAMESILO_API_KEY and the
// endpoint defaults to production. The key never appears in a config string.
func liveProviderConfig() string {
	return "provider \"namesilo\" {\n}\n"
}

// liveNameserversConfig renders one namesilo_nameservers block.
func liveNameserversConfig(domain, nameservers string) string {
	return liveProviderConfig() + fmt.Sprintf(`
resource "namesilo_nameservers" "test" {
  domain      = %q
  nameservers = %s
}
`, domain, nameservers)
}

// liveNameserversImportConfig renders the import-and-settle configuration: the
// data source carries the delegation as the API reports it, so the settling
// plan is quiet whatever the live nameservers are.
func liveNameserversImportConfig(domain string) string {
	return liveProviderConfig() + fmt.Sprintf(`
data "namesilo_nameservers" "current" {
  domain = %q
}

resource "namesilo_nameservers" "test" {
  domain      = %q
  nameservers = data.namesilo_nameservers.current.nameservers
}
`, domain, domain)
}

// liveDNSSECConfig renders one namesilo_dnssec_records block. records is a raw
// HCL list literal so a test can spell the values however it needs.
func liveDNSSECConfig(domain, records string) string {
	return liveProviderConfig() + fmt.Sprintf(`
resource "namesilo_dnssec_records" "test" {
  domain  = %q
  records = %s
}
`, domain, records)
}

// liveDNSSECImportConfig renders the import-and-settle configuration for the
// domain's DS records as they exist, whatever they are — an empty set included.
func liveDNSSECImportConfig(domain string) string {
	return liveProviderConfig() + fmt.Sprintf(`
data "namesilo_dnssec_records" "current" {
  domain = %q
}

resource "namesilo_dnssec_records" "test" {
  domain  = %q
  records = data.namesilo_dnssec_records.current.records
}
`, domain, domain)
}

// livePrivacyConfig renders one namesilo_privacy block.
func livePrivacyConfig(domain, enabled string) string {
	return liveProviderConfig() + fmt.Sprintf(`
resource "namesilo_privacy" "test" {
  domain  = %q
  enabled = %s
}
`, domain, enabled)
}

// livePrivacyImportConfig renders the import-and-settle configuration.
func livePrivacyImportConfig(domain string) string {
	return liveProviderConfig() + fmt.Sprintf(`
data "namesilo_privacy" "current" {
  domain = %q
}

resource "namesilo_privacy" "test" {
  domain  = %q
  enabled = data.namesilo_privacy.current.enabled
}
`, domain, domain)
}

// liveContactConfig renders one namesilo_contact block whose city varies per
// step: the update's one-field change. The nickname is fixed per profile
// because the API ignores it on update and the provider rejects changing it.
func liveContactConfig(nickname, city string) string {
	return liveProviderConfig() + fmt.Sprintf(`
resource "namesilo_contact" "test" {
  first_name = "Terraform"
  last_name  = "Acceptance"
  address    = "1 Test Way"
  city       = %q
  state      = "TX"
  zip        = "73301"
  country    = "US"
  email      = "terraform-test@example.com"
  phone      = "+1 512 555 0100"
  company    = "Terraform Provider Acceptance"
  nickname   = %q
}
`, city, nickname)
}

// liveDomainLockConfig renders one namesilo_domain_lock block.
func liveDomainLockConfig(domain, locked string) string {
	return liveProviderConfig() + fmt.Sprintf(`
resource "namesilo_domain_lock" "test" {
  domain = %q
  locked = %s
}
`, domain, locked)
}

// liveDomainLockImportConfig renders the import-and-settle configuration.
func liveDomainLockImportConfig(domain string) string {
	return liveProviderConfig() + fmt.Sprintf(`
data "namesilo_domain" "current" {
  domain = %q
}

resource "namesilo_domain_lock" "test" {
  domain = %q
  locked = data.namesilo_domain.current.locked
}
`, domain, domain)
}

// liveAutoRenewConfig renders one namesilo_auto_renew block.
func liveAutoRenewConfig(domain, enabled string) string {
	return liveProviderConfig() + fmt.Sprintf(`
resource "namesilo_auto_renew" "test" {
  domain  = %q
  enabled = %s
}
`, domain, enabled)
}

// liveAutoRenewImportConfig renders the import-and-settle configuration.
func liveAutoRenewImportConfig(domain string) string {
	return liveProviderConfig() + fmt.Sprintf(`
data "namesilo_domain" "current" {
  domain = %q
}

resource "namesilo_auto_renew" "test" {
  domain  = %q
  enabled = data.namesilo_domain.current.auto_renew
}
`, domain, domain)
}

// liveDataSourcesConfig reads all six data sources against the test domain.
func liveDataSourcesConfig(domain string) string {
	return liveProviderConfig() + fmt.Sprintf(`
data "namesilo_nameservers" "test" {
  domain = %q
}

data "namesilo_dnssec_records" "test" {
  domain = %q
}

data "namesilo_privacy" "test" {
  domain = %q
}

data "namesilo_contacts" "test" {
}

data "namesilo_domain" "test" {
  domain = %q
}

data "namesilo_domains" "test" {
}
`, domain, domain, domain, domain)
}

// liveDomainsConfig reads the account inventory.
func liveDomainsConfig() string {
	return liveProviderConfig() + `
data "namesilo_domains" "test" {
}
`
}

// liveDomainContactsBaselineConfig manages the association without any role,
// so all four roles are stored as computed from the API and nothing is sent.
func liveDomainContactsBaselineConfig(domain string) string {
	return liveProviderConfig() + fmt.Sprintf(`
resource "namesilo_domain_contacts" "test" {
  domain = %q
}
`, domain)
}

// liveDomainContactsReassignConfig creates the throwaway profile and repoints
// the domain's technical role at it. The registrant is deliberately left
// alone: changing it can trigger registry verification and is restricted for
// some TLDs.
func liveDomainContactsReassignConfig(domain string) string {
	return liveProviderConfig() + fmt.Sprintf(`
resource "namesilo_contact" "scratch" {
  first_name = "Terraform"
  last_name  = "Acceptance"
  address    = "1 Test Way"
  city       = "Testville"
  state      = "TX"
  zip        = "73301"
  country    = "US"
  email      = "terraform-test@example.com"
  phone      = "+1 512 555 0100"
  company    = "Terraform Provider Acceptance"
  nickname   = "tf-live-domain-contacts"
}

resource "namesilo_domain_contacts" "test" {
  domain    = %q
  technical = namesilo_contact.scratch.id
}
`, domain)
}

// liveDomainContactsRestoreConfig repoints the technical role at the account's
// default profile, found through the contacts data source so the configuration
// needs no hard-coded contact_id. This is what makes the throwaway profile
// deletable again in the next step: contactDelete refuses profiles still
// associated with a domain, and the API has no disassociate operation, so a
// test that left the throwaway in place could never clean up after itself.
// The domain's technical role ends the test at the default profile — the
// residue the NAMESILO_TEST_DOMAIN_CONTACTS opt-in accepts on a disposable
// domain.
func liveDomainContactsRestoreConfig(domain string) string {
	return liveProviderConfig() + fmt.Sprintf(`
data "namesilo_contacts" "all" {
}

resource "namesilo_contact" "scratch" {
  first_name = "Terraform"
  last_name  = "Acceptance"
  address    = "1 Test Way"
  city       = "Testville"
  state      = "TX"
  zip        = "73301"
  country    = "US"
  email      = "terraform-test@example.com"
  phone      = "+1 512 555 0100"
  company    = "Terraform Provider Acceptance"
  nickname   = "tf-live-domain-contacts"
}

resource "namesilo_domain_contacts" "test" {
  domain    = %q
  technical = [for c in data.namesilo_contacts.all.contacts : c.contact_id if c.default_profile][0]
}
`, domain)
}

// liveDomainContactsDropScratchConfig drops the throwaway profile now that
// nothing points at it, so the framework's destroy can delete it.
func liveDomainContactsDropScratchConfig(domain string) string {
	return liveProviderConfig() + fmt.Sprintf(`
data "namesilo_contacts" "all" {
}

resource "namesilo_domain_contacts" "test" {
  domain    = %q
  technical = [for c in data.namesilo_contacts.all.contacts : c.contact_id if c.default_profile][0]
}
`, domain)
}

// liveContactRoles is the four association roles a test captures from state.
type liveContactRoles struct {
	registrant, administrative, technical, billing string
}

// captureDomainContactsRoles reads all four roles of the association resource
// into dst, so a later step can assert they held their prior values. A role
// the API reports as unset is absent from the flat state and captured empty.
func captureDomainContactsRoles(dst *liveContactRoles) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		for name, slot := range map[string]*string{
			"registrant":     &dst.registrant,
			"administrative": &dst.administrative,
			"technical":      &dst.technical,
			"billing":        &dst.billing,
		} {
			value, err := liveResourceAttrOrEmpty(s, "namesilo_domain_contacts.test", name)
			if err != nil {
				return err
			}
			*slot = value
		}
		return nil
	}
}

// checkTechnicalReassigned asserts the technical role moved to the throwaway
// profile and the other three roles hold the values captured at the baseline.
// It takes a pointer because the baseline is filled in by step 1's Check: the
// Steps literal is built before that Check runs, so passing the struct by value
// would freeze a zero-value copy.
func checkTechnicalReassigned(before *liveContactRoles) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		scratch, err := liveResourceAttr(s, "namesilo_contact.scratch", "id")
		if err != nil {
			return err
		}
		technical, err := liveResourceAttr(s, "namesilo_domain_contacts.test", "technical")
		if err != nil {
			return err
		}
		if technical != scratch {
			return fmt.Errorf("technical = %q, want the throwaway profile %q", technical, scratch)
		}
		if technical == before.technical {
			return fmt.Errorf("technical still holds its prior value %q; the role was not reassigned", technical)
		}
		for name, beforeValue := range map[string]string{
			"registrant":     before.registrant,
			"administrative": before.administrative,
			"billing":        before.billing,
		} {
			current, err := liveResourceAttrOrEmpty(s, "namesilo_domain_contacts.test", name)
			if err != nil {
				return err
			}
			if current != beforeValue {
				return fmt.Errorf("%s = %q, want its prior value %q", name, current, beforeValue)
			}
		}
		return nil
	}
}

// liveResourceAttr reads one attribute of one resource from the state.
func liveResourceAttr(s *terraform.State, address, name string) (string, error) {
	rs, ok := s.RootModule().Resources[address]
	if !ok {
		return "", fmt.Errorf("resource %q is not in state", address)
	}
	value, ok := rs.Primary.Attributes[name]
	if !ok {
		return "", fmt.Errorf("resource %q has no %q attribute", address, name)
	}
	return value, nil
}

// liveResourceAttrOrEmpty reads one attribute and captures a null (absent
// flat-state key) as the empty string, for attributes a live domain may
// legitimately leave unset.
func liveResourceAttrOrEmpty(s *terraform.State, address, name string) (string, error) {
	rs, ok := s.RootModule().Resources[address]
	if !ok {
		return "", fmt.Errorf("resource %q is not in state", address)
	}
	return rs.Primary.Attributes[name], nil
}

// tfjsonStateCheck adapts a function over the JSON state into a
// statecheck.StateCheck, for the set-membership assertions the knownvalue
// vocabulary has no check for.
type tfjsonStateCheck struct {
	name string
	fn   func(*tfjson.State) error
}

func (c tfjsonStateCheck) CheckState(_ context.Context, req statecheck.CheckStateRequest, resp *statecheck.CheckStateResponse) {
	if err := c.fn(req.State); err != nil {
		resp.Error = fmt.Errorf("%s: %w", c.name, err)
	}
}

// liveJSONResource returns one resource's attribute values from the JSON
// state.
func liveJSONResource(s *tfjson.State, address string) (map[string]any, error) {
	if s == nil || s.Values == nil || s.Values.RootModule == nil {
		return nil, fmt.Errorf("the state has no root module")
	}
	for _, r := range s.Values.RootModule.Resources {
		if r.Address == address {
			return r.AttributeValues, nil
		}
	}
	return nil, fmt.Errorf("resource %q is not in state", address)
}

// expectDomainsContains asserts the inventory lists the test domain and that
// total is at least the number of rows the fetch returned.
func expectDomainsContains(domain string) statecheck.StateCheck {
	return tfjsonStateCheck{
		name: "namesilo_domains contains " + domain + " with total at least its row count",
		fn: func(s *tfjson.State) error {
			attrs, err := liveJSONResource(s, "data.namesilo_domains.test")
			if err != nil {
				return err
			}
			rows, ok := attrs["domains"].([]any)
			if !ok {
				return fmt.Errorf("domains is %T, want a set", attrs["domains"])
			}
			found := false
			for _, row := range rows {
				m, ok := row.(map[string]any)
				if !ok {
					return fmt.Errorf("a domains row is %T, want an object", row)
				}
				name, _ := m["name"].(string)
				if strings.EqualFold(name, domain) {
					found = true
				}
			}
			if !found {
				return fmt.Errorf("the domains set does not contain %q", domain)
			}
			total, ok := attrs["total"].(json.Number)
			if !ok {
				return fmt.Errorf("total is %T, want a number", attrs["total"])
			}
			count, err := total.Int64()
			if err != nil {
				return fmt.Errorf("parsing total %q: %w", total.String(), err)
			}
			if count < int64(len(rows)) {
				return fmt.Errorf("total = %d is below the %d rows returned", count, len(rows))
			}
			return nil
		},
	}
}

// expectContactsAtLeast asserts the account lists at least n profiles — at
// least one is the account's default profile (§12.3).
func expectContactsAtLeast(minimum int) statecheck.StateCheck {
	return tfjsonStateCheck{
		name: "namesilo_contacts lists at least " + fmt.Sprint(minimum) + " profiles",
		fn: func(s *tfjson.State) error {
			attrs, err := liveJSONResource(s, "data.namesilo_contacts.test")
			if err != nil {
				return err
			}
			rows, ok := attrs["contacts"].([]any)
			if !ok {
				return fmt.Errorf("contacts is %T, want a set", attrs["contacts"])
			}
			if len(rows) < minimum {
				return fmt.Errorf("contacts lists %d profiles, want at least %d", len(rows), minimum)
			}
			return nil
		},
	}
}

// checkDestroyRepointsLive asserts the destroy repointed the live domain at
// the dnsowl defaults rather than clearing delegation (§4 invariant 4).
func checkDestroyRepointsLive() resource.TestCheckFunc {
	return func(_ *terraform.State) error {
		info, err := liveClient().GetDomainInfo(context.Background(), liveDomain())
		if err != nil {
			return fmt.Errorf("reading the live domain's nameservers after destroy: %w", err)
		}
		got := namesilo.NormalizeNameservers(info.Nameservers)
		want := []string{"ns1.dnsowl.com", "ns2.dnsowl.com", "ns3.dnsowl.com"}
		if len(got) != len(want) {
			return fmt.Errorf("after destroy the domain points at %v, want %v", got, want)
		}
		for _, server := range want {
			found := false
			for _, candidate := range got {
				if candidate == server {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("after destroy the domain points at %v, want %v", got, want)
			}
		}
		return nil
	}
}

// expectDSRecord is the knownvalue check for one synthetic DS record.
func expectDSRecord(keyTag int64, digest string) knownvalue.Check {
	return knownvalue.ObjectExact(map[string]knownvalue.Check{
		"key_tag":     knownvalue.Int64Exact(keyTag),
		"algorithm":   knownvalue.Int64Exact(13),
		"digest_type": knownvalue.Int64Exact(2),
		"digest":      knownvalue.StringExact(digest),
	})
}

// TestAccLiveDataSources reads all six data sources against the test domain
// (§12.3). No opt-in beyond the base precheck: every read is harmless. The DS
// records set may legitimately be empty, and the booleans' live values are
// whatever they are, so the assertions cover shape and identity.
func TestAccLiveDataSources(t *testing.T) {
	testAccLivePreCheck(t)
	domain := liveDomain()

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccLivePreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: liveDataSourcesConfig(domain),
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue("data.namesilo_nameservers.test",
					tfjsonpath.New("id"), knownvalue.StringExact(domain)),
				statecheck.ExpectKnownValue("data.namesilo_nameservers.test",
					tfjsonpath.New("nameservers"), knownvalue.NotNull()),
				statecheck.ExpectKnownValue("data.namesilo_dnssec_records.test",
					tfjsonpath.New("id"), knownvalue.StringExact(domain)),
				statecheck.ExpectKnownValue("data.namesilo_dnssec_records.test",
					tfjsonpath.New("records"), knownvalue.NotNull()),
				statecheck.ExpectKnownValue("data.namesilo_privacy.test",
					tfjsonpath.New("id"), knownvalue.StringExact(domain)),
				statecheck.ExpectKnownValue("data.namesilo_privacy.test",
					tfjsonpath.New("enabled"), knownvalue.NotNull()),
				statecheck.ExpectKnownValue("data.namesilo_contacts.test",
					tfjsonpath.New("id"), knownvalue.StringExact("contacts")),
				expectContactsAtLeast(1),
				statecheck.ExpectKnownValue("data.namesilo_domain.test",
					tfjsonpath.New("id"), knownvalue.StringExact(domain)),
				statecheck.ExpectKnownValue("data.namesilo_domain.test",
					tfjsonpath.New("created"), knownvalue.StringRegexp(regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`))),
				statecheck.ExpectKnownValue("data.namesilo_domain.test",
					tfjsonpath.New("expires"), knownvalue.StringRegexp(regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`))),
				statecheck.ExpectKnownValue("data.namesilo_domain.test",
					tfjsonpath.New("locked"), knownvalue.NotNull()),
				statecheck.ExpectKnownValue("data.namesilo_domains.test",
					tfjsonpath.New("id"), knownvalue.StringExact("domains")),
				expectDomainsContains(domain),
			},
		}},
	})
}

// TestAccLiveDomains asserts the inventory contract of §12.3: the list
// contains NAMESILO_TEST_DOMAIN and total is at least the number of returned
// rows.
func TestAccLiveDomains(t *testing.T) {
	testAccLivePreCheck(t)
	domain := liveDomain()

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccLivePreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config:            liveDomainsConfig(),
			ConfigStateChecks: []statecheck.StateCheck{expectDomainsContains(domain)},
		}},
	})
}

// TestAccLiveNameservers covers §12.3's create/read/update/destroy case for
// the delegation: create at the example.net pair, change the set, and destroy
// returns the domain to the dnsowl defaults.
func TestAccLiveNameservers(t *testing.T) {
	testAccLivePreCheck(t)
	domain := liveDomain()

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccLivePreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             checkDestroyRepointsLive(),
		Steps: []resource.TestStep{
			{
				Config: liveNameserversConfig(domain, `["ns1.example.net", "ns2.example.net"]`),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("namesilo_nameservers.test",
						tfjsonpath.New("nameservers"), knownvalue.SetExact(stringSet("ns1.example.net", "ns2.example.net"))),
					statecheck.ExpectKnownValue("namesilo_nameservers.test",
						tfjsonpath.New("id"), knownvalue.StringExact(domain)),
				},
				ConfigPlanChecks: expectEmptyAfterRefresh(),
			},
			{
				Config: liveNameserversConfig(domain, `["ns3.example.net", "ns4.example.net"]`),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("namesilo_nameservers.test",
						tfjsonpath.New("nameservers"), knownvalue.SetExact(stringSet("ns3.example.net", "ns4.example.net"))),
				},
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("namesilo_nameservers.test", plancheck.ResourceActionUpdate),
					},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
		},
	})
}

// TestAccLiveNameserversImport covers §12.3's import-every-resource case for
// the delegation: import by domain, then a settling apply that manages
// whatever the live delegation is and therefore plans quiet.
func TestAccLiveNameserversImport(t *testing.T) {
	testAccLivePreCheck(t)
	domain := liveDomain()

	config := liveNameserversImportConfig(domain)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccLivePreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:             config,
				ResourceName:       "namesilo_nameservers.test",
				ImportState:        true,
				ImportStateId:      domain,
				ImportStatePersist: true,
				ImportStateVerify:  false,
			},
			{
				Config: config,
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("namesilo_nameservers.test",
						tfjsonpath.New("id"), knownvalue.StringExact(domain)),
					statecheck.ExpectKnownValue("namesilo_nameservers.test",
						tfjsonpath.New("nameservers"), knownvalue.NotNull()),
				},
				ConfigPlanChecks: expectEmptyAfterRefresh(),
			},
		},
	})
}

// TestAccLiveDNSSECRecordsImport covers §12.3's import-every-resource case for
// the DS records. The same opt-in as the DS mutation test applies: importing
// an unsigned domain yields records = [], and the destroy at the end of the
// test deletes every DS record present, which disables DNSSEC for the domain.
func TestAccLiveDNSSECRecordsImport(t *testing.T) {
	testAccLivePreCheck(t)
	liveOptIn(t, envOptInDNSSEC,
		"this test destroys the domain's DS records at the end, which disables DNSSEC for the live domain")
	domain := liveDomain()

	config := liveDNSSECImportConfig(domain)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccLivePreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:             config,
				ResourceName:       "namesilo_dnssec_records.test",
				ImportState:        true,
				ImportStateId:      domain,
				ImportStatePersist: true,
				ImportStateVerify:  false,
			},
			{
				Config: config,
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("namesilo_dnssec_records.test",
						tfjsonpath.New("id"), knownvalue.StringExact(domain)),
					statecheck.ExpectKnownValue("namesilo_dnssec_records.test",
						tfjsonpath.New("records"), knownvalue.NotNull()),
				},
				ConfigPlanChecks: expectEmptyAfterRefresh(),
			},
		},
	})
}

// TestAccLivePrivacyImport covers §12.3's import-every-resource case for WHOIS
// privacy. The same opt-in as the privacy mutation test applies: the destroy
// at the end turns privacy off when the imported state says it was on.
func TestAccLivePrivacyImport(t *testing.T) {
	testAccLivePreCheck(t)
	liveOptIn(t, envOptInPrivacy,
		"this test may remove WHOIS privacy from the live domain at the end, changing what a WHOIS lookup exposes")
	domain := liveDomain()

	config := livePrivacyImportConfig(domain)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccLivePreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:             config,
				ResourceName:       "namesilo_privacy.test",
				ImportState:        true,
				ImportStateId:      domain,
				ImportStatePersist: true,
				ImportStateVerify:  false,
			},
			{
				Config: config,
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("namesilo_privacy.test",
						tfjsonpath.New("id"), knownvalue.StringExact(domain)),
					statecheck.ExpectKnownValue("namesilo_privacy.test",
						tfjsonpath.New("enabled"), knownvalue.NotNull()),
				},
				ConfigPlanChecks: expectEmptyAfterRefresh(),
			},
		},
	})
}

// TestAccLiveContactImport covers §12.3's import-every-resource case for a
// contact profile. The import ID is the profile's contact_id, which only
// exists after a create, so the test creates the throwaway profile first,
// imports it by its ID, and lets the framework verify that the imported state
// matches the applied one before a settling apply plans quiet. The destroy at
// the end deletes the profile, which is the account mutation the opt-in covers.
func TestAccLiveContactImport(t *testing.T) {
	testAccLivePreCheck(t)
	liveOptIn(t, envOptInContact,
		"this test creates and deletes contact profiles in the live account")

	config := liveContactConfig("tf-live-contact-import", "Testville")

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccLivePreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config,
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("namesilo_contact.test",
						tfjsonpath.New("first_name"), knownvalue.StringExact("Terraform")),
					statecheck.ExpectKnownValue("namesilo_contact.test",
						tfjsonpath.New("default_profile"), knownvalue.Bool(false)),
					statecheck.ExpectKnownValue("namesilo_contact.test",
						tfjsonpath.New("id"), knownvalue.NotNull()),
				},
				ConfigPlanChecks: expectEmptyAfterRefresh(),
			},
			{
				ResourceName: "namesilo_contact.test",
				ImportState:  true,
				ImportStateIdFunc: func(s *terraform.State) (string, error) {
					return liveResourceAttr(s, "namesilo_contact.test", "id")
				},
				ImportStateVerify: true,
			},
			{
				Config:           config,
				ConfigPlanChecks: expectEmptyAfterRefresh(),
			},
		},
	})
}

// TestAccLiveDomainContactsImport covers §12.3's import-every-resource case
// for the associations. The configuration manages no role, so nothing is sent,
// the destroy is a no-op, and the import is a read of the live domain — no
// opt-in beyond the base precheck.
func TestAccLiveDomainContactsImport(t *testing.T) {
	testAccLivePreCheck(t)
	domain := liveDomain()

	config := liveDomainContactsBaselineConfig(domain)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccLivePreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:             config,
				ResourceName:       "namesilo_domain_contacts.test",
				ImportState:        true,
				ImportStateId:      domain,
				ImportStatePersist: true,
				ImportStateVerify:  false,
			},
			{
				Config: config,
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("namesilo_domain_contacts.test",
						tfjsonpath.New("id"), knownvalue.StringExact(domain)),
				},
				ConfigPlanChecks: expectEmptyAfterRefresh(),
			},
		},
	})
}

// TestAccLiveDomainLockImport covers §12.3's import-every-resource case for
// the registrar lock. The same opt-in as the lock mutation test applies: the
// destroy at the end unlocks the domain when the imported state says it was
// locked.
func TestAccLiveDomainLockImport(t *testing.T) {
	testAccLivePreCheck(t)
	liveOptIn(t, envOptInLock,
		"this test unlocks a live domain at the end, removing its main transfer safeguard")
	domain := liveDomain()

	config := liveDomainLockImportConfig(domain)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccLivePreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:             config,
				ResourceName:       "namesilo_domain_lock.test",
				ImportState:        true,
				ImportStateId:      domain,
				ImportStatePersist: true,
				ImportStateVerify:  false,
			},
			{
				Config: config,
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("namesilo_domain_lock.test",
						tfjsonpath.New("id"), knownvalue.StringExact(domain)),
					statecheck.ExpectKnownValue("namesilo_domain_lock.test",
						tfjsonpath.New("locked"), knownvalue.NotNull()),
				},
				ConfigPlanChecks: expectEmptyAfterRefresh(),
			},
		},
	})
}

// TestAccLiveAutoRenewImport covers §12.3's import-every-resource case for
// auto-renew. No extra opt-in: the destroy turns renewal off only when the
// imported state says it was on, which §12.3 calls harmless on a domain that
// is only used for these tests.
func TestAccLiveAutoRenewImport(t *testing.T) {
	testAccLivePreCheck(t)
	domain := liveDomain()

	config := liveAutoRenewImportConfig(domain)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccLivePreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:             config,
				ResourceName:       "namesilo_auto_renew.test",
				ImportState:        true,
				ImportStateId:      domain,
				ImportStatePersist: true,
				ImportStateVerify:  false,
			},
			{
				Config: config,
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("namesilo_auto_renew.test",
						tfjsonpath.New("id"), knownvalue.StringExact(domain)),
					statecheck.ExpectKnownValue("namesilo_auto_renew.test",
						tfjsonpath.New("enabled"), knownvalue.NotNull()),
				},
				ConfigPlanChecks: expectEmptyAfterRefresh(),
			},
		},
	})
}

// TestAccLiveDNSSEC covers §12.3's add, roll, and remove case with synthetic
// DS records: the first apply publishes one record, the second replaces the
// set with a different record — one reconcile that adds the new DS before
// deleting the old one, the §6.2 order that keeps a window with both
// published — and the empty set removes the survivor. The synthetic records
// make the domain fail DNSSEC validation while they exist, which is what the
// opt-in accepts. Note that the resource is authoritative: any DS records the
// domain already had are removed in the first apply, and the destroy at the
// end deletes every record present, leaving the domain unsigned.
func TestAccLiveDNSSEC(t *testing.T) {
	testAccLivePreCheck(t)
	liveOptIn(t, envOptInDNSSEC,
		"this test publishes a synthetic DS record that makes the live domain fail DNSSEC validation until it is removed")
	domain := liveDomain()

	digestOne := strings.Repeat("ab", 32)
	digestTwo := strings.Repeat("cd", 32)
	recordOne := fmt.Sprintf(`{ key_tag = 12345, algorithm = 13, digest_type = 2, digest = %q }`, digestOne)
	recordTwo := fmt.Sprintf(`{ key_tag = 23456, algorithm = 13, digest_type = 2, digest = %q }`, digestTwo)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccLivePreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				// The add: one synthetic DS record is published, and the
				// settling plan is quiet once it is.
				Config: liveDNSSECConfig(domain, "["+recordOne+"]"),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("namesilo_dnssec_records.test",
						tfjsonpath.New("id"), knownvalue.StringExact(domain)),
					statecheck.ExpectKnownValue("namesilo_dnssec_records.test",
						tfjsonpath.New("records"),
						knownvalue.SetExact([]knownvalue.Check{expectDSRecord(12345, digestOne)})),
				},
				ConfigPlanChecks: expectEmptyAfterRefresh(),
			},
			{
				// The roll: the desired set changes to a different record, so
				// one reconcile adds recordTwo before deleting recordOne —
				// the add-before-delete contract of §6.2 and §16 — and the
				// settling plan is quiet once only recordTwo is published.
				Config: liveDNSSECConfig(domain, "["+recordTwo+"]"),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("namesilo_dnssec_records.test",
						tfjsonpath.New("records"),
						knownvalue.SetExact([]knownvalue.Check{expectDSRecord(23456, digestTwo)})),
				},
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("namesilo_dnssec_records.test", plancheck.ResourceActionUpdate),
					},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			{
				// The remove: the empty set declares the domain unsigned, so
				// the survivor is deleted and the plan stays quiet afterwards.
				Config: liveDNSSECConfig(domain, "[]"),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("namesilo_dnssec_records.test",
						tfjsonpath.New("records"), knownvalue.SetExact(nil)),
				},
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("namesilo_dnssec_records.test", plancheck.ResourceActionUpdate),
					},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
		},
	})
}

// TestAccLivePrivacy covers §12.3's enable, disable, and re-enable case. Each
// direction change issues one call, and the already-in-state replies the API
// can produce are tolerated by the client. Destroy turns privacy off when the
// final state says it was on, so the domain ends the test unprivate — the
// WHOIS exposure change the opt-in accepts.
func TestAccLivePrivacy(t *testing.T) {
	testAccLivePreCheck(t)
	liveOptIn(t, envOptInPrivacy,
		"this test toggles WHOIS privacy on the live domain, changing what a WHOIS lookup exposes")
	domain := liveDomain()

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccLivePreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: livePrivacyConfig(domain, "true"),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("namesilo_privacy.test",
						tfjsonpath.New("id"), knownvalue.StringExact(domain)),
					statecheck.ExpectKnownValue("namesilo_privacy.test",
						tfjsonpath.New("enabled"), knownvalue.Bool(true)),
				},
				ConfigPlanChecks: expectEmptyAfterRefresh(),
			},
			{
				Config: livePrivacyConfig(domain, "false"),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("namesilo_privacy.test",
						tfjsonpath.New("enabled"), knownvalue.Bool(false)),
				},
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("namesilo_privacy.test", plancheck.ResourceActionUpdate),
					},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			{
				Config: livePrivacyConfig(domain, "true"),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("namesilo_privacy.test",
						tfjsonpath.New("enabled"), knownvalue.Bool(true)),
				},
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("namesilo_privacy.test", plancheck.ResourceActionUpdate),
					},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
		},
	})
}

// TestAccLiveContact covers §12.3's create, update, and delete case for a
// throwaway profile with clearly test data. Destroy deletes the profile; the
// test never associates it with a domain, so contactDelete accepts it.
func TestAccLiveContact(t *testing.T) {
	testAccLivePreCheck(t)
	liveOptIn(t, envOptInContact,
		"this test creates and deletes contact profiles in the live account")

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccLivePreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: liveContactConfig("tf-live-contact", "Testville"),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("namesilo_contact.test",
						tfjsonpath.New("first_name"), knownvalue.StringExact("Terraform")),
					statecheck.ExpectKnownValue("namesilo_contact.test",
						tfjsonpath.New("email"), knownvalue.StringExact("terraform-test@example.com")),
					statecheck.ExpectKnownValue("namesilo_contact.test",
						tfjsonpath.New("default_profile"), knownvalue.Bool(false)),
					statecheck.ExpectKnownValue("namesilo_contact.test",
						tfjsonpath.New("id"), knownvalue.NotNull()),
				},
				ConfigPlanChecks: expectEmptyAfterRefresh(),
			},
			{
				// The nickname is immutable after create, so the update moves
				// the mutable city instead.
				Config: liveContactConfig("tf-live-contact", "Testville-2"),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("namesilo_contact.test",
						tfjsonpath.New("city"), knownvalue.StringExact("Testville-2")),
				},
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("namesilo_contact.test", plancheck.ResourceActionUpdate),
					},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
		},
	})
}

// TestAccLiveDomainContacts covers §12.3's reassign-a-role case: the domain's
// technical role moves to a throwaway profile (the registrant is deliberately
// left alone: changing it can trigger registry verification and is restricted
// for some TLDs) while the other three roles hold their prior values, and the
// destroy of the association resource afterwards is a no-op.
//
// The test then reassigns the technical role at the account's default profile
// before dropping the throwaway profile. That ordering is what lets the test
// clean up after itself: contactDelete refuses profiles still associated with
// a domain and the API has no disassociate operation, so a throwaway left
// pointing at the domain could never be deleted and the framework's destroy
// would fail. The residue is the domain's technical role ending at the
// default profile, which the NAMESILO_TEST_DOMAIN_CONTACTS opt-in accepts on
// a disposable domain.
func TestAccLiveDomainContacts(t *testing.T) {
	testAccLivePreCheck(t)
	liveOptIn(t, envOptInDomainContacts,
		"this test reassigns one of the live domain's contact roles — the registrant can trigger registry verification and is restricted for some TLDs")
	domain := liveDomain()

	var baseline liveContactRoles

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccLivePreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				// The baseline manages no role: all four are stored as
				// computed from the API and nothing is sent, so the captured
				// values are the domain's pre-test associations.
				Config:           liveDomainContactsBaselineConfig(domain),
				ConfigPlanChecks: expectEmptyAfterRefresh(),
				Check:            captureDomainContactsRoles(&baseline),
			},
			{
				Config:           liveDomainContactsReassignConfig(domain),
				ConfigPlanChecks: expectEmptyAfterRefresh(),
				Check:            checkTechnicalReassigned(&baseline),
			},
			{
				// Reassign the technical role away from the throwaway, so the
				// next step can delete it.
				Config: liveDomainContactsRestoreConfig(domain),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("namesilo_domain_contacts.test", plancheck.ResourceActionUpdate),
					},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			{
				// The throwaway is no longer associated, so the framework can
				// delete it, and the association resource's own destroy at the
				// end of the test is the no-op the resource documents.
				Config: liveDomainContactsDropScratchConfig(domain),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("namesilo_contact.scratch", plancheck.ResourceActionDestroy),
					},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
		},
	})
}

// TestAccLiveDomainLock covers §12.3's lock and unlock case. The domain ends
// the test unlocked — the transfer-safeguard change the opt-in accepts, and
// the destroy at the end makes no further call because the final state says
// the resource was unlocked.
func TestAccLiveDomainLock(t *testing.T) {
	testAccLivePreCheck(t)
	liveOptIn(t, envOptInLock,
		"this test unlocks a live domain, removing its main transfer safeguard")
	domain := liveDomain()

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccLivePreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: liveDomainLockConfig(domain, "true"),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("namesilo_domain_lock.test",
						tfjsonpath.New("id"), knownvalue.StringExact(domain)),
					statecheck.ExpectKnownValue("namesilo_domain_lock.test",
						tfjsonpath.New("locked"), knownvalue.Bool(true)),
				},
				ConfigPlanChecks: expectEmptyAfterRefresh(),
			},
			{
				Config: liveDomainLockConfig(domain, "false"),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("namesilo_domain_lock.test",
						tfjsonpath.New("locked"), knownvalue.Bool(false)),
				},
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("namesilo_domain_lock.test", plancheck.ResourceActionUpdate),
					},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
		},
	})
}

// TestAccLiveAutoRenew covers §12.3's enable and disable case. No extra
// opt-in: the resource's own lifecycle toggles the flag, and the destroy at
// the end makes no call because the final state says the resource was
// disabled (§12.3).
func TestAccLiveAutoRenew(t *testing.T) {
	testAccLivePreCheck(t)
	domain := liveDomain()

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccLivePreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: liveAutoRenewConfig(domain, "true"),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("namesilo_auto_renew.test",
						tfjsonpath.New("id"), knownvalue.StringExact(domain)),
					statecheck.ExpectKnownValue("namesilo_auto_renew.test",
						tfjsonpath.New("enabled"), knownvalue.Bool(true)),
				},
				ConfigPlanChecks: expectEmptyAfterRefresh(),
			},
			{
				Config: liveAutoRenewConfig(domain, "false"),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("namesilo_auto_renew.test",
						tfjsonpath.New("enabled"), knownvalue.Bool(false)),
				},
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("namesilo_auto_renew.test", plancheck.ResourceActionUpdate),
					},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
		},
	})
}
