// SPDX-License-Identifier: GPL-3.0-or-later

package provider

import (
	"context"
	"regexp"

	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
)

// iso3166Alpha2Pattern matches an uppercase ISO 3166-1 alpha-2 country code:
// exactly two uppercase ASCII letters. Lowercase is rejected at validate time
// rather than normalized, because OpenTofu core forbids the provider from
// changing a known configured value at plan time on create (see ModifyPlan).
var iso3166Alpha2Pattern = regexp.MustCompile(`^[A-Z]{2}$`)

// nonEmptyString rejects an explicitly configured empty string. An Optional
// attribute expresses "unset" as null, which a configuration spells by omitting
// the attribute; setting it to "" is a different value that would reach the API
// as a value, or be dropped and leave a perpetual diff. Null and unknown values
// pass, so omission is accepted. Shared by namesilo_contact's optional strings
// and namesilo_domain_contacts' roles.
func nonEmptyString() validator.String {
	return nonEmptyStringValidator{}
}

type nonEmptyStringValidator struct{}

func (nonEmptyStringValidator) Description(context.Context) string {
	return "value must not be an empty string; omit the attribute instead"
}

func (v nonEmptyStringValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (nonEmptyStringValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	if req.ConfigValue.ValueString() == "" {
		resp.Diagnostics.AddAttributeError(
			req.Path,
			"Invalid Attribute Value",
			"An empty string is not a valid value. Omit the attribute instead of setting an empty string.",
		)
	}
}
