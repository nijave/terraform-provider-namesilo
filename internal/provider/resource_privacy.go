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
	_ resource.Resource                = (*privacyResource)(nil)
	_ resource.ResourceWithConfigure   = (*privacyResource)(nil)
	_ resource.ResourceWithImportState = (*privacyResource)(nil)
)

// privacyResource manages one domain's WHOIS privacy. It is a desired-state
// boolean, not a presence-only resource: the resource exists to assert the
// domain's privacy either way, and enabled = false keeps a domain unprivate
// without deleting the resource (§16).
type privacyResource struct {
	client *namesilo.Client
}

// NewPrivacyResource returns the namesilo_privacy resource.
func NewPrivacyResource() resource.Resource {
	return &privacyResource{}
}

// privacyResourceModel is namesilo_privacy's state model.
type privacyResourceModel struct {
	Domain  types.String `tfsdk:"domain"`
	Enabled types.Bool   `tfsdk:"enabled"`
	ID      types.String `tfsdk:"id"`
}

func (r *privacyResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_privacy"
}

func (r *privacyResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages one domain's WHOIS privacy — NameSilo's PrivacyGuardian, which " +
			"replaces the registrant's contact data in public WHOIS output with NameSilo's proxy details, " +
			"hiding registrant contact data from WHOIS lookups.\n\n" +
			"`enabled` is the desired state, so resource existence does not imply privacy is on: " +
			"`enabled = false` keeps this resource and asserts the domain must not use PrivacyGuardian, " +
			"rather than deleting the resource to turn privacy off. Destroy turns privacy off only when " +
			"the state says it was on.\n\n" +
			"Privacy is unavailable or billable for some TLDs; when NameSilo rejects the toggle for that " +
			"reason, the API error is surfaced unchanged, so the registry's own message is what you see. " +
			"Drift detection is the normal refresh: a change made in the web UI is picked up on the next " +
			"plan and corrected.",
		Attributes: map[string]schema.Attribute{
			"domain": schema.StringAttribute{
				Required: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
				MarkdownDescription: "The domain whose WHOIS privacy to manage, e.g. `example.com`. " +
					"Changing it forces a replacement: the resource is pointed at a different domain " +
					"rather than renamed. Also the import ID.",
			},
			"enabled": schema.BoolAttribute{
				Required: true,
				MarkdownDescription: "Whether the domain's WHOIS output should hide registrant contact " +
					"data behind PrivacyGuardian. This is the desired state, not a claim about the " +
					"current one: `enabled = false` keeps the resource and keeps the domain unprivate. " +
					"Privacy is unavailable or billable for some TLDs, and NameSilo's error for those " +
					"is surfaced unchanged.",
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
func (r *privacyResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

// Create turns privacy on when enabled is true. When it is false, the create
// records the desired state and makes no API call: keeping a domain unprivate
// is the absence of privacy, not an operation (§6.3).
func (r *privacyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan privacyResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if plan.Enabled.ValueBool() {
		if err := r.client.AddPrivacy(ctx, plan.Domain.ValueString()); err != nil {
			addAPIError(&resp.Diagnostics, "namesilo_privacy", err)
			return
		}
	}

	plan.ID = plan.Domain
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Read refreshes enabled from getDomainInfo's private flag. Any API error is a
// diagnostic and leaves state untouched, the way the other resources treat
// NameSilo's code 200 conflation of "expired", "inactive", and "not yours"
// (§11).
func (r *privacyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state privacyResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	info, err := r.client.GetDomainInfo(ctx, state.Domain.ValueString())
	if err != nil {
		addAPIError(&resp.Diagnostics, "namesilo_privacy", err)
		return
	}

	state.Enabled = types.BoolValue(info.Private)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Update toggles privacy in the direction of the change, and only when the
// plan and state actually disagree. Both AddPrivacy and RemovePrivacy tolerate
// the API's already-in-state replies (255 and 256, classified as success by
// the client), so a race with the web UI cannot produce a spurious failure;
// the resource adds no special-casing of its own. A change to domain plans a
// replacement, so Update never sees a domain change.
func (r *privacyResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan privacyResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var state privacyResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if plan.Enabled.ValueBool() != state.Enabled.ValueBool() {
		var err error
		if plan.Enabled.ValueBool() {
			err = r.client.AddPrivacy(ctx, plan.Domain.ValueString())
		} else {
			err = r.client.RemovePrivacy(ctx, plan.Domain.ValueString())
		}
		if err != nil {
			addAPIError(&resp.Diagnostics, "namesilo_privacy", err)
			return
		}
	}

	plan.ID = plan.Domain
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Delete turns privacy off only when the state says it was on (§4 invariant
// 7): removing a resource that declared enabled = false leaves the domain as
// it is. On an API error the diagnostic is returned and state is kept, so the
// destroy can be retried instead of leaking a half-torn-down resource.
func (r *privacyResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state privacyResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if !state.Enabled.ValueBool() {
		return
	}
	if err := r.client.RemovePrivacy(ctx, state.Domain.ValueString()); err != nil {
		addAPIError(&resp.Diagnostics, "namesilo_privacy", err)
		return
	}
}

// ImportState takes the domain as the import ID, seeds id from it, and lets
// Read fill enabled from the API (§10).
func (r *privacyResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("domain"), req.ID)...)
}
