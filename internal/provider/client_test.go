// SPDX-License-Identifier: GPL-3.0-or-later

package provider

import (
	"testing"

	"github.com/nijave/terraform-provider-namesilo/internal/namesilo"
)

// TestResolveAPIKey pins the resolution order: the attribute wins, then the
// environment variable, and an empty result is an error. Both variables are
// always set (possibly empty) so the tests are hermetic against the ambient
// environment.
func TestResolveAPIKey(t *testing.T) {
	tests := []struct {
		name       string
		configured string
		env        string
		want       string
		wantErr    bool
	}{
		{"configured wins", "configured-key", "env-key", "configured-key", false},
		{"environment fallback", "", "env-key", "env-key", false},
		{"both empty is an error", "", "", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("NAMESILO_API_KEY", tt.env)
			got, err := resolveAPIKey(tt.configured)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("resolveAPIKey(%q) = %q, nil error, want an error", tt.configured, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveAPIKey(%q) returned an unexpected error: %v", tt.configured, err)
			}
			if got != tt.want {
				t.Errorf("resolveAPIKey(%q) = %q, want %q", tt.configured, got, tt.want)
			}
		})
	}
}

// TestResolveEndpoint pins the resolution order: the attribute wins, then the
// environment variable, then the production endpoint.
func TestResolveEndpoint(t *testing.T) {
	tests := []struct {
		name       string
		configured string
		env        string
		want       string
	}{
		{"configured wins", "https://example.com/api", "https://env.example.com/api", "https://example.com/api"},
		{"environment fallback", "", "https://env.example.com/api", "https://env.example.com/api"},
		{"production default", "", "", namesilo.DefaultEndpoint},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("NAMESILO_API_ENDPOINT", tt.env)
			if got := resolveEndpoint(tt.configured); got != tt.want {
				t.Errorf("resolveEndpoint(%q) = %q, want %q", tt.configured, got, tt.want)
			}
		})
	}
}
