// SPDX-License-Identifier: GPL-3.0-or-later

package provider

import (
	"context"
	"errors"

	"github.com/hashicorp/terraform-plugin-framework/attr"
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

// dsRecordObjectType is the element type of the dnssec records set: the four
// DS fields with their framework types, named as the schema spells them.
func dsRecordObjectType() types.ObjectType {
	return types.ObjectType{AttrTypes: map[string]attr.Type{
		"key_tag":     types.Int64Type,
		"algorithm":   types.Int64Type,
		"digest_type": types.Int64Type,
		"digest":      types.StringType,
	}}
}

// dsSetFromRecords converts a slice of DS records to a set(object). A nil
// slice becomes an empty set, never null: records = [] is a valid declaration
// that the domain is unsigned, and it is also what Read writes for an empty
// API reply (§4 invariant 2).
func dsSetFromRecords(ctx context.Context, records []namesilo.DSRecord) (types.Set, diag.Diagnostics) {
	if records == nil {
		records = []namesilo.DSRecord{}
	}
	models := make([]dsRecordModel, 0, len(records))
	for _, record := range records {
		models = append(models, dsRecordModel{
			KeyTag:     types.Int64Value(record.KeyTag),
			Algorithm:  types.Int64Value(record.Algorithm),
			DigestType: types.Int64Value(record.DigestType),
			Digest:     types.StringValue(record.Digest),
		})
	}
	return types.SetValueFrom(ctx, dsRecordObjectType(), models)
}

// recordsFromDSSet converts a set(object) to a slice of DS records. Null and
// unknown are both treated as absent and produce a nil slice with no
// diagnostic: a null required attribute is a schema violation the framework
// already reports, and an unknown value is resolved before Create or Update
// runs. Element order is not meaningful for a set.
func recordsFromDSSet(ctx context.Context, set types.Set) ([]namesilo.DSRecord, diag.Diagnostics) {
	var diags diag.Diagnostics
	if set.IsNull() || set.IsUnknown() {
		return nil, diags
	}
	var models []dsRecordModel
	diags.Append(set.ElementsAs(ctx, &models, false)...)
	if diags.HasError() {
		return nil, diags
	}
	records := make([]namesilo.DSRecord, 0, len(models))
	for _, model := range models {
		records = append(records, namesilo.DSRecord{
			KeyTag:     model.KeyTag.ValueInt64(),
			Algorithm:  model.Algorithm.ValueInt64(),
			DigestType: model.DigestType.ValueInt64(),
			Digest:     model.Digest.ValueString(),
		})
	}
	return records, diags
}
