// SPDX-License-Identifier: GPL-3.0-or-later

package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/nijave/terraform-provider-namesilo/internal/namesilo"
)

var (
	_ datasource.DataSource              = (*nameserversDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*nameserversDataSource)(nil)
)

// nameserversDataSource reads one domain's current delegation. It is the read
// half of namesilo_nameservers: the same getDomainInfo call and the same
// normalization, with no write and no desired state (§7).
type nameserversDataSource struct {
	client *namesilo.Client
}

// NewNameserversDataSource returns the namesilo_nameservers data source.
func NewNameserversDataSource() datasource.DataSource {
	return &nameserversDataSource{}
}

// nameserversDataSourceModel is namesilo_nameservers' state model. Nameservers
// is a list, not a set: its element order is the delegation's actual order the
// API reports position by position, which a set would discard (§7).
type nameserversDataSourceModel struct {
	Domain      types.String `tfsdk:"domain"`
	Nameservers types.List   `tfsdk:"nameservers"`
	ID          types.String `tfsdk:"id"`
}

func (d *nameserversDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_nameservers"
}

func (d *nameserversDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Reads one domain's current nameserver delegation. Use it to audit a " +
			"delegation managed outside Terraform, to compare against the `namesilo_nameservers` " +
			"resource's desired state in a plan, or to feed the delegation into other configuration.\n\n" +
			"`nameservers` is a list, not a set: its order is the delegation's position order, which " +
			"is the order queries are answered in and some DNS setups depend on. The values are " +
			"lowercased and stripped of one trailing dot, the same normalization the resource applies.",
		Attributes: map[string]schema.Attribute{
			"domain": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The domain whose delegation to read, e.g. `example.com`.",
			},
			"nameservers": schema.ListAttribute{
				Computed:    true,
				ElementType: types.StringType,
				MarkdownDescription: "The domain's nameservers in the API's position order. A list on " +
					"purpose: the order is the delegation's real order, not an unordered collection. " +
					"Values are lowercased with one trailing dot stripped.",
			},
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The domain name. It is inert and makes `tofu state show` readable.",
			},
		},
	}
}

// Configure receives the provider's *namesilo.Client. A wrong type is a
// provider bug, and the diagnostic names the type actually received.
func (d *nameserversDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

// Read refreshes the delegation from getDomainInfo and writes it lowercased,
// in the API's position order. A domain not in the account fails with the API
// error rather than returning an empty list, which would be indistinguishable
// from a name typo (§7).
func (d *nameserversDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var state nameserversDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	info, err := d.client.GetDomainInfo(ctx, state.Domain.ValueString())
	if err != nil {
		addAPIError(&resp.Diagnostics, "namesilo_nameservers", err)
		return
	}

	normalized := namesilo.NormalizeNameservers(info.Nameservers)
	nameservers, diags := types.ListValueFrom(ctx, types.StringType, normalized)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	state.Nameservers = nameservers
	state.ID = state.Domain
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
