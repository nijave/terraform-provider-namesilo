// SPDX-License-Identifier: GPL-3.0-or-later

package provider

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/nijave/terraform-provider-namesilo/internal/namesilo"
)

var (
	_ resource.Resource                = (*contactResource)(nil)
	_ resource.ResourceWithConfigure   = (*contactResource)(nil)
	_ resource.ResourceWithImportState = (*contactResource)(nil)
	_ resource.ResourceWithModifyPlan  = (*contactResource)(nil)
)

// iso3166Alpha2Pattern matches an uppercase ISO 3166-1 alpha-2 country code:
// exactly two uppercase ASCII letters. Lowercase is rejected at validate time
// rather than normalized, because OpenTofu core forbids the provider from
// changing a known configured value at plan time on create (see ModifyPlan).
var iso3166Alpha2Pattern = regexp.MustCompile(`^[A-Z]{2}$`)

// contactResource manages one account-level contact profile. A profile is
// identified by the API's contact_id and is not owned by a domain: a domain
// references profiles by ID in four roles, so profiles and their associations
// are separate resources (§6.4).
type contactResource struct {
	client *namesilo.Client
}

// NewContactResource returns the namesilo_contact resource.
func NewContactResource() resource.Resource {
	return &contactResource{}
}

// contactResourceModel is namesilo_contact's state model. Every field that
// describes the contact person is a types.String mapped through nullIfEmpty;
// default_profile is the account-level computed flag and id is the API's
// contact_id.
type contactResourceModel struct {
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
	DefaultProfile       types.Bool   `tfsdk:"default_profile"`
	ID                   types.String `tfsdk:"id"`
}

func (r *contactResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_contact"
}

