// SPDX-License-Identifier: GPL-3.0-or-later

package provider

import (
	"context"
	"errors"
	"os"

	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/nijave/terraform-provider-namesilo/internal/namesilo"
)

// providerConfigModel mirrors the provider schema as the framework decodes it
// for Configure.
type providerConfigModel struct {
	APIKey   types.String `tfsdk:"api_key"`
	Endpoint types.String `tfsdk:"endpoint"`
	PageSize types.Int64  `tfsdk:"page_size"`
}

// resolveAPIKey prefers the configured attribute and falls back to the
// NAMESILO_API_KEY environment variable. An empty result is an error; the
// message names both sources and never carries the key itself.
func resolveAPIKey(configured string) (string, error) {
	if configured != "" {
		return configured, nil
	}
	if key := os.Getenv("NAMESILO_API_KEY"); key != "" {
		return key, nil
	}
	return "", errors.New("the api_key attribute is empty and the NAMESILO_API_KEY environment variable is not set; configure one of them")
}

// resolveEndpoint prefers the configured attribute, then the
// NAMESILO_API_ENDPOINT environment variable, then the production endpoint.
func resolveEndpoint(configured string) string {
	if configured != "" {
		return configured
	}
	if endpoint := os.Getenv("NAMESILO_API_ENDPOINT"); endpoint != "" {
		return endpoint
	}
	return namesilo.DefaultEndpoint
}

// Configure resolves the provider configuration into one *namesilo.Client and
// hands it to every resource and data source through ResourceData and
// DataSourceData.
func (p *namesiloProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var config providerConfigModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	apiKey, err := resolveAPIKey(config.APIKey.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Missing NameSilo API key", err.Error())
		return
	}
	client := namesilo.NewClient(resolveEndpoint(config.Endpoint.ValueString()), apiKey, p.version)
	pageSize := int64(100)
	if !config.PageSize.IsNull() {
		pageSize = config.PageSize.ValueInt64()
	}
	client.PageSize = pageSize
	resp.ResourceData = client
	resp.DataSourceData = client
}
