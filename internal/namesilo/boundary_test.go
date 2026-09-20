// SPDX-License-Identifier: GPL-3.0-or-later

package namesilo

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPackageImportsNoTerraform enforces the boundary the design section 8
// draws: internal/namesilo is pure Go and imports zero Terraform packages, so
// every request and parsing decision is testable without a plugin harness and
// the framework layer stays a mechanical translation.
//
// This is a test rather than a convention because the pressure to violate it
// is real and arrives gradually: one diag.Diagnostics here, one types.List
// there, and each individual step looks harmless.
func TestPackageImportsNoTerraform(t *testing.T) {
	t.Parallel()

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading the package directory: %v", err)
	}
	fset := token.NewFileSet()
	forbidden := []string{
		// The dash matters: it distinguishes the modern, split-out
		// terraform-plugin-* module family from the legacy pre-split in-tree
		// SDK path github.com/hashicorp/terraform/helper/schema. Both prefixes
		// are required; do not collapse this to one entry.
		"github.com/hashicorp/terraform-",
		"github.com/hashicorp/terraform/",
		"github.com/opentofu/",
	}
	checked := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(".", e.Name()), nil, parser.ImportsOnly)
		if err != nil {
			t.Errorf("parsing %s: %v", e.Name(), err)
			continue
		}
		checked++
		for _, imp := range file.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			for _, bad := range forbidden {
				if strings.HasPrefix(path, bad) {
					t.Errorf("%s imports %q; internal/namesilo must not depend on Terraform packages", e.Name(), path)
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("no Go files were checked; the test is not doing what it claims")
	}
}