func (r *contactResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a contact profile — an account-level record identified by its " +
			"`contact_id`. A domain references profiles by ID in four roles: registrant, " +
			"administrative, technical, and billing. A profile is not owned by a domain, so profiles " +
			"and their associations are separate resources (§6.4).\n\n" +
			"Create makes a new profile every time an apply starts from a create; there is no " +
			"name-based adoption, so importing by `contact_id` is the only way to take over an " +
			"existing profile.\n\n" +
			"Every attribute that describes the contact person is `Sensitive`. That masks the value in " +
			"CLI output only: the data still lives in the state file, so treat that file as personal " +
			"data. Practitioners who want stronger masking can add `sensitive = true` to their own " +
			"contact input variables.\n\n" +
			"Write the canonical form the API stores: `country` is an uppercase two-letter code, and " +
			"the optional string attributes are omitted rather than set to an empty string. " +
			"Non-canonical values are rejected at validate time, both when creating and when " +
			"updating.\n\n" +
			"`nickname` can only be set when a profile is created. The API ignores it on update, so " +
			"changing the planned nickname of an existing profile is rejected at plan time; replace " +
			"the profile to change it.\n\n" +
			"Destroying the resource deletes the profile. The API refuses to delete a profile still " +
			"associated with a domain, and the account's default profile cannot be deleted; the error " +
			"is surfaced unchanged, and the fix is to reassign the domain's contacts first — which is " +
			"what the dependency graph expresses when a domain's contacts reference this resource.",
		Attributes: map[string]schema.Attribute{
			"first_name": schema.StringAttribute{
				Required:            true,
				Sensitive:           true,
				Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
				MarkdownDescription: "The registrant's first name.",
			},
			"last_name": schema.StringAttribute{
				Required:            true,
				Sensitive:           true,
				Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
				MarkdownDescription: "The registrant's last name.",
			},
			"address": schema.StringAttribute{
				Required:            true,
				Sensitive:           true,
				Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
				MarkdownDescription: "The registrant's street address.",
			},
			"address2": schema.StringAttribute{
				Optional:  true,
				Sensitive: true,
				Validators: []validator.String{
					stringvalidator.LengthAtMost(128),
					nonEmptyString(),
				},
				MarkdownDescription: "The second line of the registrant's street address, e.g. a suite " +
					"or apartment number. Optional; the API reports it as an empty element when unset, " +
					"which this resource stores as null. Omit it instead of setting an empty string.",
			},
			"city": schema.StringAttribute{
				Required:            true,
				Sensitive:           true,
				Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
				MarkdownDescription: "The registrant's city.",
			},
			"state": schema.StringAttribute{
				Required:            true,
				Sensitive:           true,
				Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
				MarkdownDescription: "The registrant's state, province, or region.",
			},
			"zip": schema.StringAttribute{
				Required:            true,
				Sensitive:           true,
				Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
				MarkdownDescription: "The registrant's postal code.",
			},
			"country": schema.StringAttribute{
				Required:  true,
				Sensitive: true,
				Validators: []validator.String{
					stringvalidator.RegexMatches(iso3166Alpha2Pattern,
						"use an uppercase two-letter ISO 3166-1 alpha-2 country code, e.g. US or GB"),
				},
				MarkdownDescription: "The registrant's country as an ISO 3166-1 alpha-2 code, e.g. `US` " +
					"or `GB`. Exactly two uppercase letters: a lowercase or other-case value is " +
					"rejected at validate time, and no case correction is applied.",
			},
			"email": schema.StringAttribute{
				Required:   true,
				Sensitive:  true,
				Validators: []validator.String{stringvalidator.LengthAtLeast(1)},
				MarkdownDescription: "The registrant's email address. NameSilo sends contact " +
					"verification and renewal notices here.",
			},
			"phone": schema.StringAttribute{
				Required:            true,
				Sensitive:           true,
				Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
				MarkdownDescription: "The registrant's phone number, including the country code.",
			},
			"fax": schema.StringAttribute{
				Optional:  true,
				Sensitive: true,
				Validators: []validator.String{
					stringvalidator.LengthAtMost(32),
					nonEmptyString(),
				},
				MarkdownDescription: "The registrant's fax number. Optional; omit it instead of " +
					"setting an empty string.",
			},
			"company": schema.StringAttribute{
				Optional:  true,
				Sensitive: true,
				Validators: []validator.String{
					stringvalidator.LengthAtMost(64),
					nonEmptyString(),
				},
				MarkdownDescription: "The registrant's company name. Optional; omit it instead of " +
					"setting an empty string.",
			},
			"nickname": schema.StringAttribute{
				Optional:  true,
				Sensitive: true,
				Validators: []validator.String{
					stringvalidator.LengthAtMost(24),
					nonEmptyString(),
				},
				MarkdownDescription: "A label for the profile in NameSilo's UI. Optional, and " +
					"informational: it does not appear in WHOIS output. It can only be set when the " +
					"profile is created: the API ignores it on update, so changing it on an existing " +
					"profile is rejected at plan time and needs a replacement.",
			},
			"us_nexus_category": schema.StringAttribute{
				Optional:  true,
				Sensitive: true,
				Validators: []validator.String{
					stringvalidator.LengthAtMost(3),
					nonEmptyString(),
				},
				MarkdownDescription: "The US Nexus category for a `.us` registrant, e.g. `C11`. " +
					"Optional; needed only for `.us` domains.",
			},
			"us_application_purpose": schema.StringAttribute{
				Optional:  true,
				Sensitive: true,
				Validators: []validator.String{
					stringvalidator.LengthAtMost(2),
					nonEmptyString(),
				},
				MarkdownDescription: "The US application purpose for a `.us` registrant, e.g. `P1`. " +
					"Optional; needed only for `.us` domains.",
			},
			"ca_legal_form": schema.StringAttribute{
				Optional:            true,
				Sensitive:           true,
				Validators:          []validator.String{nonEmptyString()},
				MarkdownDescription: "The legal form CIRA requires for a `.ca` registrant. Optional.",
			},
			"ca_language": schema.StringAttribute{
				Optional:            true,
				Sensitive:           true,
				Validators:          []validator.String{nonEmptyString()},
				MarkdownDescription: "The preferred correspondence language for a `.ca` registrant. Optional.",
			},
			"ca_agreement_version": schema.StringAttribute{
				Optional:            true,
				Sensitive:           true,
				Validators:          []validator.String{nonEmptyString()},
				MarkdownDescription: "The CIRA registrant agreement version accepted for a `.ca` registrant. Optional.",
			},
			"ca_whois_display": schema.StringAttribute{
				Optional:            true,
				Sensitive:           true,
				Validators:          []validator.String{nonEmptyString()},
				MarkdownDescription: "The CIRA WHOIS display preference for a `.ca` registrant. Optional.",
			},
			"eu_citizenship_country": schema.StringAttribute{
				Optional:            true,
				Sensitive:           true,
				Validators:          []validator.String{nonEmptyString()},
				MarkdownDescription: "The citizenship country a `.eu` registrant qualifies under. Optional.",
			},
			"default_profile": schema.BoolAttribute{
				Computed: true,
				MarkdownDescription: "Whether this is the account's default profile (`1` in the API). " +
					"Computed and informational: these operations cannot set or clear it. Create " +
					"writes `false` without a second API call and the refresh that follows corrects " +
					"it, which produces no diff because the attribute is computed.",
			},
			"id": schema.StringAttribute{
				Computed: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
				MarkdownDescription: "The API's `contact_id`. It is deliberately not `Sensitive`: it " +
					"is an opaque account-scoped identifier, needed as a plain reference and as the " +
					"import value. `domain` does not exist on this resource.",
			},
		},
	}
}

