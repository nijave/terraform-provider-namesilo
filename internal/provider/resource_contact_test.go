// SPDX-License-Identifier: GPL-3.0-or-later

package provider_test

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"testing"

	fwresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/nijave/terraform-provider-namesilo/internal/namesilo"
	"github.com/nijave/terraform-provider-namesilo/internal/provider"
)

// contactPIIAttributes is §6.4's PII set: every attribute that describes the
// contact person. TestContactSchemaMasksPII pins that each one is Sensitive so
// the GDPR mapping cannot drift silently (§12.1).
var contactPIIAttributes = []string{
	"first_name", "last_name", "address", "address2", "city", "state", "zip",
	"country", "email", "phone", "fax", "company", "nickname",
	"us_nexus_category", "us_application_purpose",
	"ca_legal_form", "ca_language", "ca_agreement_version", "ca_whois_display",
	"eu_citizenship_country",
}

// TestContactSchemaMasksPII is the unit-level guard §12.1 asks for: it
// instantiates the resource, calls Schema directly, and asserts the PII
// mapping. id and default_profile are deliberately not Sensitive — id is an
// opaque account-scoped reference and default_profile is account metadata, not
// personal data (§6.4).
func TestContactSchemaMasksPII(t *testing.T) {
	t.Parallel()

	r := provider.NewContactResource()
	var resp fwresource.SchemaResponse
	r.Schema(context.Background(), fwresource.SchemaRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("namesilo_contact schema has diagnostics: %+v", resp.Diagnostics)
	}

	for _, name := range contactPIIAttributes {
		attr, ok := resp.Schema.Attributes[name]
		if !ok {
			t.Errorf("namesilo_contact schema has no %q attribute", name)
			continue
		}
		if !attr.IsSensitive() {
			t.Errorf("namesilo_contact attribute %q is not Sensitive; PII must be masked", name)
		}
	}
	for _, name := range []string{"id", "default_profile"} {
		attr, ok := resp.Schema.Attributes[name]
		if !ok {
			t.Errorf("namesilo_contact schema has no %q attribute", name)
			continue
		}
		if attr.IsSensitive() {
			t.Errorf("namesilo_contact attribute %q is Sensitive, but it is not PII", name)
		}
	}
}

// contactValues is the field set a test configures and, for import, seeds into
// the fake, so the two always agree.
type contactValues struct {
	FirstName, LastName, Address, Address2, City, State, Zip, Country string
	Email, Phone, Fax, Company, Nickname                              string
	UsNexusCategory, UsApplicationPurpose                             string
	CaLegalForm, CaLanguage, CaAgreementVersion, CaWhoisDisplay       string
	EuCitizenshipCountry                                              string
}

// fullContact returns every §6.4 attribute populated. country is uppercase
// because it is Sensitive: OpenTofu forbids a provider from planning a known
// Sensitive value that differs from the configuration, so ModifyPlan's
// uppercasing is a no-op for canonical input and only a non-canonical code
// would trip the core check. See the report for that core limitation.
func fullContact() contactValues {
	return contactValues{
		FirstName:            "Ada",
		LastName:             "Lovelace",
		Address:              "1 Analytical Engine Way",
		Address2:             "Suite 200",
		City:                 "London",
		State:                "Greater London",
		Zip:                  "SW1A 1AA",
		Country:              "GB",
		Email:                "ada@example.com",
		Phone:                "+44 20 7946 0000",
		Fax:                  "+44 20 7946 0001",
		Company:              "Analytical Engines Ltd",
		Nickname:             "ada",
		UsNexusCategory:      "C11",
		UsApplicationPurpose: "P1",
		CaLegalForm:          "CORP",
		CaLanguage:           "en",
		CaAgreementVersion:   "2.0",
		CaWhoisDisplay:       "1",
		EuCitizenshipCountry: "FR",
	}
}

