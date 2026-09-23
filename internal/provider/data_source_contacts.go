// SPDX-License-Identifier: GPL-3.0-or-later

package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/nijave/terraform-provider-namesilo/internal/namesilo"
)

var (
	_ datasource.DataSource              = (*contactsDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*contactsDataSource)(nil)
)

// contactsDataSource reads every contact profile in the account. Profiles are
// account-level, so the data source has no input; the same GDPR-driven
// Sensitive markings as the namesilo_contact resource apply to the nested
// attributes, because the resource and this data source are the only way to
// read account contacts (§7).
type contactsDataSource struct {
	client *namesilo.Client
}

// NewContactsDataSource returns the namesilo_contacts data source.
func NewContactsDataSource() datasource.DataSource {
	return &contactsDataSource{}
}

// contactProfileModel is one nested contacts element: the profile's contact_id
// and default_profile plus the shared contactFields, mapped through nullIfEmpty
// exactly as the resource's state model is (§6.4).
type contactProfileModel struct {
	contactFields
	ContactID      types.String `tfsdk:"contact_id"`
	DefaultProfile types.Bool   `tfsdk:"default_profile"`
}

// contactsDataSourceModel is namesilo_contacts' state model.
type contactsDataSourceModel struct {
	Contacts types.Set    `tfsdk:"contacts"`
	ID       types.String `tfsdk:"id"`
}

func (d *contactsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_contacts"
}

// contactsPIIMarkdown is the masking note the nested PII attributes share. It
// lives in one place so the wording cannot drift between attributes.
const contactsPIIMarkdown = "Sensitive, like the same attribute of the `namesilo_contact` " +
	"resource: Terraform masks it in CLI output, but the value still lives in the state file, " +
	"so treat that file as personal data."

func (d *contactsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Reads every contact profile in the account. Profiles are account-level " +
			"records, so the data source has no input. The attributes that describe the contact person " +
			"are `Sensitive`, mirroring the `namesilo_contact` resource: Terraform masks them in CLI " +
			"output, but they still live in the state file, so treat that file as personal data.\n\n" +
			"Use it to audit the account's profiles, to find the `contact_id` of a profile for the " +
			"`namesilo_domain_contacts` resource, or to assert on profiles managed outside Terraform.",
		Attributes: map[string]schema.Attribute{
			"contacts": schema.SetNestedAttribute{
				Computed: true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"contact_id": schema.StringAttribute{
							Computed: true,
							MarkdownDescription: "The API's `contact_id`, an opaque account-scoped " +
								"reference. Deliberately not `Sensitive`: it is needed as a plain " +
								"reference, the way the resource's id is.",
						},
						"default_profile": schema.BoolAttribute{
							Computed:            true,
							MarkdownDescription: "Whether this is the account's default profile.",
						},
						"first_name": schema.StringAttribute{
							Computed:            true,
							Sensitive:           true,
							MarkdownDescription: contactsPIIMarkdown,
						},
						"last_name": schema.StringAttribute{
							Computed:            true,
							Sensitive:           true,
							MarkdownDescription: contactsPIIMarkdown,
						},
						"address": schema.StringAttribute{
							Computed:            true,
							Sensitive:           true,
							MarkdownDescription: contactsPIIMarkdown,
						},
						"address2": schema.StringAttribute{
							Computed:            true,
							Sensitive:           true,
							MarkdownDescription: contactsPIIMarkdown,
						},
						"city": schema.StringAttribute{
							Computed:            true,
							Sensitive:           true,
							MarkdownDescription: contactsPIIMarkdown,
						},
						"state": schema.StringAttribute{
							Computed:            true,
							Sensitive:           true,
							MarkdownDescription: contactsPIIMarkdown,
						},
						"zip": schema.StringAttribute{
							Computed:            true,
							Sensitive:           true,
							MarkdownDescription: contactsPIIMarkdown,
						},
						"country": schema.StringAttribute{
							Computed:            true,
							Sensitive:           true,
							MarkdownDescription: contactsPIIMarkdown,
						},
						"email": schema.StringAttribute{
							Computed:            true,
							Sensitive:           true,
							MarkdownDescription: contactsPIIMarkdown,
						},
						"phone": schema.StringAttribute{
							Computed:            true,
							Sensitive:           true,
							MarkdownDescription: contactsPIIMarkdown,
						},
						"fax": schema.StringAttribute{
							Computed:            true,
							Sensitive:           true,
							MarkdownDescription: contactsPIIMarkdown,
						},
						"company": schema.StringAttribute{
							Computed:            true,
							Sensitive:           true,
							MarkdownDescription: contactsPIIMarkdown,
						},
						"nickname": schema.StringAttribute{
							Computed:            true,
							Sensitive:           true,
							MarkdownDescription: contactsPIIMarkdown,
						},
						"us_nexus_category": schema.StringAttribute{
							Computed:            true,
							Sensitive:           true,
							MarkdownDescription: contactsPIIMarkdown,
						},
						"us_application_purpose": schema.StringAttribute{
							Computed:            true,
							Sensitive:           true,
							MarkdownDescription: contactsPIIMarkdown,
						},
						"ca_legal_form": schema.StringAttribute{
							Computed:            true,
							Sensitive:           true,
							MarkdownDescription: contactsPIIMarkdown,
						},
						"ca_language": schema.StringAttribute{
							Computed:            true,
							Sensitive:           true,
							MarkdownDescription: contactsPIIMarkdown,
						},
						"ca_agreement_version": schema.StringAttribute{
							Computed:            true,
							Sensitive:           true,
							MarkdownDescription: contactsPIIMarkdown,
						},
						"ca_whois_display": schema.StringAttribute{
							Computed:            true,
							Sensitive:           true,
							MarkdownDescription: contactsPIIMarkdown,
						},
						"eu_citizenship_country": schema.StringAttribute{
							Computed:            true,
							Sensitive:           true,
							MarkdownDescription: contactsPIIMarkdown,
						},
					},
				},
				MarkdownDescription: "Every profile in the account. Attributes whose value the API " +
					"reports as unset come back null. An account with no profiles yields an empty set.",
			},
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Always `contacts`. It is inert and makes `tofu state show` readable.",
			},
		},
	}
}

