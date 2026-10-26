// SPDX-License-Identifier: MPL-2.0

//go:build generate

package tools

import (
	// Documentation generation.
	_ "github.com/hashicorp/terraform-plugin-docs/cmd/tfplugindocs"
)

//go:generate go run github.com/hashicorp/terraform-plugin-docs/cmd/tfplugindocs generate --provider-dir .. --provider-name namesilo --providers-schema schema.json