// attrs renders the contact as HCL attribute literals. When optional is false
// the optional fields are omitted entirely; the required ones are always
// present.
func (v contactValues) attrs(optional bool) map[string]string {
	m := map[string]string{
		"first_name": quoteHCL(v.FirstName),
		"last_name":  quoteHCL(v.LastName),
		"address":    quoteHCL(v.Address),
		"city":       quoteHCL(v.City),
		"state":      quoteHCL(v.State),
		"zip":        quoteHCL(v.Zip),
		"country":    quoteHCL(v.Country),
		"email":      quoteHCL(v.Email),
		"phone":      quoteHCL(v.Phone),
	}
	if optional {
		m["address2"] = quoteHCL(v.Address2)
		m["fax"] = quoteHCL(v.Fax)
		m["company"] = quoteHCL(v.Company)
		m["nickname"] = quoteHCL(v.Nickname)
		m["us_nexus_category"] = quoteHCL(v.UsNexusCategory)
		m["us_application_purpose"] = quoteHCL(v.UsApplicationPurpose)
		m["ca_legal_form"] = quoteHCL(v.CaLegalForm)
		m["ca_language"] = quoteHCL(v.CaLanguage)
		m["ca_agreement_version"] = quoteHCL(v.CaAgreementVersion)
		m["ca_whois_display"] = quoteHCL(v.CaWhoisDisplay)
		m["eu_citizenship_country"] = quoteHCL(v.EuCitizenshipCountry)
	}
	return m
}

// contact converts the values to the client's Contact shape, for seeding the
// fake during import.
func (v contactValues) contact(id string, defaultProfile bool) namesilo.Contact {
	return namesilo.Contact{
		ID:                   id,
		DefaultProfile:       defaultProfile,
		FirstName:            v.FirstName,
		LastName:             v.LastName,
		Address:              v.Address,
		Address2:             v.Address2,
		City:                 v.City,
		State:                v.State,
		Zip:                  v.Zip,
		Country:              strings.ToUpper(v.Country),
		Email:                v.Email,
		Phone:                v.Phone,
		Fax:                  v.Fax,
		Company:              v.Company,
		Nickname:             v.Nickname,
		UsNexusCategory:      v.UsNexusCategory,
		UsApplicationPurpose: v.UsApplicationPurpose,
		CaLegalForm:          v.CaLegalForm,
		CaLanguage:           v.CaLanguage,
		CaAgreementVersion:   v.CaAgreementVersion,
		CaWhoisDisplay:       v.CaWhoisDisplay,
		EuCitizenshipCountry: v.EuCitizenshipCountry,
	}
}

// contactConfig renders one namesilo_contact block from HCL attribute
// literals, sorted by name for deterministic output.
func contactConfig(endpoint string, attrs map[string]string) string {
	names := make([]string, 0, len(attrs))
	for name := range attrs {
		names = append(names, name)
	}
	sort.Strings(names)

	var b strings.Builder
	b.WriteString(namesiloProviderConfig(endpoint))
	b.WriteString("resource \"namesilo_contact\" \"test\" {\n")
	for _, name := range names {
		b.WriteString("  " + name + " = " + attrs[name] + "\n")
	}
	b.WriteString("}\n")
	return b.String()
}

// quoteHCL quotes a value for an HCL string literal.
func quoteHCL(value string) string {
	return `"` + value + `"`
}

// expectContactString checks one contact attribute's applied value.
func expectContactString(attr, value string) statecheck.StateCheck {
	return statecheck.ExpectKnownValue("namesilo_contact.test",
		tfjsonpath.New(attr), knownvalue.StringExact(value))
}

// expectContactNull checks one contact attribute is null.
func expectContactNull(attr string) statecheck.StateCheck {
	return statecheck.ExpectKnownValue("namesilo_contact.test",
		tfjsonpath.New(attr), knownvalue.Null())
}

// expectContactBool checks default_profile.
func expectContactBool(value bool) statecheck.StateCheck {
	return statecheck.ExpectKnownValue("namesilo_contact.test",
		tfjsonpath.New("default_profile"), knownvalue.Bool(value))
}

// TestAccContactCreate covers §12.2 "contact create" and §4 invariant 8: the
// fake records every configured field, the returned contact_id becomes id,
// default_profile is written as false, and the plan is quiet after refresh.
func TestAccContactCreate(t *testing.T) {
	requireTofu(t)
	fake := newFakeNamesilo()
	defer fake.Close()

	values := fullContact()
	config := contactConfig(fake.URL(), values.attrs(true))

	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: config,
			ConfigStateChecks: []statecheck.StateCheck{
				expectContactString("id", "1001"),
				expectContactBool(false),
				expectContactString("first_name", "Ada"),
				// country is uppercased at plan time, then echoed by the fake.
				expectContactString("country", "GB"),
			},
			ConfigPlanChecks: expectEmptyAfterRefresh(),
			PostApplyFunc: func() {
				if got := fake.count("contactAdd"); got != 1 {
					t.Errorf("contactAdd called %d times, want 1", got)
				}
				last := fake.lastRequest("contactAdd")
				for _, want := range []string{
					"fn=Ada", "ln=Lovelace", "ad=1 Analytical Engine Way",
					"ad2=Suite 200", "cy=London", "st=Greater London", "zp=SW1A 1AA",
					"ct=GB", "em=ada@example.com", "ph=+44 20 7946 0000",
					"fx=+44 20 7946 0001", "cp=Analytical Engines Ltd", "nn=ada",
					"usnc=C11", "usap=P1", "calf=CORP", "caln=en", "caag=2.0",
					"cawd=1", "eucs=FR",
				} {
					if !strings.Contains(last, want) {
						t.Errorf("contactAdd request is missing %q: %s", want, last)
					}
				}
				if strings.Contains(last, "test-key") {
					t.Errorf("the request log leaked the API key: %s", last)
				}
			},
		}},
	})
}