// Configure receives the provider's *namesilo.Client. A wrong type is a
// provider bug, and the diagnostic names the type actually received.
func (r *contactResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	client, ok := req.ProviderData.(*namesilo.Client)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected resource Configure type",
			fmt.Sprintf("Expected *namesilo.Client, got %T. This is a provider bug; please report it.", req.ProviderData),
		)
		return
	}
	r.client = client
}

// Create adds the profile and stores the returned contact_id as id.
// default_profile is written as false without a second API call: the refresh
// that follows the apply corrects it, and because the attribute is computed
// that correction produces no diff. A second call was rejected because a
// failure after a successful add would orphan the profile with no ID in state
// (§6.4).
func (r *contactResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan contactResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	id, err := r.client.AddContact(ctx, modelToContact(plan))
	if err != nil {
		addAPIError(&resp.Diagnostics, "namesilo_contact", err)
		return
	}

	plan.ID = types.StringValue(id)
	plan.DefaultProfile = types.BoolValue(false)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Read lists the single profile named by id. Zero profiles means the profile
// no longer exists, so it is removed from state with a warning rather than
// left to fail every future plan — the one exception to the rule that Read
// never drops state on a successful call (§11). More than one profile is
// impossible for a contact_id and is reported as a provider bug.
func (r *contactResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state contactResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	contacts, err := r.client.ListContacts(ctx, state.ID.ValueString())
	if err != nil {
		addAPIError(&resp.Diagnostics, "namesilo_contact", err)
		return
	}

	switch len(contacts) {
	case 0:
		resp.State.RemoveResource(ctx)
		resp.Diagnostics.AddWarning(
			"Contact profile not found",
			"The contact profile "+state.ID.ValueString()+" no longer exists in the NameSilo account, "+
				"so it has been removed from state. If it was deleted outside Terraform, the next apply "+
				"recreates it from configuration.",
		)
	case 1:
		state = contactToModel(contacts[0])
		resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
	default:
		resp.Diagnostics.AddError(
			"Multiple contact profiles returned",
			fmt.Sprintf("contactList returned %d profiles for contact_id %s, but it must return at "+
				"most one. This is a provider bug; please report it.", len(contacts), state.ID.ValueString()),
		)
	}
}

// Update sends every managed field with the id from state. default_profile is
// carried from state because contactUpdate cannot set it and the plan value is
// unknown during an update; domain does not exist here, so Update never sees a
// replacement.
func (r *contactResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan contactResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var state contactResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	plan.ID = state.ID
	plan.DefaultProfile = state.DefaultProfile

	if err := r.client.UpdateContact(ctx, modelToContact(plan)); err != nil {
		addAPIError(&resp.Diagnostics, "namesilo_contact", err)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Delete removes the profile. A profile still associated with a domain is
// refused by the API, and the error is surfaced unchanged so the fix (reassign
// the domain's contacts first) is visible; state is kept, so the destroy can
// be retried.
func (r *contactResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state contactResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.client.DeleteContact(ctx, state.ID.ValueString()); err != nil {
		addAPIError(&resp.Diagnostics, "namesilo_contact", err)
		return
	}
}

// ImportState takes the contact_id as the import ID, seeds id from it, and
// lets Read fill every field from contactList (§10). It passes the id through
// only: there is no name-based adoption, so the import ID is the only handle.
func (r *contactResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// ModifyPlan normalizes the configured shape to what Read writes: optional
// fields whose planned value is "" become null, and country is uppercased.
// Input that is already canonical is unchanged, and unknown values are skipped,
// not guessed — the apply resolves them (§6.8).
//
// OpenTofu core forbids a provider from planning a value that differs from a
// known configuration value (internal/plans/objchange/assertPlannedValueValid);
// a non-null prior is the excepted case. These attributes also carry
// validators, so non-canonical input is rejected at validate time before it
// reaches the plan. The uppercasing stays because it is what makes an update
// against existing state converge; on create there is nothing to normalize
// against. This mirrors the spec's §6.8 and the Task 11 report.
func (r *contactResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() {
		return
	}

	var plan contactResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// nickname is create-only. The API honors nn on contactAdd but ignores it
	// on contactUpdate, so an update that changes it would report success and
	// then refresh back to the old value, leaving a perpetual diff. A non-null
	// prior state means this is an update (a create has null state and a
	// destroy has a null plan, both exempt). The comparison treats null and ""
	// as the same, matching the normalized state form.
	if !req.State.Raw.IsNull() {
		var state contactResourceModel
		resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
		if resp.Diagnostics.HasError() {
			return
		}
		if nicknameChanged(plan.Nickname, state.Nickname) {
			resp.Diagnostics.AddAttributeError(
				path.Root("nickname"),
				"Nickname cannot be changed after creation",
				"The NameSilo API ignores the nickname on contactUpdate: it can only be set when a "+
					"profile is created. To change it, replace the profile instead of updating it, for "+
					"example with `tofu apply -replace=namesilo_contact.<name>`.",
			)
		}
		if resp.Diagnostics.HasError() {
			return
		}
	}

	// Only the normalized attributes are written back, so the framework's
	// planning for the computed id and default_profile is left alone.
	optionals := []struct {
		name  string
		value types.String
	}{
		{"address2", plan.Address2},
		{"fax", plan.Fax},
		{"company", plan.Company},
		{"nickname", plan.Nickname},
		{"us_nexus_category", plan.UsNexusCategory},
		{"us_application_purpose", plan.UsApplicationPurpose},
		{"ca_legal_form", plan.CaLegalForm},
		{"ca_language", plan.CaLanguage},
		{"ca_agreement_version", plan.CaAgreementVersion},
		{"ca_whois_display", plan.CaWhoisDisplay},
		{"eu_citizenship_country", plan.EuCitizenshipCountry},
	}
	for _, optional := range optionals {
		normalized := normalizeOptionalString(optional.value)
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root(optional.name), normalized)...)
	}

	if !plan.Country.IsNull() && !plan.Country.IsUnknown() {
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("country"),
			types.StringValue(strings.ToUpper(plan.Country.ValueString())))...)
	}
}

