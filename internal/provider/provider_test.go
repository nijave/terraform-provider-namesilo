// SPDX-License-Identifier: GPL-3.0-or-later

package provider_test

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"

	"github.com/nijave/terraform-provider-namesilo/internal/provider"
)

// testAccProtoV6ProviderFactories serves the provider in-process over protocol
// 6. Every harness and acceptance test uses this; there is no external
// provider to install and no registry lookup.
var testAccProtoV6ProviderFactories = map[string]func() (tfprotov6.ProviderServer, error){
	"namesilo": providerserver.NewProtocol6WithError(provider.New("test")()),
}

// expectedResourceTypes lists every resource type the harness tests must
// cover. Each resource task appends its type name here.
var expectedResourceTypes = []string{
	"namesilo_nameservers",
	"namesilo_dnssec_records",
	"namesilo_privacy",
}

// expectedDataSourceTypes lists every data source type the harness tests must
// cover. The data source tasks append their type names here.
var expectedDataSourceTypes []string

var tofuPath string

func TestMain(m *testing.M) {
	if p := os.Getenv("TF_ACC_TERRAFORM_PATH"); p != "" {
		tofuPath = p
	} else if p, err := exec.LookPath("tofu"); err == nil {
		tofuPath = p
		_ = os.Setenv("TF_ACC_TERRAFORM_PATH", p)
	}
	_ = os.Setenv("TF_ACC_PROVIDER_HOST", "registry.opentofu.org")
	os.Exit(m.Run())
}

// requireTofu skips when no OpenTofu binary was found, so `go test ./...`
// still passes on a machine without it. Run `make test` for the full suite.
func requireTofu(t *testing.T) {
	t.Helper()
	if tofuPath == "" {
		t.Skip("tofu not found; the harness tests need OpenTofu. Run `make test` with tofu on PATH.")
	}
}

// testAccPreCheck fails fast with an actionable message when the harness is
// misconfigured, rather than letting terraform-plugin-testing download a
// Terraform binary. There are no credentials to check here: live tests add
// their own gates.
func testAccPreCheck(t *testing.T) {
	t.Helper()
	if os.Getenv("TF_ACC") == "" {
		t.Skip("TF_ACC is not set; skipping the acceptance test")
	}
	path := os.Getenv("TF_ACC_TERRAFORM_PATH")
	if path == "" {
		t.Fatal("TF_ACC_TERRAFORM_PATH is not set. Run `make testacc-live`, which points it at the tofu binary. " +
			"Without it the harness falls back to downloading Terraform, which is not the tested platform.")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("TF_ACC_TERRAFORM_PATH=%q is not usable: %v", path, err)
	}
	// Without this, terraform-plugin-testing pairs its default host
	// registry.terraform.io with the legacy "-" namespace it registers for
	// reattach, and OpenTofu refuses the combination with a message about
	// provider address parsing that says nothing about the real cause. Fail
	// here instead, where the message can name the fix.
	if got := os.Getenv("TF_ACC_PROVIDER_HOST"); got != "registry.opentofu.org" {
		t.Fatalf("TF_ACC_PROVIDER_HOST is %q, want \"registry.opentofu.org\". Run `make testacc-live`, which "+
			"sets it. Without it terraform-plugin-testing pairs its default registry.terraform.io host "+
			"with the legacy \"-\" namespace, and OpenTofu rejects that pairing before the provider is "+
			"ever reached.", got)
	}
}

// TestProviderSchema validates the whole provider schema without a Terraform
// CLI and without TF_ACC. It talks to the protocol 6 server directly rather
// than going through resource.Test, which would default-discover or download a
// Terraform binary (terraform-plugin-testing's plugintest.DiscoverConfig). Every
// resource and data source a later task registers is validated automatically,
// because the framework runs Schema.ValidateImplementation on each one.
func TestProviderSchema(t *testing.T) {
	t.Parallel()

	server, err := providerserver.NewProtocol6WithError(provider.New("test")())()
	if err != nil {
		t.Fatalf("creating the protocol 6 server: %v", err)
	}

	resp, err := server.GetProviderSchema(context.Background(), &tfprotov6.GetProviderSchemaRequest{})
	if err != nil {
		t.Fatalf("GetProviderSchema returned an error: %v", err)
	}
	if resp == nil {
		t.Fatal("GetProviderSchema returned a nil response")
	}
	if len(resp.Diagnostics) != 0 {
		t.Fatalf("provider schema diagnostics: %+v", resp.Diagnostics)
	}
	// Keep the schema genuinely non-empty so this test cannot pass against an
	// unregistered provider that returns nothing at all.
	if resp.Provider == nil {
		t.Fatal("GetProviderSchema returned no provider schema")
	}
	for _, name := range expectedResourceTypes {
		if _, ok := resp.ResourceSchemas[name]; !ok {
			t.Errorf("provider did not register resource %q", name)
		}
	}
	for _, name := range expectedDataSourceTypes {
		if _, ok := resp.DataSourceSchemas[name]; !ok {
			t.Errorf("provider did not register data source %q", name)
		}
	}
	// The protocol 6 schema carries its attributes as a slice under the root
	// block; index them by name for the assertions below.
	attrs := make(map[string]*tfprotov6.SchemaAttribute, len(resp.Provider.Block.Attributes))
	for _, a := range resp.Provider.Block.Attributes {
		attrs[a.Name] = a
	}
	apiKey, ok := attrs["api_key"]
	if !ok {
		t.Error("provider schema has no api_key attribute")
	} else if !apiKey.Sensitive {
		t.Error("provider attribute api_key is not Sensitive")
	}
	if _, ok := attrs["endpoint"]; !ok {
		t.Error("provider schema has no endpoint attribute")
	}
	if _, ok := attrs["page_size"]; !ok {
		t.Error("provider schema has no page_size attribute")
	}
}

