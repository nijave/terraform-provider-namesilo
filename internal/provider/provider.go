// SPDX-License-Identifier: GPL-3.0-or-later

package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
)

// Ensure namesiloProvider satisfies the interface the framework dispatches
// on. If a method signature drifts, this fails at compile time rather than at
// plan time.
var _ provider.Provider = (*namesiloProvider)(nil)

// namesiloProvider manages registrar-level state at NameSilo: the delegation,
// the DS records, WHOIS privacy, the registrar lock, auto-renew, contact
// profiles, and the domain contact associations.
type namesiloProvider struct {
	// version is injected by main.go from goreleaser's ldflags.
	version string
}

// New returns a provider factory for the given version string.
func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &namesiloProvider{version: version}
	}
}

func (p *namesiloProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "namesilo"
	resp.Version = p.version
}

// Schema declares the provider block. The api_key, endpoint, and page_size
// attributes are all optional; client.go resolves their fallbacks and builds
// the client at Configure time. There is no schema-level default for
// page_size because the framework's provider schema does not support one;
// Configure resolves the missing value to the same 100 the design specifies.
func (p *namesiloProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages registrar-level state at NameSilo — nameserver delegation, DS records " +
			"for DNSSEC, WHOIS privacy, the registrar lock, auto-renew, contact profiles, and the domain " +
			"contact associations.",
		Attributes: map[string]schema.Attribute{
			"api_key": schema.StringAttribute{
				Optional:  true,
				Sensitive: true,
				MarkdownDescription: "NameSilo API key. Falls back to the `NAMESILO_API_KEY` environment " +
					"variable; configuration fails when the resolved value is empty.",
			},
			"endpoint": schema.StringAttribute{
				Optional: true,
				MarkdownDescription: "NameSilo API endpoint. Falls back to the `NAMESILO_API_ENDPOINT` " +
					"environment variable, then to the production endpoint. A trailing slash is trimmed.",
			},
			"page_size": schema.Int64Attribute{
				Optional: true,
				MarkdownDescription: "Page size for the `namesilo_domains` data source's inventory requests. " +
					"Defaults to 100; allowed values are the ones NameSilo's own UI offers: 20, 50, 100, 200, " +
					"and 500.",
				Validators: []validator.Int64{
					int64validator.OneOf(20, 50, 100, 200, 500),
				},
			},
		},
	}
}

// Resources returns the provider's resources. Every resource task appends its
// constructor here.
func (p *namesiloProvider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		NewNameserversResource,
		NewDNSSecRecordsResource,
		NewPrivacyResource,
		NewDomainLockResource,
		NewAutoRenewResource,
		NewContactResource,
		NewDomainContactsResource,
	}
}

// DataSources returns the provider's data sources. Every data source task
// appends its constructor here.
func (p *namesiloProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		NewNameserversDataSource,
		NewDNSSecRecordsDataSource,
		NewPrivacyDataSource,
		NewContactsDataSource,
		NewDomainDataSource,
		NewDomainsDataSource,
	}
}
