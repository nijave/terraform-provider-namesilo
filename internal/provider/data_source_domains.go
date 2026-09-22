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
	_ datasource.DataSource              = (*domainsDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*domainsDataSource)(nil)
)

// domainsDataSource reads the account's domain inventory. It pages listDomains
// with the provider's page_size until it has every domain the API reports
// (§4 invariant 13). The client stops after a page that adds no new domain
// names, so an API that ignores the paging parameters terminates after one
// repeat instead of looping; the data source then warns that the list may be
// incomplete while still returning what it fetched, and total still reports
// the API's count (§7).
type domainsDataSource struct {
	client *namesilo.Client
}

// NewDomainsDataSource returns the namesilo_domains data source.
func NewDomainsDataSource() datasource.DataSource {
	return &domainsDataSource{}
}

// domainSummaryModel is one nested domains element: the inventory row's name
// and dates, as the API's listDomains attributes spell them.
type domainSummaryModel struct {
	Name    types.String `tfsdk:"name"`
	Created types.String `tfsdk:"created"`
	Expires types.String `tfsdk:"expires"`
}

// domainsDataSourceModel is namesilo_domains' state model.
type domainsDataSourceModel struct {
	Domains types.Set    `tfsdk:"domains"`
	Total   types.Int64  `tfsdk:"total"`
	ID      types.String `tfsdk:"id"`
}

func (d *domainsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_domains"
}

func (d *domainsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Reads the account's domain inventory. The data source pages the API with " +
			"the provider's `page_size` until it has every domain the API reports, and `total` carries " +
			"the API's own count, so a configuration can assert against completeness.\n\n" +
			"If the API ignores the paging parameters, the read stops after one repeated page instead " +
			"of looping and emits a warning that the list may be incomplete; the `domains` set then " +
			"holds what was fetched and `total` still reports the API's count.",
		Attributes: map[string]schema.Attribute{
			"domains": schema.SetNestedAttribute{
				Computed: true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"name": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "The domain name, as the API lists it.",
						},
						"created": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "The creation date, in the API's YYYY-MM-DD form.",
						},
						"expires": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "The expiry date, in the API's YYYY-MM-DD form.",
						},
					},
				},
				MarkdownDescription: "Every domain the fetch gathered. An empty account yields an empty set.",
			},
			"total": schema.Int64Attribute{
				Computed:            true,
				MarkdownDescription: "The number of domains the API reports for the account, from the reply's pager element.",
			},
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Always `domains`. It is inert and makes `tofu state show` readable.",
			},
		},
	}
}

// domainSummaryObjectType is the framework type of one nested domains element,
// spelled to match the nested schema attributes.
func domainSummaryObjectType() types.ObjectType {
	return types.ObjectType{AttrTypes: map[string]attr.Type{
		"name":    types.StringType,
		"created": types.StringType,
		"expires": types.StringType,
	}}
}

// Configure receives the provider's *namesilo.Client. A wrong type is a
// provider bug, and the diagnostic names the type actually received.
func (d *domainsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

// Read pages the inventory through ListDomains, which uses the provider's
// page_size, and writes the combined set with the API's total. The Truncated
// branch is the duplicate-page guard's signal that the API ignored the paging
// parameters: the warning says the list may be short, but the fetch is not
// discarded and total is not rewritten (§7).
func (d *domainsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var state domainsDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	list, err := d.client.ListDomains(ctx, d.client.PageSize)
	if err != nil {
		addAPIError(&resp.Diagnostics, "namesilo_domains", err)
		return
	}

	models := make([]domainSummaryModel, 0, len(list.Domains))
	for _, domain := range list.Domains {
		models = append(models, domainSummaryModel{
			Name:    types.StringValue(domain.Name),
			Created: types.StringValue(domain.Created),
			Expires: types.StringValue(domain.Expires),
		})
	}
	set, diags := types.SetValueFrom(ctx, domainSummaryObjectType(), models)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	if list.Truncated {
		resp.Diagnostics.AddWarning(
			"NameSilo domain list may be incomplete",
			fmt.Sprintf("NameSilo's listDomains ignored the paging parameters, so the data source "+
				"stopped after a page that added no new domains: it fetched %d of the %d domains the "+
				"API reports. The domains set holds what was fetched, and total still carries the "+
				"API's count; re-planning will not add more domains.",
				len(list.Domains), list.Total),
		)
	}

	state.Domains = set
	state.Total = types.Int64Value(list.Total)
	state.ID = types.StringValue("domains")
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
