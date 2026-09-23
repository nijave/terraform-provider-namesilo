// SPDX-License-Identifier: GPL-3.0-or-later

package provider

import (
	"context"
	"errors"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/nijave/terraform-provider-namesilo/internal/namesilo"
)

// addAPIError turns an error from the client into a diagnostic. The summary
// names the resource type so a user can tell which resource's operation
// failed; the detail is the API's own detail text plus its code for an
// *namesilo.APIError, and the error text otherwise. Neither the request URL
// nor the query appears in an *namesilo.APIError, and the error text from the
// client is itself built without them, so the API key cannot surface here
// (§8.4, §11).
func addAPIError(diags *diag.Diagnostics, resourceType string, err error) {
	summary := "NameSilo API error (" + resourceType + ")"
	var apiErr *namesilo.APIError
	if errors.As(err, &apiErr) {
		diags.AddError(summary, apiErr.Detail+" (code "+apiErr.Code+")")
		return
	}
	diags.AddError(summary, err.Error())
}

// setFromStrings converts a Go slice to a set(string). A nil slice becomes an
// empty set, never null: the framework reports a duplicate element as a
// diagnostic rather than collapsing it, so callers must pass unique values.
func setFromStrings(ctx context.Context, values []string) (types.Set, diag.Diagnostics) {
	if values == nil {
		values = []string{}
	}
	return types.SetValueFrom(ctx, types.StringType, values)
}

// stringsFromSet converts a set(string) to a Go slice. Null and unknown are
// both treated as absent and produce a nil slice with no diagnostic: a null
// required attribute is a schema violation the framework already reports, and
// an unknown value is resolved before Create or Update runs. Element order is
// not meaningful for a set.
func stringsFromSet(ctx context.Context, set types.Set) ([]string, diag.Diagnostics) {
	var diags diag.Diagnostics
	if set.IsNull() || set.IsUnknown() {
		return nil, diags
	}
	var out []string
	diags.Append(set.ElementsAs(ctx, &out, false)...)
	return out, diags
}

// nullIfEmpty maps the API's empty element convention to a null value: the
// API returns `<field/>` for "not set", and a null attribute is how the schema
// spells that so a configuration that omits the field does not diff. Used by
// the optional attributes of later resources.
func nullIfEmpty(value string) types.String {
	if value == "" {
		return types.StringNull()
	}
	return types.StringValue(value)
}

// contactFields is the contact-person field set shared by the namesilo_contact
// resource's state model and the namesilo_contacts data source's nested model.
// Both embed it, so one mapping function fills it and the two cannot drift
// field-by-field (§6.4).
type contactFields struct {
	FirstName            types.String `tfsdk:"first_name"`
	LastName             types.String `tfsdk:"last_name"`
	Address              types.String `tfsdk:"address"`
	Address2             types.String `tfsdk:"address2"`
	City                 types.String `tfsdk:"city"`
	State                types.String `tfsdk:"state"`
	Zip                  types.String `tfsdk:"zip"`
	Country              types.String `tfsdk:"country"`
	Email                types.String `tfsdk:"email"`
	Phone                types.String `tfsdk:"phone"`
	Fax                  types.String `tfsdk:"fax"`
	Company              types.String `tfsdk:"company"`
	Nickname             types.String `tfsdk:"nickname"`
	UsNexusCategory      types.String `tfsdk:"us_nexus_category"`
	UsApplicationPurpose types.String `tfsdk:"us_application_purpose"`
	CaLegalForm          types.String `tfsdk:"ca_legal_form"`
	CaLanguage           types.String `tfsdk:"ca_language"`
	CaAgreementVersion   types.String `tfsdk:"ca_agreement_version"`
	CaWhoisDisplay       types.String `tfsdk:"ca_whois_display"`
	EuCitizenshipCountry types.String `tfsdk:"eu_citizenship_country"`
}

// contactFieldsFromContact is the single mapping from the client's Contact onto
// the shared contact-person field set. Every string field goes through
// nullIfEmpty, because the API returns unset fields as empty elements; a
// required field that arrives empty therefore becomes null and diffs, which is
// the honest report of an API anomaly rather than a silent empty value (§6.4).
// TestContactToModelMapsEveryContactField pins that every field of
// namesilo.Contact is carried here.
func contactFieldsFromContact(contact namesilo.Contact) contactFields {
	return contactFields{
		FirstName:            nullIfEmpty(contact.FirstName),
		LastName:             nullIfEmpty(contact.LastName),
		Address:              nullIfEmpty(contact.Address),
		Address2:             nullIfEmpty(contact.Address2),
		City:                 nullIfEmpty(contact.City),
		State:                nullIfEmpty(contact.State),
		Zip:                  nullIfEmpty(contact.Zip),
		Country:              nullIfEmpty(contact.Country),
		Email:                nullIfEmpty(contact.Email),
		Phone:                nullIfEmpty(contact.Phone),
		Fax:                  nullIfEmpty(contact.Fax),
		Company:              nullIfEmpty(contact.Company),
		Nickname:             nullIfEmpty(contact.Nickname),
		UsNexusCategory:      nullIfEmpty(contact.UsNexusCategory),
		UsApplicationPurpose: nullIfEmpty(contact.UsApplicationPurpose),
		CaLegalForm:          nullIfEmpty(contact.CaLegalForm),
		CaLanguage:           nullIfEmpty(contact.CaLanguage),
		CaAgreementVersion:   nullIfEmpty(contact.CaAgreementVersion),
		CaWhoisDisplay:       nullIfEmpty(contact.CaWhoisDisplay),
		EuCitizenshipCountry: nullIfEmpty(contact.EuCitizenshipCountry),
	}
}