// TestAccContactUpdate covers §12.2 "contact update": changing one field issues
// exactly one contactUpdate, the plan is quiet afterwards, and default_profile
// is carried rather than reset.
func TestAccContactUpdate(t *testing.T) {
	requireTofu(t)
	fake := newFakeNamesilo()
	defer fake.Close()

	values := fullContact()
	before := contactConfig(fake.URL(), values.attrs(true))
	changed := fullContact()
	changed.City = "Manchester"
	after := contactConfig(fake.URL(), changed.attrs(true))

	var baseline int
	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:            before,
				ConfigStateChecks: []statecheck.StateCheck{expectContactString("city", "London")},
				ConfigPlanChecks:  expectEmptyAfterRefresh(),
				PostApplyFunc: func() {
					// The account default is not this profile yet; make the
					// fake report it after the update so the carry is visible.
					fake.mutateContact("1001", func(c *namesilo.Contact) { c.DefaultProfile = true })
					baseline = len(fake.ops())
				},
			},
			{
				Config: after,
				ConfigStateChecks: []statecheck.StateCheck{
					expectContactString("city", "Manchester"),
					expectContactBool(true),
				},
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("namesilo_contact.test",
							plancheck.ResourceActionUpdate),
					},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				PostApplyFunc: func() {
					step := fake.ops()[baseline:]
					if got := countOps(step, "contactUpdate"); got != 1 {
						t.Errorf("the update issued %d contactUpdate calls, want 1: %v", got, step)
					}
					if got := countOps(step, "contactAdd"); got != 0 {
						t.Errorf("the update issued %d contactAdd calls, want 0: %v", got, step)
					}
				},
			},
		},
	})
}

// TestAccContactNicknameCreateOnly covers §6.4's create-only nickname rule:
// the API honors nn on contactAdd but ignores it on contactUpdate, so
// ModifyPlan rejects a changed nickname on an existing profile with the
// diagnostic instead of applying an update that would silently revert. An
// unchanged nickname with a changed city still updates.
func TestAccContactNicknameCreateOnly(t *testing.T) {
	requireTofu(t)
	fake := newFakeNamesilo()
	defer fake.Close()

	values := fullContact()
	created := contactConfig(fake.URL(), values.attrs(true))

	changedNickname := fullContact()
	changedNickname.Nickname = "ada-2"
	nicknameConfig := contactConfig(fake.URL(), changedNickname.attrs(true))

	changedCity := fullContact()
	changedCity.City = "Manchester"
	cityConfig := contactConfig(fake.URL(), changedCity.attrs(true))

	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:            created,
				ConfigStateChecks: []statecheck.StateCheck{expectContactString("nickname", "ada")},
				ConfigPlanChecks:  expectEmptyAfterRefresh(),
			},
			{
				Config:      nicknameConfig,
				ExpectError: regexp.MustCompile("Nickname cannot be changed after creation"),
			},
			{
				Config: cityConfig,
				ConfigStateChecks: []statecheck.StateCheck{
					expectContactString("nickname", "ada"),
					expectContactString("city", "Manchester"),
				},
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("namesilo_contact.test",
							plancheck.ResourceActionUpdate),
					},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
		},
	})
}