// TestUserFacingStringsUseEmDashes pins the house style: Go comments in this
// repository write a parenthetical break as `--`, but user-facing text renders
// it, so it has to be a real em dash there. `--` inside a MarkdownDescription
// reaches the registry documentation as two literal hyphens.
//
// Scanning string literals rather than raw file text separates the two cases
// cleanly: a comment is not a literal. A literal is checked as its concatenated
// value, not one BasicLit at a time, because this package wraps long
// descriptions across `+`-joined literals and a per-literal scan would miss a
// `--` split across the join.
func TestUserFacingStringsUseEmDashes(t *testing.T) {
	t.Parallel()

	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parsing the package source: %v", err)
	}

	// constString reports the value of a compile-time constant string
	// expression: a string literal, or a `+` concatenation of them. It returns
	// false for anything it cannot fully resolve, so the caller can keep
	// walking rather than guess.
	var constString func(ast.Node) (string, bool)
	constString = func(n ast.Node) (string, bool) {
		switch e := n.(type) {
		case *ast.BasicLit:
			if e.Kind != token.STRING {
				return "", false
			}
			v, err := strconv.Unquote(e.Value)
			if err != nil {
				return "", false
			}
			return v, true
		case *ast.BinaryExpr:
			if e.Op != token.ADD {
				return "", false
			}
			left, ok := constString(e.X)
			if !ok {
				return "", false
			}
			right, ok := constString(e.Y)
			if !ok {
				return "", false
			}
			return left + right, true
		default:
			return "", false
		}
	}

	checked := 0
	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			ast.Inspect(file, func(n ast.Node) bool {
				value, ok := constString(n)
				if !ok {
					return true
				}
				checked++
				// A concatenation is checked here as a whole, and its child
				// literals must not be re-checked on their own: a `--` split
				// across the join would be reported twice. Returning false
				// stops the descent once a node has been folded.
				if strings.Contains(value, "--") {
					t.Errorf("%s: user-facing string contains \"--\" (%q); "+
						"it renders as two literal hyphens, so use an em dash instead",
						fset.Position(n.Pos()), value)
				}
				return false
			})
		}
	}
	if checked == 0 {
		t.Fatal("no string literals were checked; the test is not doing what it claims")
	}
}

// TestEveryGoFileHasTheSPDXHeader guards the whole module, not just this
// package. Every task adds files under main.go, internal/, and tools/, and this
// is the only check that sees all of them.
func TestEveryGoFileHasTheSPDXHeader(t *testing.T) {
	t.Parallel()

	const root = "../.."
	const want = "// SPDX-License-Identifier: GPL-3.0-or-later"
	skipDirs := map[string]bool{
		".git":         true,
		".claude":      true,
		".superpowers": true,
	}

	checked := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".go") {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("reading %s: %v", path, err)
			return nil
		}
		checked++
		if !strings.HasPrefix(string(content), want) {
			t.Errorf("%s does not start with %q", path, want)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
	if checked == 0 {
		t.Fatal("no Go files were checked; the test is not doing what it claims")
	}
}

// stringSet widens string values into the knownvalue check slice
// ListExact/SetExact take.
func stringSet(values ...string) []knownvalue.Check {
	checks := make([]knownvalue.Check, 0, len(values))
	for _, v := range values {
		checks = append(checks, knownvalue.StringExact(v))
	}
	return checks
}

// expectEmptyAfterRefresh is the check design section 4 invariant 1 requires
// of every step that applies configuration: after the apply and a refresh, the
// plan proposes nothing.
func expectEmptyAfterRefresh() resource.ConfigPlanChecks {
	return resource.ConfigPlanChecks{
		PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
	}
}