// contactToModel maps the client's Contact onto the resource's state model: the
// shared contact-person fields plus the account-scoped id and default_profile
// flag (§6.4).
func contactToModel(contact namesilo.Contact) contactResourceModel {
	return contactResourceModel{
		contactFields:  contactFieldsFromContact(contact),
		DefaultProfile: types.BoolValue(contact.DefaultProfile),
		ID:             types.StringValue(contact.ID),
	}
}

// modelToContact maps the resource's state model onto the client's Contact. A
// null attribute reads as the empty string the API sends for "unset"; every
// field is sent, empty values included, so the request is deterministic
// (§8.1). default_profile is carried so an update can preserve it; it is not
// a request parameter.
func modelToContact(model contactResourceModel) namesilo.Contact {
	return namesilo.Contact{
		ID:                   model.ID.ValueString(),
		DefaultProfile:       model.DefaultProfile.ValueBool(),
		FirstName:            model.FirstName.ValueString(),
		LastName:             model.LastName.ValueString(),
		Address:              model.Address.ValueString(),
		Address2:             model.Address2.ValueString(),
		City:                 model.City.ValueString(),
		State:                model.State.ValueString(),
		Zip:                  model.Zip.ValueString(),
		Country:              model.Country.ValueString(),
		Email:                model.Email.ValueString(),
		Phone:                model.Phone.ValueString(),
		Fax:                  model.Fax.ValueString(),
		Company:              model.Company.ValueString(),
		Nickname:             model.Nickname.ValueString(),
		UsNexusCategory:      model.UsNexusCategory.ValueString(),
		UsApplicationPurpose: model.UsApplicationPurpose.ValueString(),
		CaLegalForm:          model.CaLegalForm.ValueString(),
		CaLanguage:           model.CaLanguage.ValueString(),
		CaAgreementVersion:   model.CaAgreementVersion.ValueString(),
		CaWhoisDisplay:       model.CaWhoisDisplay.ValueString(),
		EuCitizenshipCountry: model.EuCitizenshipCountry.ValueString(),
	}
}

// dsRecordObjectType is the element type of the dnssec records set: the four
// DS fields with their framework types, named as the schema spells them.
func dsRecordObjectType() types.ObjectType {
	return types.ObjectType{AttrTypes: map[string]attr.Type{
		"key_tag":     types.Int64Type,
		"algorithm":   types.Int64Type,
		"digest_type": types.Int64Type,
		"digest":      types.StringType,
	}}
}

// dsSetFromRecords converts a slice of DS records to a set(object). A nil
// slice becomes an empty set, never null: records = [] is a valid declaration
// that the domain is unsigned, and it is also what Read writes for an empty
// API reply (§4 invariant 2).
func dsSetFromRecords(ctx context.Context, records []namesilo.DSRecord) (types.Set, diag.Diagnostics) {
	if records == nil {
		records = []namesilo.DSRecord{}
	}
	models := make([]dsRecordModel, 0, len(records))
	for _, record := range records {
		models = append(models, dsRecordModel{
			KeyTag:     types.Int64Value(record.KeyTag),
			Algorithm:  types.Int64Value(record.Algorithm),
			DigestType: types.Int64Value(record.DigestType),
			Digest:     types.StringValue(record.Digest),
		})
	}
	return types.SetValueFrom(ctx, dsRecordObjectType(), models)
}

// recordsFromDSSet converts a set(object) to a slice of DS records. Null and
// unknown are both treated as absent and produce a nil slice with no
// diagnostic: a null required attribute is a schema violation the framework
// already reports, and an unknown value is resolved before Create or Update
// runs. Element order is not meaningful for a set.
func recordsFromDSSet(ctx context.Context, set types.Set) ([]namesilo.DSRecord, diag.Diagnostics) {
	var diags diag.Diagnostics
	if set.IsNull() || set.IsUnknown() {
		return nil, diags
	}
	var models []dsRecordModel
	diags.Append(set.ElementsAs(ctx, &models, false)...)
	if diags.HasError() {
		return nil, diags
	}
	records := make([]namesilo.DSRecord, 0, len(models))
	for _, model := range models {
		records = append(records, namesilo.DSRecord{
			KeyTag:     model.KeyTag.ValueInt64(),
			Algorithm:  model.Algorithm.ValueInt64(),
			DigestType: model.DigestType.ValueInt64(),
			Digest:     model.Digest.ValueString(),
		})
	}
	return records, diags
}
