// SPDX-License-Identifier: GPL-3.0-or-later

package provider

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/nijave/terraform-provider-namesilo/internal/namesilo"
)

// DomainContactsPropagationPollInterval and DomainContactsPropagationTimeout
// bound the wait for a contactDomainAssociate write to appear in
// getDomainInfo. contactDomainAssociate succeeds before its change is visible
// in a read: the real API propagates over several seconds (measured: a read two
// seconds after the write still reported the old role, and it landed within
// tens of seconds), so Create and Update poll rather than trust the first
// post-write read. They are variables, not constants, so the hermetic tests can
// shrink them; production keeps a 2-second interval and a 60-second bound.
var (
	DomainContactsPropagationPollInterval = 2 * time.Second
	DomainContactsPropagationTimeout      = 60 * time.Second
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
//
// contactDomainAssociate succeeds before its change is visible in
// getDomainInfo: the association propagates over several seconds, so a read
// taken immediately after the write can still report the old role. Create and
// Update send the write and then wait for it: they poll getDomainInfo until
// every role they just wrote matches, bounded by
// DomainContactsPropagationTimeout. When the wait confirms propagation they
// fill state from that final read, so the immediate post-apply refresh sees no
// phantom change. If the window elapses first they fall back to the plan's
// values for the managed roles (and one getDomainInfo for any unknowns) and
// emit a warning, because the association is still propagating and the next
// refresh will confirm it. Read remains the authority and refreshes all four
// roles; drift that this leaves in state (an out-of-band change between
// refreshes) self-heals on the next refresh rather than being reported as an
// inconsistent result after apply.
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
				Validators: []validator.String{nonEmptyString()},
				MarkdownDescription: "The `contact_id` of the registrant profile. Optional; when it " +
					"is omitted the role is left alone and the API's current value is stored as " +
					"computed. Omitting it is how to stop managing the role; do not set an empty " +
					"string. The registrant is the role most likely to be restricted: changing it " +
					"can trigger a registry contact-verification email and, for some TLDs, is rejected " +
					"outright, with the API error surfaced unchanged.",
			},
			"administrative": schema.StringAttribute{
				Optional: true,
				Computed: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
				Validators: []validator.String{nonEmptyString()},
				MarkdownDescription: "The `contact_id` of the administrative profile. Optional; " +
					"when it is omitted the role is left alone and the API's current value is stored " +
					"as computed. Omit it to stop managing the role; an empty string is rejected.",
			},
			"technical": schema.StringAttribute{
				Optional: true,
				Computed: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
				Validators: []validator.String{nonEmptyString()},
				MarkdownDescription: "The `contact_id` of the technical profile. Optional; when it " +
					"is omitted the role is left alone and the API's current value is stored as " +
					"computed. Omit it to stop managing the role; an empty string is rejected.",
			},
			"billing": schema.StringAttribute{
				Optional: true,
				Computed: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
				Validators: []validator.String{nonEmptyString()},
				MarkdownDescription: "The `contact_id` of the billing profile. Optional; when it is " +
					"omitted the role is left alone and the API's current value is stored as computed. " +
					"Omit it to stop managing the role; an empty string is rejected.",
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

// Create sends the roles present in configuration in one
// contactDomainAssociate call and then waits for the write to propagate. When
// the wait confirms propagation, all four roles come from the final
// getDomainInfo (they are current and truthful then); when it times out, the
// managed roles keep their planned values and the omitted roles, which are
// Computed and unknown in a Create plan, are filled from one getDomainInfo call
// so every value is known after apply. A warning names any role still lagging,
// but a warning does not fail the apply.
//
// When configuration manages no roles, nothing is sent and nothing is waited
// for: the omitted roles are simply filled from one getDomainInfo call.
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

	plan.ID = plan.Domain
	info, confirmed, err := r.applyAssociatedRoles(ctx, &resp.Diagnostics, plan.Domain.ValueString(), configuredRoles(config, plan))
	if err != nil {
		addAPIError(&resp.Diagnostics, "namesilo_domain_contacts", err)
		return
	}
	if confirmed {
		applyRoles(&plan, info.Contacts)
	} else if err := r.fillUnmanagedRoles(ctx, &config, &plan); err != nil {
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

// refreshRoles fills all four role attributes from getDomainInfo's contact_ids.
// It is Read's refresh, and Read is the authority for out-of-band changes.
// Create and Update fill from the getDomainInfo that confirmed their write.
// Null and unknown input values are overwritten, so every role in the caller's
// model becomes the API's current value.
func (r *domainContactsResource) refreshRoles(ctx context.Context, model *domainContactsResourceModel) error {
	info, err := r.client.GetDomainInfo(ctx, model.Domain.ValueString())
	if err != nil {
		return err
	}
	applyRoles(model, info.Contacts)
	return nil
}

// fillUnmanagedRoles fills only the roles the configuration leaves unmanaged
// (null in config, and therefore unknown in a Create plan) from one
// getDomainInfo call, leaving the managed roles at the plan's configured
// values. It makes no call when every role is configured. Create calls it when
// its propagation wait did not confirm (including the no-roles-sent path);
// Update does not, because Update keeps the plan's values for all four roles
// and lets the next Read pick up anything that changed out of band.
func (r *domainContactsResource) fillUnmanagedRoles(ctx context.Context, config, plan *domainContactsResourceModel) error {
	if !config.Registrant.IsNull() && !config.Administrative.IsNull() &&
		!config.Technical.IsNull() && !config.Billing.IsNull() {
		return nil
	}
	info, err := r.client.GetDomainInfo(ctx, plan.Domain.ValueString())
	if err != nil {
		return err
	}
	if config.Registrant.IsNull() {
		plan.Registrant = nullIfEmpty(info.Contacts.Registrant)
	}
	if config.Administrative.IsNull() {
		plan.Administrative = nullIfEmpty(info.Contacts.Administrative)
	}
	if config.Technical.IsNull() {
		plan.Technical = nullIfEmpty(info.Contacts.Technical)
	}
	if config.Billing.IsNull() {
		plan.Billing = nullIfEmpty(info.Contacts.Billing)
	}
	return nil
}

// Update sends only the roles present in configuration, with the plan's
// values. A role that was dropped from configuration is not sent: omitting a
// role leaves the API's association untouched, which is the only thing the API
// supports. A change to domain plans a replacement, so Update never sees one.
//
// Like Create it waits for the write to propagate and, when the wait confirms,
// fills all four roles from the final getDomainInfo. On timeout it keeps the
// plan's values: the managed roles from configuration and the unmanaged roles
// from prior state, so all four are known. The next Read is the authority and
// picks up any out-of-band change then.
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

	plan.ID = plan.Domain
	info, confirmed, err := r.applyAssociatedRoles(ctx, &resp.Diagnostics, plan.Domain.ValueString(), configuredRoles(config, plan))
	if err != nil {
		addAPIError(&resp.Diagnostics, "namesilo_domain_contacts", err)
		return
	}
	if confirmed {
		applyRoles(&plan, info.Contacts)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// applyAssociatedRoles sends roles in one contactDomainAssociate call, waits
// for the write to appear in getDomainInfo, and reports how it settled. It
// returns the final getDomainInfo and true when the wait confirmed propagation;
// on timeout it returns false, the getDomainInfo from the last poll (whose
// managed roles may still be stale), and adds a warning naming the roles that
// are still lagging. A zero roles value sends nothing, waits for nothing, and
// returns false with no warning, so Create's unmanaged-configuration path does
// not block.
func (r *domainContactsResource) applyAssociatedRoles(ctx context.Context, diags *diag.Diagnostics, domain string, roles namesilo.ContactRoles) (namesilo.DomainInfo, bool, error) {
	if roles == (namesilo.ContactRoles{}) {
		return namesilo.DomainInfo{}, false, nil
	}
	if err := r.client.AssociateContacts(ctx, domain, roles); err != nil {
		return namesilo.DomainInfo{}, false, err
	}
	info, lagging, err := r.waitForContactsPropagation(ctx, domain, roles)
	if err != nil {
		return namesilo.DomainInfo{}, false, err
	}
	if len(lagging) == 0 {
		return info, true, nil
	}
	diags.AddWarning(
		"Contact association is still propagating",
		fmt.Sprintf("The roles %s for %s have not appeared in the API's domain information yet. "+
			"contactDomainAssociate succeeds before its change is visible in a read, so the "+
			"association is still propagating; the next refresh will confirm it. State keeps the "+
			"values that were written until then.",
			strings.Join(lagging, ", "), domain),
	)
	return info, false, nil
}

// waitForContactsPropagation polls getDomainInfo until every role named in want
// (its non-empty fields) has the value that was just written, or
// DomainContactsPropagationTimeout elapses. It returns the final getDomainInfo
// and the names of the roles still lagging; the slice is empty when propagation
// was confirmed. Context cancellation between polls is returned as an error, so
// a cancelled apply stops promptly instead of waiting out the window.
//
// The window is the resource's documented propagation window: the live API
// measured several seconds, so the default 2-second interval and 60-second
// bound leave room for tens of seconds of lag without blocking forever.
func (r *domainContactsResource) waitForContactsPropagation(ctx context.Context, domain string, want namesilo.ContactRoles) (namesilo.DomainInfo, []string, error) {
	deadline := time.Now().Add(DomainContactsPropagationTimeout)
	for {
		info, err := r.client.GetDomainInfo(ctx, domain)
		if err != nil {
			return namesilo.DomainInfo{}, nil, err
		}
		lagging := laggingRoles(info.Contacts, want)
		if len(lagging) == 0 || !time.Now().Before(deadline) {
			return info, lagging, nil
		}
		timer := time.NewTimer(DomainContactsPropagationPollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return namesilo.DomainInfo{}, nil, ctx.Err()
		case <-timer.C:
		}
	}
}

// laggingRoles names the roles in want (its non-empty fields) whose value in
// have differs. The names are the resource's attribute names, so a warning can
// name them without translation.
func laggingRoles(have, want namesilo.ContactRoles) []string {
	var lagging []string
	if want.Registrant != "" && have.Registrant != want.Registrant {
		lagging = append(lagging, "registrant")
	}
	if want.Administrative != "" && have.Administrative != want.Administrative {
		lagging = append(lagging, "administrative")
	}
	if want.Technical != "" && have.Technical != want.Technical {
		lagging = append(lagging, "technical")
	}
	if want.Billing != "" && have.Billing != want.Billing {
		lagging = append(lagging, "billing")
	}
	return lagging
}

// applyRoles overwrites all four roles in model with the API's current values,
// mapping the API's empty strings to null. It is the mapping Refresh uses;
// Create and Update call it with the getDomainInfo that confirmed their write,
// so all four roles are current and truthful.
func applyRoles(model *domainContactsResourceModel, roles namesilo.ContactRoles) {
	model.Registrant = nullIfEmpty(roles.Registrant)
	model.Administrative = nullIfEmpty(roles.Administrative)
	model.Technical = nullIfEmpty(roles.Technical)
	model.Billing = nullIfEmpty(roles.Billing)
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
