// SPDX-License-Identifier: GPL-3.0-or-later

package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/nijave/terraform-provider-namesilo/internal/namesilo"
)

var (
	_ resource.Resource                = (*domainContactsResource)(nil)
	_ resource.ResourceWithConfigure   = (*domainContactsResource)(nil)
	_ resource.ResourceWithImportState = (*domainContactsResource)(nil)
)

// domainContactsResource manages one domain's four contact associations —
// registrant, administrative, technical, and billing — as references to
// account-level contact profiles. The profiles are namesilo_contact resources;
// this resource owns only the references, because a profile is account-scoped
// and can be shared by many domains (§6.5).
//
// Management is partial by design (§16). A role omitted from configuration is
// left alone and its API value is stored as computed, so an update cannot
// mistake "I copied the current value" for "set this role". Destroying the
// resource stops managing the associations and leaves them in place: the API
// has no disassociate operation and a domain must always have all four roles
// (§4 invariant 10).
type domainContactsResource struct {
	client *namesilo.Client
}

// NewDomainContactsResource returns the namesilo_domain_contacts resource.
func NewDomainContactsResource() resource.Resource {
	return &domainContactsResource{}
}

// domainContactsResourceModel is namesilo_domain_contacts' state model. The
// four roles are Optional and Computed: configured roles are managed, and an
// omitted role carries the API's current value without diffing.
type domainContactsResourceModel struct {
	Domain         types.String `tfsdk:"domain"`
	Registrant     types.String `tfsdk:"registrant"`
	Administrative types.String `tfsdk:"administrative"`
	Technical      types.String `tfsdk:"technical"`
	Billing        types.String `tfsdk:"billing"`
	ID             types.String `tfsdk:"id"`
}

func (r *domainContactsResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_domain_contacts"
}

func (r *domainContactsResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Associates a domain's four contact roles — registrant, administrative, " +
			"technical, and billing — with account-level contact profiles identified by `contact_id`. " +
			"The profiles themselves are `namesilo_contact` resources; this resource manages only the " +
			"references, because a profile is account-scoped and can be shared by many domains.\n\n" +
			"Partial management is deliberate. A role that is omitted from configuration is left alone: " +
			"it is `Computed`, so the API's current value is stored and does not appear as drift. Removing " +
			"a role from configuration stops managing it — it does not reassign the domain — because the " +
			"API has no disassociate operation and a domain must always have all four roles.\n\n" +
			"Changing the registrant contact can trigger a registry contact-verification email and, for " +
			"some TLDs, is restricted; when the registry rejects the change, the API error is surfaced " +
			"unchanged.\n\n" +
			"Destroying this resource stops managing the associations and leaves them in place: there is " +
			"nothing to undo, because the API cannot clear a role.",
		Attributes: map[string]schema.Attribute{
			"domain": schema.StringAttribute{
				Required: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
				MarkdownDescription: "The domain whose contact associations to manage, e.g. " +
					"`example.com`. Changing it forces a replacement: the resource is pointed at a " +
					"different domain rather than renamed. Also the import ID.",
			},
			"registrant": schema.StringAttribute{
				Optional: true,
				Computed: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
				MarkdownDescription: "The `contact_id` of the registrant profile. Optional; when it " +
					"is omitted the role is left alone and the API's current value is stored as " +
					"computed. The registrant is the role most likely to be restricted: changing it " +
					"can trigger a registry contact-verification email and, for some TLDs, is rejected " +
					"outright, with the API error surfaced unchanged.",
			},
			"administrative": schema.StringAttribute{
				Optional: true,
				Computed: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
				MarkdownDescription: "The `contact_id` of the administrative profile. Optional; " +
					"when it is omitted the role is left alone and the API's current value is stored " +
					"as computed.",
			},
			"technical": schema.StringAttribute{
				Optional: true,
				Computed: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
				MarkdownDescription: "The `contact_id` of the technical profile. Optional; when it " +
					"is omitted the role is left alone and the API's current value is stored as " +
					"computed.",
			},
			"billing": schema.StringAttribute{
				Optional: true,
				Computed: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
				MarkdownDescription: "The `contact_id` of the billing profile. Optional; when it is " +
					"omitted the role is left alone and the API's current value is stored as computed.",
			},
			"id": schema.StringAttribute{
				Computed: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
				MarkdownDescription: "The domain name. It is inert (Terraform keys state by resource " +
					"address), it makes `tofu state show` readable, and it is the import ID.",
			},
		},
	}
}

