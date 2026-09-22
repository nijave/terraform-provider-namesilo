// SPDX-License-Identifier: GPL-3.0-or-later

package provider

import (
	"context"
	"errors"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/nijave/terraform-provider-namesilo/internal/namesilo"
)

// addAPIError turns an error from the client into a diagnostic. The summary
// names the resource type so a user can tell which resource's operation
// failed; the detail is the API's own detail text plus its code for an
// *namesilo.APIError, and the error text otherwise. Neither the request URL
// nor the query appears in an *namesilo.APIError, and the error text from the
// client is itself built without them, so the API key cannot surface here
// (§8.4, §11).
func addAPIError(diags *diag.Diagnostics, resourceType string, err error) {
	summary := "NameSilo API error (" + resourceType + ")"
	var apiErr *namesilo.APIError
	if errors.As(err, &apiErr) {
		diags.AddError(summary, apiErr.Detail+" (code "+apiErr.Code+")")
		return
	}
	diags.AddError(summary, err.Error())
}

// setFromStrings converts a Go slice to a set(string). A nil slice becomes an
// empty set, never null: the framework reports a duplicate element as a
// diagnostic rather than collapsing it, so callers must pass unique values.
func setFromStrings(ctx context.Context, values []string) (types.Set, diag.Diagnostics) {
	if values == nil {
		values = []string{}
	}
	return types.SetValueFrom(ctx, types.StringType, values)
}

// stringsFromSet converts a set(string) to a Go slice. Null and unknown are
// both treated as absent and produce a nil slice with no diagnostic: a null
// required attribute is a schema violation the framework already reports, and
// an unknown value is resolved before Create or Update runs. Element order is
// not meaningful for a set.
func stringsFromSet(ctx context.Context, set types.Set) ([]string, diag.Diagnostics) {
	var diags diag.Diagnostics
	if set.IsNull() || set.IsUnknown() {
		return nil, diags
	}
	var out []string
	diags.Append(set.ElementsAs(ctx, &out, false)...)
	return out, diags
}

// nullIfEmpty maps the API's empty element convention to a null value: the
// API returns `<field/>` for "not set", and a null attribute is how the schema
// spells that so a configuration that omits the field does not diff. Used by
// the optional attributes of later resources.
func nullIfEmpty(value string) types.String {
	if value == "" {
		return types.StringNull()
	}
	return types.StringValue(value)
}