// nicknameChanged reports whether a planned nickname differs from the prior
// one. Unknown values are left for the apply to resolve and never count as a
// change. A null value and an empty string compare equal, because "" is the
// shape a configured empty optional normalizes to and the API reports as
// empty; the validator rejects an explicit "" anyway.
func nicknameChanged(planned, prior types.String) bool {
	if planned.IsUnknown() || prior.IsUnknown() {
		return false
	}
	return planned.ValueString() != prior.ValueString()
}

// normalizeOptionalString maps a known empty planned value to null, the shape
// Read writes, so a canonical plan does not diff against the null the API's
// empty element produces. Unknown values are returned unchanged. The optional
// attributes also carry nonEmptyString, so an explicit "" is rejected at
// validate time; this branch is the update-path fallback that keeps the plan
// quiet. See ModifyPlan for the core limitation on primitives.
func normalizeOptionalString(value types.String) types.String {
	if value.IsNull() || value.IsUnknown() {
		return value
	}
	if value.ValueString() == "" {
		return types.StringNull()
	}
	return value
}

// nonEmptyString rejects an explicitly configured empty string. An Optional
// attribute expresses "unset" as null, which a configuration spells by omitting
// the attribute; setting it to "" is a different value that would reach the API
// as a value, or be dropped and leave a perpetual diff. Null and unknown values
// pass, so omission is accepted. Shared by namesilo_contact's optional strings
// and namesilo_domain_contacts' roles.
func nonEmptyString() validator.String {
	return nonEmptyStringValidator{}
}

type nonEmptyStringValidator struct{}

func (nonEmptyStringValidator) Description(context.Context) string {
	return "value must not be an empty string; omit the attribute instead"
}

func (v nonEmptyStringValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (nonEmptyStringValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	if req.ConfigValue.ValueString() == "" {
		resp.Diagnostics.AddAttributeError(
			req.Path,
			"Invalid Attribute Value",
			"An empty string is not a valid value. Omit the attribute instead of setting an empty string.",
		)
	}
}