// Configure receives the provider's *namesilo.Client. A wrong type is a
// provider bug, and the diagnostic names the type actually received.
func (r *domainContactsResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

// Create sends the roles that are present in configuration in one
// contactDomainAssociate call, then stores the refreshed state. The omitted
// roles are Computed, so they are filled from getDomainInfo's contact_ids
// before state is returned: OpenTofu requires every value to be known after
// apply, so leaving them unknown for the framework's refresh to resolve is
// rejected.
func (r *domainContactsResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan domainContactsResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var config domainContactsResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if roles := configuredRoles(config, plan); roles != (namesilo.ContactRoles{}) {
		if err := r.client.AssociateContacts(ctx, plan.Domain.ValueString(), roles); err != nil {
			addAPIError(&resp.Diagnostics, "namesilo_domain_contacts", err)
			return
		}
	}

	plan.ID = plan.Domain
	if err := r.refreshRoles(ctx, &plan); err != nil {
		addAPIError(&resp.Diagnostics, "namesilo_domain_contacts", err)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Read refreshes all four roles from getDomainInfo's contact_ids. It writes
// every role, managed or not, so state mirrors the API; an unmanaged role
// changing out of band is tracked in state without ever being planned as a
// change. API errors are diagnostics and leave state untouched (§11).
func (r *domainContactsResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state domainContactsResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.refreshRoles(ctx, &state); err != nil {
		addAPIError(&resp.Diagnostics, "namesilo_domain_contacts", err)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// refreshRoles fills all four role attributes from getDomainInfo's contact_ids,
// the read Read performs. Create and Update call it after their write so state
// is complete before it is returned: the association call returns no contacts,
// and OpenTofu requires every attribute to be known after apply. Null and
// unknown input values are overwritten, so the caller's managed roles are
// replaced by the API's authoritative values.
func (r *domainContactsResource) refreshRoles(ctx context.Context, model *domainContactsResourceModel) error {
	info, err := r.client.GetDomainInfo(ctx, model.Domain.ValueString())
	if err != nil {
		return err
	}
	model.Registrant = nullIfEmpty(info.Contacts.Registrant)
	model.Administrative = nullIfEmpty(info.Contacts.Administrative)
	model.Technical = nullIfEmpty(info.Contacts.Technical)
	model.Billing = nullIfEmpty(info.Contacts.Billing)
	return nil
}

// Update sends only the roles present in configuration, with the plan's
// values. A role that was dropped from configuration is not sent: omitting a
// role leaves the API's association untouched, which is the only thing the API
// supports. A change to domain plans a replacement, so Update never sees one.
func (r *domainContactsResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan domainContactsResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var config domainContactsResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if roles := configuredRoles(config, plan); roles != (namesilo.ContactRoles{}) {
		if err := r.client.AssociateContacts(ctx, plan.Domain.ValueString(), roles); err != nil {
			addAPIError(&resp.Diagnostics, "namesilo_domain_contacts", err)
			return
		}
	}

	plan.ID = plan.Domain
	if err := r.refreshRoles(ctx, &plan); err != nil {
		addAPIError(&resp.Diagnostics, "namesilo_domain_contacts", err)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Delete is a no-op (§6.5, §4 invariant 10). The API has no disassociate
// operation and a domain must always have all four roles, so there is nothing
// to undo: destroying the resource stops managing the association and leaves it
// in place.
func (r *domainContactsResource) Delete(_ context.Context, _ resource.DeleteRequest, _ *resource.DeleteResponse) {
}

// ImportState takes the domain as the import ID, seeds id from it, and lets
// Read fill all four roles from getDomainInfo (§6.5, §10).
func (r *domainContactsResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("domain"), req.ID)...)
}

// configuredRoles builds the association request from the configuration's
// non-null roles and the plan's values. Presence is read from config because
// an omitted role is null there, while it is unknown in the plan (the attribute
// is Optional and Computed); the value is read from the plan because that is
// where the resolved contact ID lives. Omitted roles are left out, and the
// client turns that into an absent parameter, which the API reads as "leave
// this role alone" (§6.5).
func configuredRoles(config, plan domainContactsResourceModel) namesilo.ContactRoles {
	var roles namesilo.ContactRoles
	if !config.Registrant.IsNull() {
		roles.Registrant = plan.Registrant.ValueString()
	}
	if !config.Administrative.IsNull() {
		roles.Administrative = plan.Administrative.ValueString()
	}
	if !config.Technical.IsNull() {
		roles.Technical = plan.Technical.ValueString()
	}
	if !config.Billing.IsNull() {
		roles.Billing = plan.Billing.ValueString()
	}
	return roles
}
