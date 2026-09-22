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
	_ datasource.DataSource              = (*domainDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*domainDataSource)(nil)
)

// domainDataSource reads one domain's whole registrar state. It exposes the
// getDomainInfo reply the way the API sent it: Yes/No replies become booleans,
// dates stay strings in the API's YYYY-MM-DD form, and forward_url and
// forward_type pass through unchanged, N/A included, because mapping them to
// null would be a guess about a value the API is allowed to return (§7).
type domainDataSource struct {
	client *namesilo.Client
}

// NewDomainDataSource returns the namesilo_domain data source.
func NewDomainDataSource() datasource.DataSource {
	return &domainDataSource{}
}

// contactRolesModel is the nested contact_ids object: the four association
// roles. An unset role comes back null, matching how the
// namesilo_domain_contacts resource stores an unmanaged role. The model field
// is a pointer because contact_ids is Computed only: the configuration's null
// must decode into a nil pointer.
type contactRolesModel struct {
	Registrant     types.String `tfsdk:"registrant"`
	Administrative types.String `tfsdk:"administrative"`
	Technical      types.String `tfsdk:"technical"`
	Billing        types.String `tfsdk:"billing"`
}

// domainDataSourceModel is namesilo_domain's state model.
type domainDataSourceModel struct {
	Domain                    types.String       `tfsdk:"domain"`
	Created                   types.String       `tfsdk:"created"`
	Expires                   types.String       `tfsdk:"expires"`
	Status                    types.String       `tfsdk:"status"`
	Locked                    types.Bool         `tfsdk:"locked"`
	Private                   types.Bool         `tfsdk:"private"`
	AutoRenew                 types.Bool         `tfsdk:"auto_renew"`
	TrafficType               types.String       `tfsdk:"traffic_type"`
	EmailVerificationRequired types.Bool         `tfsdk:"email_verification_required"`
	Portfolio                 types.String       `tfsdk:"portfolio"`
	ForwardURL                types.String       `tfsdk:"forward_url"`
	ForwardType               types.String       `tfsdk:"forward_type"`
	Nameservers               types.List         `tfsdk:"nameservers"`
	ContactIDs                *contactRolesModel `tfsdk:"contact_ids"`
	ID                        types.String       `tfsdk:"id"`
}

func (d *domainDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_domain"
}

func (d *domainDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Reads one domain's registrar state: dates, status, registrar lock, WHOIS " +
			"privacy, auto-renew, traffic type, email verification, portfolio, forwarding, the " +
			"delegation, and the contact IDs in the four association roles. It is the read half of " +
			"the domain's managed attributes, for audits, comparisons, and drift detection in a plan.\n\n" +
			"`forward_url` and `forward_type` are exposed exactly as the API sends them: `N/A` when " +
			"forwarding is off. They are not mapped to null, because that would be a guess about a " +
			"value the API is allowed to return. The association roles the API reports as unset come " +
			"back null, matching how the `namesilo_domain_contacts` resource stores an unmanaged role.",
		Attributes: map[string]schema.Attribute{
			"domain": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The domain whose registrar state to read, e.g. `example.com`.",
			},
			"created": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The creation date, in the API's YYYY-MM-DD form.",
			},
			"expires": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The expiry date, in the API's YYYY-MM-DD form.",
			},
			"status": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The domain's status as the API reports it, e.g. `active`.",
			},
			"locked": schema.BoolAttribute{
				Computed:            true,
				MarkdownDescription: "Whether the registrar lock is on, protecting the domain from transfers.",
			},
			"private": schema.BoolAttribute{
				Computed:            true,
				MarkdownDescription: "Whether WHOIS privacy (PrivacyGuardian) is on.",
			},
			"auto_renew": schema.BoolAttribute{
				Computed:            true,
				MarkdownDescription: "Whether auto-renewal is on.",
			},
			"traffic_type": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The traffic type as the API reports it; an empty string when unset.",
			},
			"email_verification_required": schema.BoolAttribute{
				Computed:            true,
				MarkdownDescription: "Whether the registrant's email verification is pending.",
			},
			"portfolio": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The portfolio the domain belongs to as the API reports it; an empty string when unset.",
			},
			"forward_url": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The forwarding target exactly as the API sends it, `N/A` included when forwarding is off.",
			},
			"forward_type": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The forwarding type exactly as the API sends it, `N/A` included when forwarding is off.",
			},
			"nameservers": schema.ListAttribute{
				Computed:    true,
				ElementType: types.StringType,
				MarkdownDescription: "The domain's nameservers in the API's position order, lowercased " +
					"and stripped of one trailing dot, the same normalization the resources apply.",
			},
			"contact_ids": schema.SingleNestedAttribute{
				Computed: true,
				Attributes: map[string]schema.Attribute{
					"registrant": schema.StringAttribute{
						Computed:            true,
						MarkdownDescription: "The `contact_id` of the registrant profile, null when unset.",
					},
					"administrative": schema.StringAttribute{
						Computed:            true,
						MarkdownDescription: "The `contact_id` of the administrative profile, null when unset.",
					},
					"technical": schema.StringAttribute{
						Computed:            true,
						MarkdownDescription: "The `contact_id` of the technical profile, null when unset.",
					},
					"billing": schema.StringAttribute{
						Computed:            true,
						MarkdownDescription: "The `contact_id` of the billing profile, null when unset.",
					},
				},
				MarkdownDescription: "The domain's contact associations by role.",
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
func (d *domainDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

// Read refreshes the whole registrar view from getDomainInfo. The client has
// already parsed the Yes/No elements into booleans and rejected anything else;
// the data source maps the result one to one. A domain not in the account
// fails with the API error rather than returning an empty result (§7).
func (d *domainDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var state domainDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	info, err := d.client.GetDomainInfo(ctx, state.Domain.ValueString())
	if err != nil {
		addAPIError(&resp.Diagnostics, "namesilo_domain", err)
		return
	}

	nameservers, diags := types.ListValueFrom(ctx, types.StringType, namesilo.NormalizeNameservers(info.Nameservers))
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	state.Created = types.StringValue(info.Created)
	state.Expires = types.StringValue(info.Expires)
	state.Status = types.StringValue(info.Status)
	state.Locked = types.BoolValue(info.Locked)
	state.Private = types.BoolValue(info.Private)
	state.AutoRenew = types.BoolValue(info.AutoRenew)
	state.TrafficType = types.StringValue(info.TrafficType)
	state.EmailVerificationRequired = types.BoolValue(info.EmailVerificationRequired)
	state.Portfolio = types.StringValue(info.Portfolio)
	state.ForwardURL = types.StringValue(info.ForwardURL)
	state.ForwardType = types.StringValue(info.ForwardType)
	state.Nameservers = nameservers
	state.ContactIDs = &contactRolesModel{
		Registrant:     nullIfEmpty(info.Contacts.Registrant),
		Administrative: nullIfEmpty(info.Contacts.Administrative),
		Technical:      nullIfEmpty(info.Contacts.Technical),
		Billing:        nullIfEmpty(info.Contacts.Billing),
	}
	state.ID = state.Domain
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
