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
	_ datasource.DataSource              = (*dnssecRecordsDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*dnssecRecordsDataSource)(nil)
)

// dnssecRecordsDataSource reads one domain's DS records as they exist at
// NameSilo. It is the read half of namesilo_dnssec_records: the same
// dnsSecListRecords call and the same normalization, with no write (§7).
type dnssecRecordsDataSource struct {
	client *namesilo.Client
}

// NewDNSSecRecordsDataSource returns the namesilo_dnssec_records data source.
func NewDNSSecRecordsDataSource() datasource.DataSource {
	return &dnssecRecordsDataSource{}
}

// dnssecRecordsDataSourceModel is namesilo_dnssec_records' state model.
type dnssecRecordsDataSourceModel struct {
	Domain  types.String `tfsdk:"domain"`
	Records types.Set    `tfsdk:"records"`
	ID      types.String `tfsdk:"id"`
}

func (d *dnssecRecordsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_dnssec_records"
}

func (d *dnssecRecordsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Reads one domain's DS records as NameSilo currently publishes them. Use " +
			"it to audit a DNSSEC delegation managed outside Terraform, or to compare against the " +
			"`namesilo_dnssec_records` resource's desired set in a plan.\n\n" +
			"The records are normalized the same way the resource stores them: digests lowercased, " +
			"so a set comparison against configured records does not diff on case.",
		Attributes: map[string]schema.Attribute{
			"domain": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The domain whose DS records to read, e.g. `example.com`.",
			},
			"records": schema.SetNestedAttribute{
				Computed: true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"key_tag": schema.Int64Attribute{
							Computed:            true,
							MarkdownDescription: "The key tag: the RFC 4034 identifier of the zone's DNSKEY that this DS record points at.",
						},
						"algorithm": schema.Int64Attribute{
							Computed:            true,
							MarkdownDescription: "The DNSSEC signing algorithm number of the zone's DNSKEY, e.g. 8 (RSA/SHA-256) or 13 (ECDSA P-256/SHA-256).",
						},
						"digest_type": schema.Int64Attribute{
							Computed:            true,
							MarkdownDescription: "The digest algorithm that produced `digest`, e.g. 2 (SHA-256).",
						},
						"digest": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "The hexadecimal digest of the zone's DNSKEY, lowercased.",
						},
					},
				},
				MarkdownDescription: "The domain's DS records. An unsigned domain yields an empty set.",
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
func (d *dnssecRecordsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

// Read refreshes the DS records from dnsSecListRecords and writes them with
// normalized digests. A domain not in the account fails with the API error
// rather than returning an empty set (§7).
func (d *dnssecRecordsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var state dnssecRecordsDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	records, err := d.client.ListDSRecords(ctx, state.Domain.ValueString())
	if err != nil {
		addAPIError(&resp.Diagnostics, "namesilo_dnssec_records", err)
		return
	}

	normalized := make([]namesilo.DSRecord, 0, len(records))
	for _, record := range records {
		normalized = append(normalized, namesilo.NormalizeDSRecord(record))
	}
	set, diags := dsSetFromRecords(ctx, normalized)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	state.Records = set
	state.ID = state.Domain
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