// Configure receives the provider's *namesilo.Client. A wrong type is a
// provider bug, and the diagnostic names the type actually received.
func (d *contactsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	client, ok := req.ProviderData.(*namesilo.Client)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected data source Configure type",
			fmt.Sprintf("Expected *namesilo.Client, got %T. This is a provider bug; please report it.", req.ProviderData),
		)
		return
	}
	d.client = client
}

// contactProfileObjectType is the framework type of one nested contacts
// element, spelled to match the nested schema attributes.
func contactProfileObjectType() types.ObjectType {
	return types.ObjectType{AttrTypes: map[string]attr.Type{
		"contact_id":             types.StringType,
		"default_profile":        types.BoolType,
		"first_name":             types.StringType,
		"last_name":              types.StringType,
		"address":                types.StringType,
		"address2":               types.StringType,
		"city":                   types.StringType,
		"state":                  types.StringType,
		"zip":                    types.StringType,
		"country":                types.StringType,
		"email":                  types.StringType,
		"phone":                  types.StringType,
		"fax":                    types.StringType,
		"company":                types.StringType,
		"nickname":               types.StringType,
		"us_nexus_category":      types.StringType,
		"us_application_purpose": types.StringType,
		"ca_legal_form":          types.StringType,
		"ca_language":            types.StringType,
		"ca_agreement_version":   types.StringType,
		"ca_whois_display":       types.StringType,
		"eu_citizenship_country": types.StringType,
	}}
}

// contactProfileToModel maps the client's Contact onto one nested element: the
// shared contact-person fields plus the profile's contact_id and default_profile
// flag. It is a thin adapter over contactFieldsFromContact, so the two models
// cannot drift field-by-field; only the identifier's name differs (the resource
// spells it id, the nested element contact_id).
func contactProfileToModel(contact namesilo.Contact) contactProfileModel {
	return contactProfileModel{
		contactFields:  contactFieldsFromContact(contact),
		ContactID:      types.StringValue(contact.ID),
		DefaultProfile: types.BoolValue(contact.DefaultProfile),
	}
}

// Read lists every profile in the account and writes them as a set. An account
// with no profiles yields an empty set, not an error (§7).
func (d *contactsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var state contactsDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	contacts, err := d.client.ListContacts(ctx, "")
	if err != nil {
		addAPIError(&resp.Diagnostics, "namesilo_contacts", err)
		return
	}

	models := make([]contactProfileModel, 0, len(contacts))
	for _, contact := range contacts {
		models = append(models, contactProfileToModel(contact))
	}
	set, diags := types.SetValueFrom(ctx, contactProfileObjectType(), models)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	state.Contacts = set
	state.ID = types.StringValue("contacts")
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
