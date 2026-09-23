// SPDX-License-Identifier: GPL-3.0-or-later

package provider_test

import (
	"context"
	"strings"
	"testing"

	fwdatasource "github.com/hashicorp/terraform-plugin-framework/datasource"
	dsschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	fwresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/nijave/terraform-provider-namesilo/internal/namesilo"
	"github.com/nijave/terraform-provider-namesilo/internal/provider"
)

// TestAccContactsDataSource covers §12.2 "contacts data source": every profile
// in the account is returned, including one with empty optional fields, whose
// attributes come back null the way the resource stores them. id is fixed at
// "contacts".
func TestAccContactsDataSource(t *testing.T) {
	requireTofu(t)
	fake := newFakeNamesilo()
	defer fake.Close()

	full := fullContact()
	fake.seedContact("1001", full.contact("1001", true))
	sparse := namesilo.Contact{
		ID:        "1002",
		FirstName: "Grace",
		LastName:  "Hopper",
		Address:   "1 Navy Way",
		City:      "Arlington",
		State:     "VA",
		Zip:       "22206",
		Country:   "US",
		Email:     "grace@example.com",
		Phone:     "+1 703 555 0100",
	}
	fake.seedContact("1002", sparse)

	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: namesiloProviderConfig(fake.URL()) + `
data "namesilo_contacts" "test" {}
`,
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue("data.namesilo_contacts.test",
					tfjsonpath.New("id"), knownvalue.StringExact("contacts")),
				statecheck.ExpectKnownValue("data.namesilo_contacts.test",
					tfjsonpath.New("contacts"),
					knownvalue.SetExact([]knownvalue.Check{
						contactObjectCheck(full.contact("1001", true)),
						contactObjectCheck(sparse),
					})),
			},
			PostApplyFunc: func() {
				if last := fake.lastRequest("contactList"); strings.Contains(last, "test-key") {
					t.Errorf("the request log leaked the API key: %s", last)
				}
				if last := fake.lastRequest("contactList"); !strings.Contains(last, "contact_id=&") &&
					!strings.HasSuffix(last, "contact_id=") {
					t.Errorf("contactList did not ask for every profile: %s", last)
				}
			},
		}},
	})
}

// TestAccContactsDataSourceEmpty covers §7's empty-account rule: zero profiles
// is an empty set, not an error.
func TestAccContactsDataSourceEmpty(t *testing.T) {
	requireTofu(t)
	fake := newFakeNamesilo()
	defer fake.Close()

	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: namesiloProviderConfig(fake.URL()) + `
data "namesilo_contacts" "test" {}
`,
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue("data.namesilo_contacts.test",
					tfjsonpath.New("contacts"), knownvalue.SetExact(nil)),
				statecheck.ExpectKnownValue("data.namesilo_contacts.test",
					tfjsonpath.New("id"), knownvalue.StringExact("contacts")),
			},
		}},
	})
}

// contactObjectCheck builds the ObjectExact check for one nested contact
// element: every profile attribute is spelled, and an empty string maps to
// Null, the shape nullIfEmpty writes. PII values live in the state file even
// though they are Sensitive, so the check sees them.
func contactObjectCheck(contact namesilo.Contact) knownvalue.Check {
	str := func(v string) knownvalue.Check {
		if v == "" {
			return knownvalue.Null()
		}
		return knownvalue.StringExact(v)
	}
	return knownvalue.ObjectExact(map[string]knownvalue.Check{
		"contact_id":             knownvalue.StringExact(contact.ID),
		"default_profile":        knownvalue.Bool(contact.DefaultProfile),
		"first_name":             str(contact.FirstName),
		"last_name":              str(contact.LastName),
		"address":                str(contact.Address),
		"address2":               str(contact.Address2),
		"city":                   str(contact.City),
		"state":                  str(contact.State),
		"zip":                    str(contact.Zip),
		"country":                str(contact.Country),
		"email":                  str(contact.Email),
		"phone":                  str(contact.Phone),
		"fax":                    str(contact.Fax),
		"company":                str(contact.Company),
		"nickname":               str(contact.Nickname),
		"us_nexus_category":      str(contact.UsNexusCategory),
		"us_application_purpose": str(contact.UsApplicationPurpose),
		"ca_legal_form":          str(contact.CaLegalForm),
		"ca_language":            str(contact.CaLanguage),
		"ca_agreement_version":   str(contact.CaAgreementVersion),
		"ca_whois_display":       str(contact.CaWhoisDisplay),
		"eu_citizenship_country": str(contact.EuCitizenshipCountry),
	})
}

