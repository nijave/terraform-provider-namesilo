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
	_ resource.Resource                = (*autoRenewResource)(nil)
	_ resource.ResourceWithConfigure   = (*autoRenewResource)(nil)
	_ resource.ResourceWithImportState = (*autoRenewResource)(nil)
)

// autoRenewResource manages one domain's auto-renew flag. It is a
// desired-state boolean, not a presence-only resource: enabled = false is a
// valid declaration that the domain must be left to expire, and removing the
// resource stops renewal — the most consequential destroy in the provider,
// because a domain without renewal can be lost (§16).
type autoRenewResource struct {
	client *namesilo.Client
}

// NewAutoRenewResource returns the namesilo_auto_renew resource.
func NewAutoRenewResource() resource.Resource {
	return &autoRenewResource{}
}

// autoRenewResourceModel is namesilo_auto_renew's state model.
type autoRenewResourceModel struct {
	Domain  types.String `tfsdk:"domain"`
	Enabled types.Bool   `tfsdk:"enabled"`
	ID      types.String `tfsdk:"id"`
}

func (r *autoRenewResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_auto_renew"
}

func (r *autoRenewResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages one domain's auto-renew — NameSilo's flag that renews the " +
			"domain at expiry and charges the account for the renewal.\n\n" +
			"`enabled` is the desired state, so resource existence does not imply auto-renew is on: " +
			"`enabled = false` keeps this resource and declares that the domain must be left to " +
			"expire, rather than deleting the resource to turn renewal off. Destroy stops automatic " +
			"renewal only when the state says it was on — removing the resource stops renewal, and " +
			"a domain left to expire can be lost entirely. This is the most consequential destroy " +
			"in this provider: the others cost an outage or a missing safety net, this one can " +
			"lose the domain.\n\n" +
			"Auto-renew is not offered by every TLD, and a renewal charges the account when it " +
			"fires; when NameSilo rejects the toggle for that reason, the API error is surfaced " +
			"unchanged, so the registry's own message is what you see. Drift detection is the " +
			"normal refresh: a change made in the web UI is picked up on the next plan and " +
			"corrected.",
		Attributes: map[string]schema.Attribute{
			"domain": schema.StringAttribute{
				Required: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
				MarkdownDescription: "The domain whose auto-renew flag to manage, e.g. `example.com`. " +
					"Changing it forces a replacement: the resource is pointed at a different domain " +
					"rather than renamed. Also the import ID.",
			},
			"enabled": schema.BoolAttribute{
				Required: true,
				MarkdownDescription: "Whether NameSilo should automatically renew the domain at expiry " +
					"and charge the account for the renewal. This is the desired state, not a claim " +
					"about the current one: `enabled = false` keeps the resource and declares the " +
					"domain must be left to expire. Auto-renew is not offered by every TLD, and " +
					"NameSilo's error for those is surfaced unchanged.",
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
func (r *autoRenewResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

// Create turns auto-renew on when enabled is true. When it is false, the
// create records the desired state and makes no API call: leaving a domain to
// expire is the absence of renewal, not an operation (§6.7).
func (r *autoRenewResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan autoRenewResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if plan.Enabled.ValueBool() {
		if err := r.client.AddAutoRenew(ctx, plan.Domain.ValueString()); err != nil {
			addAPIError(&resp.Diagnostics, "namesilo_auto_renew", err)
			return
		}
	}

	plan.ID = plan.Domain
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Read refreshes enabled from getDomainInfo's auto_renew flag. Any API error
// is a diagnostic and leaves state untouched, the way the other resources
// treat NameSilo's code 200 conflation of "expired", "inactive", and "not
// yours" (§11).
func (r *autoRenewResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state autoRenewResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	info, err := r.client.GetDomainInfo(ctx, state.Domain.ValueString())
	if err != nil {
		addAPIError(&resp.Diagnostics, "namesilo_auto_renew", err)
		return
	}

	state.Enabled = types.BoolValue(info.AutoRenew)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Update toggles renewal in the direction of the change, and only when the
// plan and state actually disagree. Both AddAutoRenew and RemoveAutoRenew
// tolerate the API's already-in-state replies (250 and 251, classified as
// success by the client), so a race with the web UI cannot produce a spurious
// failure; the resource adds no special-casing of its own. A change to domain
// plans a replacement, so Update never sees a domain change.
func (r *autoRenewResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan autoRenewResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var state autoRenewResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if plan.Enabled.ValueBool() != state.Enabled.ValueBool() {
		var err error
		if plan.Enabled.ValueBool() {
			err = r.client.AddAutoRenew(ctx, plan.Domain.ValueString())
		} else {
			err = r.client.RemoveAutoRenew(ctx, plan.Domain.ValueString())
		}
		if err != nil {
			addAPIError(&resp.Diagnostics, "namesilo_auto_renew", err)
			return
		}
	}

	plan.ID = plan.Domain
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Delete turns renewal off only when the state says it was enabled (§4
// invariant 15): removing a resource that declared enabled = false leaves the
// domain as it is. On an API error the diagnostic is returned and state is
// kept, so the destroy can be retried instead of leaking a half-torn-down
// resource.
func (r *autoRenewResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state autoRenewResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if !state.Enabled.ValueBool() {
		return
	}
	if err := r.client.RemoveAutoRenew(ctx, state.Domain.ValueString()); err != nil {
		addAPIError(&resp.Diagnostics, "namesilo_auto_renew", err)
		return
	}
}

// ImportState takes the domain as the import ID, seeds id from it, and lets
// Read fill enabled from the API (§10).
func (r *autoRenewResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("domain"), req.ID)...)
}
