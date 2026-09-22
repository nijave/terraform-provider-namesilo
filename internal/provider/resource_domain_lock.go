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
	_ resource.Resource                = (*domainLockResource)(nil)
	_ resource.ResourceWithConfigure   = (*domainLockResource)(nil)
	_ resource.ResourceWithImportState = (*domainLockResource)(nil)
)

// domainLockResource manages one domain's registrar lock. It is a
// desired-state boolean, not a presence-only resource: locked = false is a
// valid declaration that unlocks a domain, and removing the resource releases
// the lock it managed (§16).
type domainLockResource struct {
	client *namesilo.Client
}

// NewDomainLockResource returns the namesilo_domain_lock resource.
func NewDomainLockResource() resource.Resource {
	return &domainLockResource{}
}

// domainLockResourceModel is namesilo_domain_lock's state model.
type domainLockResourceModel struct {
	Domain types.String `tfsdk:"domain"`
	Locked types.Bool   `tfsdk:"locked"`
	ID     types.String `tfsdk:"id"`
}

func (r *domainLockResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_domain_lock"
}

func (r *domainLockResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages one domain's registrar lock — the lock that prevents " +
			"unauthorized transfers of the domain to another registrar.\n\n" +
			"`locked` is the desired state, so resource existence does not imply the domain is locked: " +
			"`locked = false` keeps this resource and declares that the domain must be unlocked, " +
			"rather than deleting the resource to unlock it. Destroy unlocks the domain only when " +
			"the state says it was locked — removing the resource releases the lock, and unlocking " +
			"a domain removes the main safeguard against an unauthorized transfer.\n\n" +
			"Some registry states reject both locking and unlocking (a pending transfer, for " +
			"example); when that happens the API error is surfaced unchanged, so the registry's own " +
			"message is what you see. Drift detection is the normal refresh: a change made in the " +
			"web UI is picked up on the next plan and corrected.",
		Attributes: map[string]schema.Attribute{
			"domain": schema.StringAttribute{
				Required: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
				MarkdownDescription: "The domain whose registrar lock to manage, e.g. `example.com`. " +
					"Changing it forces a replacement: the resource is pointed at a different domain " +
					"rather than renamed. Also the import ID.",
			},
			"locked": schema.BoolAttribute{
				Required: true,
				MarkdownDescription: "Whether the domain is locked against transfer at the registrar. " +
					"This is the desired state, not a claim about the current one: `locked = false` " +
					"keeps the resource and keeps the domain unlocked. Unlocking a domain removes the " +
					"main safeguard against an unauthorized transfer.",
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
func (r *domainLockResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

// Create locks the domain when locked is true. When it is false, the create
// records the desired state and makes no API call: an unlocked domain is the
// absence of a lock, not an operation (§6.6).
func (r *domainLockResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan domainLockResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if plan.Locked.ValueBool() {
		if err := r.client.DomainLock(ctx, plan.Domain.ValueString()); err != nil {
			addAPIError(&resp.Diagnostics, "namesilo_domain_lock", err)
			return
		}
	}

	plan.ID = plan.Domain
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Read refreshes locked from getDomainInfo's locked flag. Any API error is a
// diagnostic and leaves state untouched, the way the other resources treat
// NameSilo's code 200 conflation of "expired", "inactive", and "not yours"
// (§11).
func (r *domainLockResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state domainLockResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	info, err := r.client.GetDomainInfo(ctx, state.Domain.ValueString())
	if err != nil {
		addAPIError(&resp.Diagnostics, "namesilo_domain_lock", err)
		return
	}

	state.Locked = types.BoolValue(info.Locked)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Update toggles the lock in the direction of the change, and only when the
// plan and state actually disagree. Both DomainLock and DomainUnlock tolerate
// the API's already-in-state replies (252 and 253, classified as success by
// the client), so a race with the web UI cannot produce a spurious failure;
// the resource adds no special-casing of its own. A change to domain plans a
// replacement, so Update never sees a domain change.
func (r *domainLockResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan domainLockResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var state domainLockResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if plan.Locked.ValueBool() != state.Locked.ValueBool() {
		var err error
		if plan.Locked.ValueBool() {
			err = r.client.DomainLock(ctx, plan.Domain.ValueString())
		} else {
			err = r.client.DomainUnlock(ctx, plan.Domain.ValueString())
		}
		if err != nil {
			addAPIError(&resp.Diagnostics, "namesilo_domain_lock", err)
			return
		}
	}

	plan.ID = plan.Domain
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Delete unlocks the domain only when the state says it was locked (§4
// invariant 12): removing a resource that declared locked = false leaves the
// domain as it is. On an API error the diagnostic is returned and state is
// kept, so the destroy can be retried instead of leaking a half-torn-down
// resource.
func (r *domainLockResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state domainLockResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if !state.Locked.ValueBool() {
		return
	}
	if err := r.client.DomainUnlock(ctx, state.Domain.ValueString()); err != nil {
		addAPIError(&resp.Diagnostics, "namesilo_domain_lock", err)
		return
	}
}

// ImportState takes the domain as the import ID, seeds id from it, and lets
// Read fill locked from the API (§10).
func (r *domainLockResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("domain"), req.ID)...)
}