// TestAccContactEmptyOptionalRoundTrip covers §12.2 "contact empty optional"
// and §4 invariant 8: a config with only the required fields sends every
// optional as empty, the fake returns empty elements, state holds null, and the
// plan is quiet.
func TestAccContactEmptyOptionalRoundTrip(t *testing.T) {
	requireTofu(t)
	fake := newFakeNamesilo()
	defer fake.Close()

	values := fullContact()
	config := contactConfig(fake.URL(), values.attrs(false))

	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: config,
			ConfigStateChecks: []statecheck.StateCheck{
				expectContactString("id", "1001"),
				expectContactString("country", "GB"),
				expectContactNull("address2"),
				expectContactNull("fax"),
				expectContactNull("company"),
				expectContactNull("nickname"),
				expectContactNull("us_nexus_category"),
				expectContactNull("us_application_purpose"),
				expectContactNull("ca_legal_form"),
				expectContactNull("ca_language"),
				expectContactNull("ca_agreement_version"),
				expectContactNull("ca_whois_display"),
				expectContactNull("eu_citizenship_country"),
			},
			ConfigPlanChecks: expectEmptyAfterRefresh(),
			PostApplyFunc: func() {
				last := fake.lastRequest("contactAdd")
				for _, param := range []string{
					"ad2=", "fx=", "cp=", "nn=", "usnc=", "usap=",
					"calf=", "caln=", "caag=", "cawd=", "eucs=",
				} {
					if !strings.Contains(last, param) {
						t.Errorf("contactAdd omitted the empty optional %q: %s", param, last)
					}
				}
			},
		}},
	})
}

// TestAccContactDefaultProfileComputed covers §6.4's Create contract: Create
// writes default_profile = false without a second API call, and the refresh
// that follows corrects it because the attribute is Computed, with no plan
// diff.
func TestAccContactDefaultProfileComputed(t *testing.T) {
	requireTofu(t)
	fake := newFakeNamesilo()
	defer fake.Close()

	values := fullContact()
	config := contactConfig(fake.URL(), values.attrs(true))

	var baseline int
	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:            config,
				ConfigStateChecks: []statecheck.StateCheck{expectContactBool(false)},
				ConfigPlanChecks:  expectEmptyAfterRefresh(),
				PostApplyFunc:     func() { baseline = len(fake.ops()) },
			},
			{
				// The created profile becomes the account default out of
				// band; the refresh picks it up and the computed correction
				// produces no diff and no API write.
				PreConfig: func() {
					fake.mutateContact("1001", func(c *namesilo.Contact) { c.DefaultProfile = true })
				},
				Config:            config,
				ConfigStateChecks: []statecheck.StateCheck{expectContactBool(true)},
				ConfigPlanChecks:  expectEmptyAfterRefresh(),
				PostApplyFunc: func() {
					step := fake.ops()[baseline:]
					if got := countOps(step, "contactUpdate"); got != 0 {
						t.Errorf("the computed correction issued %d contactUpdate calls, want 0: %v", got, step)
					}
				},
			},
		},
	})
}

// TestAccContactDrift covers §12.2 "contact drift": a profile edited out of
// band plans an update and the apply converges.
func TestAccContactDrift(t *testing.T) {
	requireTofu(t)
	fake := newFakeNamesilo()
	defer fake.Close()

	values := fullContact()
	config := contactConfig(fake.URL(), values.attrs(true))

	var baseline int
	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:            config,
				ConfigStateChecks: []statecheck.StateCheck{expectContactString("city", "London")},
				ConfigPlanChecks:  expectEmptyAfterRefresh(),
				PostApplyFunc:     func() { baseline = len(fake.ops()) },
			},
			{
				PreConfig: func() {
					fake.mutateContact("1001", func(c *namesilo.Contact) { c.City = "Bristol" })
				},
				Config:            config,
				ConfigStateChecks: []statecheck.StateCheck{expectContactString("city", "London")},
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("namesilo_contact.test",
							plancheck.ResourceActionUpdate),
					},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				PostApplyFunc: func() {
					step := fake.ops()[baseline:]
					if got := countOps(step, "contactUpdate"); got != 1 {
						t.Errorf("the drift correction issued %d contactUpdate calls, want 1: %v", got, step)
					}
				},
			},
		},
	})
}

