#!/usr/bin/env bash
# tfplugindocs (github.com/hashicorp/terraform-plugin-docs) resolves the
# provider schema by shelling out to a binary literally named "terraform" and
# assumes providers publish under registry.terraform.io. Neither holds for
# OpenTofu: it isn't named "terraform", so tfplugindocs falls back to
# downloading the latest real Terraform release, silently pulling a
# BUSL-licensed binary into doc generation, which is exactly what this
# project's `license` job exists to forbid, and a source of doc drift every time
# HashiCorp cuts a release. OpenTofu also defaults unqualified provider
# addresses to registry.opentofu.org, so it can't simply be renamed/wrapped as
# "terraform" either.
#
# Export the schema ourselves with `tofu`, using a dev_overrides CLI config
# (the standard local-provider-development mechanism) so no registry lookup or
# provider install is involved, then rewrite the registry.opentofu.org key to
# the bare provider name tfplugindocs's --providers-schema loader expects.
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")"

command -v tofu >/dev/null || { echo "tofu not found in PATH; OpenTofu >= 1.11 is required" >&2; exit 1; }

workdir=$(mktemp -d)
trap 'rm -rf "$workdir"' EXIT

(cd .. && go build -o "$workdir/terraform-provider-namesilo" .)

cat > "$workdir/dev.tfrc" <<EOF
provider_installation {
  dev_overrides {
    "hashicorp/namesilo" = "$workdir"
  }
  direct {}
}
EOF

mkdir "$workdir/work"
cat > "$workdir/work/main.tf" <<'EOF'
terraform {
  required_providers {
    namesilo = {
      source = "hashicorp/namesilo"
    }
  }
}
provider "namesilo" {}
EOF

TF_CLI_CONFIG_FILE="$workdir/dev.tfrc" tofu -chdir="$workdir/work" providers schema -json \
  | sed 's#"registry\.opentofu\.org/hashicorp/namesilo"#"namesilo"#' \
  > schema.json

# Guard the rewrite above. If OpenTofu ever emits the address under a different
# host or namespace, the sed silently no-ops and tfplugindocs later fails with a
# schema-lookup error that says nothing about the real cause. Fail here instead.
if ! grep -q '"namesilo"' schema.json; then
  echo "schema.json does not contain the bare provider name 'namesilo'; the registry rewrite in tools/gen-schema.sh did not match" >&2
  exit 1
fi
if grep -q 'registry\.opentofu\.org/hashicorp/namesilo' schema.json; then
  echo "schema.json still contains the registry-qualified provider address; the registry rewrite in tools/gen-schema.sh did not match" >&2
  exit 1
fi
