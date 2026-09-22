// SPDX-License-Identifier: GPL-3.0-or-later

package namesilo

import (
	"os"
	"path/filepath"
	"testing"
)

// fixtureDomain is the disposable domain the live corpus was captured
// against. Tests that read a fixture use it so the request and the reply tell
// the same story.
const fixtureDomain = "testing-1519107.top"

// loadFixture reads one captured NameSilo reply from testdata. The corpus is
// real traffic captured live against the API on 2026-09-22 and sanitized
// (personal data replaced with similar placeholders; the request IP is RFC
// 5737 TEST-NET). It is ground truth for the reply shapes, so tests read it
// rather than spelling a shape from memory, and it is never edited: an
// assertion that disagrees with a fixture is adjusted to the fixture.
func loadFixture(t *testing.T, name string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("reading testdata/%s: %v", name, err)
	}
	return string(body)
}
