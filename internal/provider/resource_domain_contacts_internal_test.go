// SPDX-License-Identifier: GPL-3.0-or-later

package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/diag"

	"github.com/nijave/terraform-provider-namesilo/internal/namesilo"
)

// TestLaggingRolesNamesOnlyMismatchedManagedRoles pins the warning helper: a
// role the caller did not write (empty in want) is never reported, and the
// reported names are the resource's attribute names.
func TestLaggingRolesNamesOnlyMismatchedManagedRoles(t *testing.T) {
	want := namesilo.ContactRoles{Registrant: "1001", Administrative: "1002"}
	have := namesilo.ContactRoles{Registrant: "9999", Administrative: "1002", Technical: "1003", Billing: "1004"}

	if got := laggingRoles(have, want); len(got) != 1 || got[0] != "registrant" {
		t.Errorf("laggingRoles = %v, want [registrant]", got)
	}
}

// TestApplyAssociatedRolesWarnsOnTimeout exercises the timeout branch directly:
// a fake that never releases the written role makes the (shrunk) wait expire,
// and applyAssociatedRoles must report not-confirmed and add a warning naming
// the lagging role, rather than returning an error that would fail the apply.
func TestApplyAssociatedRolesWarnsOnTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/xml")
		switch r.URL.Path {
		case "/contactDomainAssociate":
			fmt.Fprint(w, `<namesilo><reply><code>300</code><detail>success</detail></reply></namesilo>`)
		case "/getDomainInfo":
			fmt.Fprint(w, `<namesilo><reply><code>300</code><detail>success</detail>`+
				`<created>2020-01-02</created><expires>2027-01-02</expires><status>active</status>`+
				`<locked>No</locked><private>No</private><auto_renew>No</auto_renew>`+
				`<traffic_type>U</traffic_type><email_verification_required>No</email_verification_required>`+
				`<portfolio></portfolio><forward_url>N/A</forward_url><forward_type>N/A</forward_type>`+
				`<nameservers></nameservers>`+
				`<contact_ids><registrant>9999</registrant><administrative>1002</administrative>`+
				`<technical>1003</technical><billing>1004</billing></contact_ids></reply></namesilo>`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	oldInterval, oldTimeout := DomainContactsPropagationPollInterval, DomainContactsPropagationTimeout
	DomainContactsPropagationPollInterval, DomainContactsPropagationTimeout = 5*time.Millisecond, 30*time.Millisecond
	defer func() {
		DomainContactsPropagationPollInterval, DomainContactsPropagationTimeout = oldInterval, oldTimeout
	}()

	r := &domainContactsResource{client: namesilo.NewClient(srv.URL, "test-key", "test")}
	var diags diag.Diagnostics
	_, confirmed, err := r.applyAssociatedRoles(context.Background(), &diags, "example.com",
		namesilo.ContactRoles{Registrant: "1001"})
	if err != nil {
		t.Fatalf("applyAssociatedRoles: %v, want no error on a propagation timeout", err)
	}
	if confirmed {
		t.Fatal("confirmed = true, want false when propagation times out")
	}
	if diags.HasError() {
		t.Fatalf("diagnostics = %+v, want no error (a warning must not fail the apply)", diags)
	}
	if got := diags.WarningsCount(); got != 1 {
		t.Fatalf("warnings = %d, want 1: %+v", got, diags)
	}
	w := diags.Warnings()[0]
	if !strings.Contains(w.Detail(), "registrant") {
		t.Errorf("warning detail %q does not name the lagging role", w.Detail())
	}
	if !strings.Contains(w.Detail(), "example.com") {
		t.Errorf("warning detail %q does not name the domain", w.Detail())
	}
}