// TestAccContactGone covers §12.2 "contact gone" and §11's one exception: a
// successful contactList filtered by ID that returns no profile removes the
// resource from state with a warning, so the next plan recreates it instead of
// failing every future plan.
func TestAccContactGone(t *testing.T) {
	requireTofu(t)
	fake := newFakeNamesilo()
	defer fake.Close()

	values := fullContact()
	config := contactConfig(fake.URL(), values.attrs(true))

	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:            config,
				ConfigStateChecks: []statecheck.StateCheck{expectContactString("id", "1001")},
				ConfigPlanChecks:  expectEmptyAfterRefresh(),
			},
			{
				PreConfig: func() {
					// The profile is gone from the account. Read reports it
					// absent, the framework drops it from state, and the plan
					// below proposes a create.
					fake.removeContact("1001")
				},
				Config: config,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("namesilo_contact.test",
							plancheck.ResourceActionCreate),
					},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				ConfigStateChecks: []statecheck.StateCheck{expectContactString("id", "1002")},
				PostApplyFunc: func() {
					if got := fake.count("contactAdd"); got != 2 {
						t.Errorf("contactAdd called %d times across create+gone, want 2", got)
					}
					if _, ok := fake.contactFor("1002"); !ok {
						t.Error("the gone path did not create a replacement profile")
					}
				},
			},
		},
	})
}

// TestAccContactDestroy covers §12.2 "contact destroy" and §4 invariant 11:
// destroy calls contactDelete exactly once and removes the profile.
func TestAccContactDestroy(t *testing.T) {
	requireTofu(t)
	fake := newFakeNamesilo()
	defer fake.Close()

	values := fullContact()
	config := contactConfig(fake.URL(), values.attrs(true))

	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: func(_ *terraform.State) error {
			if got := fake.count("contactDelete"); got != 1 {
				return fmt.Errorf("destroy issued %d contactDelete calls, want 1", got)
			}
			if _, ok := fake.contactFor("1001"); ok {
				return fmt.Errorf("contact 1001 still present after destroy")
			}
			return nil
		},
		Steps: []resource.TestStep{{
			Config:            config,
			ConfigStateChecks: []statecheck.StateCheck{expectContactString("id", "1001")},
			ConfigPlanChecks:  expectEmptyAfterRefresh(),
		}},
	})
}

// TestAccContactDestroyBlocked covers §6.4 and §4 invariant 11's failure half:
// the API refuses to delete a profile still associated with a domain, and the
// error is surfaced unchanged rather than swallowed. The injected failure is
// cleared for a follow-up destroy step so the test leaves no profile behind;
// the deferred post-test destroy would otherwise retry the same failed plan.
func TestAccContactDestroyBlocked(t *testing.T) {
	requireTofu(t)
	fake := newFakeNamesilo()
	defer fake.Close()

	values := fullContact()
	config := contactConfig(fake.URL(), values.attrs(true))

	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: func(_ *terraform.State) error {
			if _, ok := fake.contactFor("1001"); ok {
				return fmt.Errorf("post-test destroy left contact 1001 behind")
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config:            config,
				ConfigStateChecks: []statecheck.StateCheck{expectContactString("id", "1001")},
				ConfigPlanChecks:  expectEmptyAfterRefresh(),
			},
			{
				Config: config,
				PreConfig: func() {
					fake.failWith("contactDelete", "210",
						"Contact is currently associated with a domain and cannot be deleted")
				},
				Destroy:     true,
				ExpectError: regexp.MustCompile("associated with a domain"),
			},
			{
				Config: config,
				PreConfig: func() {
					fake.clearFailure("contactDelete")
				},
				Destroy: true,
				Check: func(s *terraform.State) error {
					// Check receives the state before this step's destroy, so
					// it still holds the profile the blocked destroy kept.
					if len(s.RootModule().Resources) != 1 {
						return fmt.Errorf("the blocked destroy did not keep the contact in state")
					}
					return nil
				},
			},
		},
	})
}

// TestAccContactImport covers §10: importing by contact_id seeds only id, Read
// fills every field from contactList, and a settling step is quiet. The
// profile seeded in the fake is the resource's state, so the import must match
// it exactly.
func TestAccContactImport(t *testing.T) {
	requireTofu(t)
	fake := newFakeNamesilo()
	defer fake.Close()

	values := fullContact()
	fake.seedContact("1001", values.contact("1001", true))
	config := contactConfig(fake.URL(), values.attrs(true))

	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:             config,
				ResourceName:       "namesilo_contact.test",
				ImportState:        true,
				ImportStateId:      "1001",
				ImportStatePersist: true,
				ImportStateVerify:  false,
			},
			{
				Config: config,
				ConfigStateChecks: []statecheck.StateCheck{
					expectContactString("id", "1001"),
					expectContactBool(true),
					expectContactString("city", "London"),
					expectContactString("country", "GB"),
				},
				ConfigPlanChecks: expectEmptyAfterRefresh(),
			},
		},
	})
}
