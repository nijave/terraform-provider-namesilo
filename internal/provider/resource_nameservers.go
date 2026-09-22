// SPDX-License-Identifier: GPL-3.0-or-later

package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
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
	_ resource.Resource                = (*nameserversResource)(nil)
	_ resource.ResourceWithConfigure   = (*nameserversResource)(nil)
	_ resource.ResourceWithImportState = (*nameserversResource)(nil)
	_ resource.ResourceWithModifyPlan  = (*nameserversResource)(nil)
)

// defaultNameservers is what Delete points a domain at. It is not the absence
// of delegation: the API has no "remove all nameservers" call, so the closest
// thing to undelegating is NameSilo's own default set (§4 invariant 4).
var defaultNameservers = []string{"ns1.dnsowl.com", "ns2.dnsowl.com", "ns3.dnsowl.com"}

// nameserversResource manages one domain's registrar delegation.
type nameserversResource struct {
	client *namesilo.Client
}

// NewNameserversResource returns the namesilo_nameservers resource.
func NewNameserversResource() resource.Resource {
	return &nameserversResource{}
}

// nameserversResourceModel is namesilo_nameservers's state model.
type nameserversResourceModel struct {
	Domain      types.String `tfsdk:"domain"`
	Nameservers types.Set    `tfsdk:"nameservers"`
	ID          types.String `tfsdk:"id"`
}

func (r *nameserversResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_nameservers"
}

func (r *nameserversResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a domain's registrar nameserver delegation — the NS records the " +
			"registry publishes. NameSilo replaces the whole set in one call, so this resource is " +
			"authoritative: the domain's delegation is exactly the `nameservers` set.\n\n" +
			"Two behaviours surprise operators. First, values are stored lowercased: a configuration " +
			"written in another case is normalized at plan time, so it does not diff. Second, destroying " +
			"this resource does not remove delegation — the API offers no way to do that. Destroy " +
			"repoints the domain at NameSilo's default nameservers (`ns1.dnsowl.com`, `ns2.dnsowl.com`, " +
			"`ns3.dnsowl.com`), which is an outage if the zone is hosted elsewhere. Remove this resource " +
			"only when the domain is being retired or is about to use a different provider.",
		Attributes: map[string]schema.Attribute{
			"domain": schema.StringAttribute{
				Required: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
				MarkdownDescription: "The domain whose delegation to manage, e.g. `example.com`. Changing " +
					"it forces a replacement: the resource is pointed at a different domain rather than " +
					"renamed. Also the import ID.",
			},
			"nameservers": schema.SetAttribute{
				Required:    true,
				ElementType: types.StringType,
				Validators: []validator.Set{
					setvalidator.SizeAtLeast(2),
					setvalidator.SizeAtMost(13),
				},
				MarkdownDescription: "The complete delegation set. A set, not a list: order has no meaning " +
					"for delegation, the API assigns positions itself, and NameSilo returns the set in an " +
					"arbitrary order. Values are stored lowercased and with a trailing dot stripped, and " +
					"are normalized at plan time so a differently-cased configuration does not diff. The " +
					"API requires at least two and accepts at most thirteen nameservers. Destroying the " +
					"resource repoints the domain at NameSilo's default nameservers rather than removing " +
					"delegation.",
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
func (r *nameserversResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

// Create and Update are the same operation: the API has no "add one
// nameserver" call, so both replace the whole set with the normalized planned
// value.
func (r *nameserversResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan nameserversResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	nameservers, diags := stringsFromSet(ctx, plan.Nameservers)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	normalized := namesilo.NormalizeNameservers(nameservers)

	if err := r.client.ChangeNameServers(ctx, plan.Domain.ValueString(), normalized); err != nil {
		addAPIError(&resp.Diagnostics, "namesilo_nameservers", err)
		return
	}

	normalizedSet, diags := setFromStrings(ctx, normalized)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	plan.Nameservers = normalizedSet
	plan.ID = plan.Domain
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Read refreshes the delegation from getDomainInfo. An empty list from the API
// cannot be represented (`SizeAtLeast(2)` rejects it), so it is an error
// diagnostic rather than a silent empty set. Any API error is a diagnostic and
// leaves state untouched: NameSilo's code 200 conflates "expired", "inactive",
// and "not yours", so treating it as "gone" would silently drop a domain that
// is merely expired (§11).
func (r *nameserversResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state nameserversResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	info, err := r.client.GetDomainInfo(ctx, state.Domain.ValueString())
	if err != nil {
		addAPIError(&resp.Diagnostics, "namesilo_nameservers", err)
		return
	}

	normalized := namesilo.NormalizeNameservers(info.Nameservers)
	if len(normalized) == 0 {
		resp.Diagnostics.AddError(
			"NameSilo API error (namesilo_nameservers)",
			fmt.Sprintf("getDomainInfo returned no nameservers for %s. Delegation is a required set of "+
				"at least two nameservers, so an empty reply cannot be stored. The domain may have no "+
				"delegation, or the reply was incomplete.", state.Domain.ValueString()),
		)
		return
	}

	normalizedSet, diags := setFromStrings(ctx, normalized)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	state.Nameservers = normalizedSet
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Update replaces the whole delegation, exactly as Create does. A change to
// domain plans a replacement, so Update never sees a domain change.
func (r *nameserversResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan nameserversResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	nameservers, diags := stringsFromSet(ctx, plan.Nameservers)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	normalized := namesilo.NormalizeNameservers(nameservers)

	if err := r.client.ChangeNameServers(ctx, plan.Domain.ValueString(), normalized); err != nil {
		addAPIError(&resp.Diagnostics, "namesilo_nameservers", err)
		return
	}

	normalizedSet, diags := setFromStrings(ctx, normalized)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	plan.Nameservers = normalizedSet
	plan.ID = plan.Domain
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Delete points the domain at NameSilo's default nameservers (§4 invariant 4).
// On an API error the diagnostic is returned and state is kept, so the destroy
// can be retried instead of leaking a half-torn-down resource.
func (r *nameserversResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state nameserversResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.client.ChangeNameServers(ctx, state.Domain.ValueString(), defaultNameservers); err != nil {
		addAPIError(&resp.Diagnostics, "namesilo_nameservers", err)
		return
	}
}

// ImportState takes the domain as the import ID, seeds id from it, and lets
// Read fill nameservers from the API (§10).
func (r *nameserversResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("domain"), req.ID)...)
}

// ModifyPlan normalizes the planned set so it matches the normalized state Read
// writes; without it a lowercase-only API produces a perpetual diff (§6.8).
// Destroy plans carry no planned state and unknown values are left alone: the
// apply resolves them.
func (r *nameserversResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() {
		return
	}

	var plan nameserversResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !setWhollyKnown(plan.Nameservers) {
		return
	}

	nameservers, diags := stringsFromSet(ctx, plan.Nameservers)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	normalizedSet, diags := setFromStrings(ctx, namesilo.NormalizeNameservers(nameservers))
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("nameservers"), normalizedSet)...)
}

// setWhollyKnown reports whether a set and every one of its elements is known.
// types.Set has no IsWhollyKnown in this framework version, and IsUnknown
// alone is false for a set that has a known length but holds unknown elements,
// which is exactly the case ModifyPlan must not try to normalize.
func setWhollyKnown(set types.Set) bool {
	if set.IsNull() {
		return true
	}
	if set.IsUnknown() {
		return false
	}
	for _, element := range set.Elements() {
		if element.IsUnknown() {
			return false
		}
	}
	return true
}