// contactsNonPIIAttributes names the nested contacts attributes that are
// deliberately not Sensitive: the resource's contactNonPIIAttributes with id
// spelled contact_id. Every other nested attribute describes the contact person
// and TestContactsDataSourceSchemaMasksPII requires it to be Sensitive, so the
// nested PII markings cannot drift from the resource's (§6.4, §12.1).
var contactsNonPIIAttributes = map[string]string{
	"contact_id":      "an opaque account-scoped reference, needed as a plain value",
	"default_profile": "account metadata, not personal data",
}

// TestContactsDataSourceSchemaMasksPII is the unit-level guard for the data
// source's nested PII. It walks every nested attribute rather than a hardcoded
// list and asserts each is Sensitive unless it is in contactsNonPIIAttributes.
// It also pins that the nested set mirrors the resource's attribute set exactly
// (with id renamed contact_id), so a field added to one cannot go missing from
// the other.
func TestContactsDataSourceSchemaMasksPII(t *testing.T) {
	t.Parallel()

	ds := provider.NewContactsDataSource()
	var resp fwdatasource.SchemaResponse
	ds.Schema(context.Background(), fwdatasource.SchemaRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("namesilo_contacts schema has diagnostics: %+v", resp.Diagnostics)
	}

	attr, ok := resp.Schema.Attributes["contacts"]
	if !ok {
		t.Fatal("namesilo_contacts schema has no contacts attribute")
	}
	set, ok := attr.(dsschema.SetNestedAttribute)
	if !ok {
		t.Fatalf("contacts is %T, want SetNestedAttribute", attr)
	}

	for name, nested := range set.NestedObject.Attributes {
		reason, exempt := contactsNonPIIAttributes[name]
		switch {
		case exempt && nested.IsSensitive():
			t.Errorf("namesilo_contacts nested attribute %q is Sensitive, but %s", name, reason)
		case !exempt && !nested.IsSensitive():
			t.Errorf("namesilo_contacts nested attribute %q is not Sensitive; every contact-person field must be masked", name)
		}
	}

	// The nested set must mirror the resource's fields exactly.
	r := provider.NewContactResource()
	var resourceResp fwresource.SchemaResponse
	r.Schema(context.Background(), fwresource.SchemaRequest{}, &resourceResp)
	if resourceResp.Diagnostics.HasError() {
		t.Fatalf("namesilo_contact schema has diagnostics: %+v", resourceResp.Diagnostics)
	}
	for name := range resourceResp.Schema.Attributes {
		nested := name
		if name == "id" {
			nested = "contact_id"
		}
		if _, ok := set.NestedObject.Attributes[nested]; !ok {
			t.Errorf("namesilo_contacts nested attributes are missing %q (the resource's %q)", nested, name)
		}
	}
	if got, want := len(set.NestedObject.Attributes), len(resourceResp.Schema.Attributes); got != want {
		t.Errorf("namesilo_contacts has %d nested attributes, want the resource's %d", got, want)
	}
	for name := range contactsNonPIIAttributes {
		if _, ok := set.NestedObject.Attributes[name]; !ok {
			t.Errorf("namesilo_contacts nested attributes are missing %q", name)
		}
	}
}
