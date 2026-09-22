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
	_ datasource.DataSource              = (*privacyDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*privacyDataSource)(nil)
)

// privacyDataSource reads whether WHOIS privacy is enabled on one domain. It
// is the read half of namesilo_privacy: the same getDomainInfo call, with no
// write and no desired state (§7).
type privacyDataSource struct {
	client *namesilo.Client
}

// NewPrivacyDataSource returns the namesilo_privacy data source.
func NewPrivacyDataSource() datasource.DataSource {
	return &privacyDataSource{}
}

// privacyDataSourceModel is namesilo_privacy's state model.
type privacyDataSourceModel struct {
	Domain  types.String `tfsdk:"domain"`
	Enabled types.Bool   `tfsdk:"enabled"`
	ID      types.String `tfsdk:"id"`
}

func (d *privacyDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_privacy"
}

func (d *privacyDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Reads whether WHOIS privacy — NameSilo's PrivacyGuardian — is enabled on " +
			"one domain. Use it to audit privacy state managed outside Terraform, or to gate other " +
			"configuration on the domain's current exposure. To manage the state instead, use the " +
			"`namesilo_privacy` resource.",
		Attributes: map[string]schema.Attribute{
			"domain": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The domain whose privacy state to read, e.g. `example.com`.",
			},
			"enabled": schema.BoolAttribute{
				Computed:            true,
				MarkdownDescription: "Whether the domain's WHOIS output currently hides registrant contact data behind PrivacyGuardian.",
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
func (d *privacyDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

// Read refreshes enabled from getDomainInfo's private flag. A domain not in
// the account fails with the API error rather than reporting enabled = false,
// which would be indistinguishable from a name typo (§7).
func (d *privacyDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var state privacyDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	info, err := d.client.GetDomainInfo(ctx, state.Domain.ValueString())
	if err != nil {
		addAPIError(&resp.Diagnostics, "namesilo_privacy", err)
		return
	}

	state.Enabled = types.BoolValue(info.Private)
	state.ID = state.Domain
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
