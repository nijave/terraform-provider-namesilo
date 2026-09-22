// SPDX-License-Identifier: GPL-3.0-or-later

package provider_test

import (
	"encoding/xml"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/nijave/terraform-provider-namesilo/internal/namesilo"
)

// fakeNamesilo is an in-process stand-in for the NameSilo API. Harness tests
// point the provider's endpoint at URL(), drive real Create/Read/Update/Delete
// through the framework, and then assert on the recorded request log.
//
// The store is guarded by one mutex: httptest serves each request on its own
// goroutine, and a harness test's PreConfig (drift seeding) writes to the same
// store from the test goroutine while the provider reads it.
type fakeNamesilo struct {
	mu sync.Mutex

	server *httptest.Server

	domains map[string]*fakeDomain

	// log records one entry per request, in arrival order, as
	// "operation|sorted params". The API key is excluded by construction, so
	// the log can never leak it.
	log []string

	// failures maps an operation to the reply it always answers with instead
	// of its real behaviour.
	failures map[string]fakeFailure

	// contacts and associations are unused in this task; later resource tasks
	// extend the store for them.
	contacts map[string]map[string]string
}

// fakeDomain is one domain's mutable registrar state. Fields are exported to
// the harness test via the pointer addDomain returns; tests seed them directly.
type fakeDomain struct {
	created string
	expires string
	status  string

	locked    bool
	private   bool
	autoRenew bool

	nameservers []string
	dsRecords   []namesilo.DSRecord
	roles       namesilo.ContactRoles
}

// fakeFailure is an injected reply: every request for the operation answers
// with code and detail instead of the operation's real behaviour.
type fakeFailure struct {
	code   string
	detail string
}

// newFakeNamesilo starts the fake server. Callers must Close it.
func newFakeNamesilo() *fakeNamesilo {
	f := &fakeNamesilo{
		domains:  make(map[string]*fakeDomain),
		failures: make(map[string]fakeFailure),
		contacts: make(map[string]map[string]string),
	}
	f.server = httptest.NewServer(http.HandlerFunc(f.handle))
	return f
}

// URL is the endpoint the provider is configured with.
func (f *fakeNamesilo) URL() string {
	return f.server.URL
}

// Close stops the server.
func (f *fakeNamesilo) Close() {
	f.server.Close()
}

// addDomain creates an empty domain and returns it so the caller can seed its
// fields directly. It panics on a duplicate: two fixture rows for one name
// would make a test's expectations depend on map iteration order.
func (f *fakeNamesilo) addDomain(name string) *fakeDomain {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.domains[name]; ok {
		panic("fake: addDomain called twice for " + name)
	}
	d := &fakeDomain{status: "active"}
	f.domains[name] = d
	return d
}

// setNameservers mutates one domain's nameservers out of band, the way a
// change in NameSilo's web UI would. The drift test calls it from PreConfig.
func (f *fakeNamesilo) setNameservers(name string, nameservers []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if d, ok := f.domains[name]; ok {
		d.nameservers = append([]string(nil), nameservers...)
	}
}

// failWith makes an operation always reply with the given code and detail.
func (f *fakeNamesilo) failWith(operation, code, detail string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failures[operation] = fakeFailure{code: code, detail: detail}
}

// ops is every recorded request, in arrival order.
func (f *fakeNamesilo) ops() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.log...)
}

// count is how many requests the operation received.
func (f *fakeNamesilo) count(operation string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, entry := range f.log {
		if opOf(entry) == operation {
			n++
		}
	}
	return n
}

// lastRequest is the most recent recorded request for the operation, or "".
func (f *fakeNamesilo) lastRequest(operation string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.log) - 1; i >= 0; i-- {
		if opOf(f.log[i]) == operation {
			return f.log[i]
		}
	}
	return ""
}

// handle is the whole server: one switch on the operation path. It owns the
// response envelope, so each handler only decides the reply's code, detail,
// and inner elements.
func (f *fakeNamesilo) handle(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	operation := strings.TrimPrefix(r.URL.Path, "/")
	key := query.Get("key")

	f.mu.Lock()
	entry := logEntry(operation, query)
	f.log = append(f.log, entry)
	failure, failing := f.failures[operation]
	f.mu.Unlock()

	// Wrong key: code 110, "Invalid API key". Checked before the operation
	// switch so a misconfigured provider is caught on its first call, but
	// after the log append so the attempt is on the record. The reply never
	// echoes the key.
	if key != "test-key" {
		writeReply(w, operation, "110", "Invalid API key", "")
		return
	}
	if failing {
		writeReply(w, operation, failure.code, failure.detail, "")
		return
	}

	switch operation {
	case "getDomainInfo":
		f.handleGetDomainInfo(w, "getDomainInfo", query.Get("domain"))
	case "changeNameServers":
		f.handleChangeNameServers(w, "changeNameServers", query)
	default:
		writeReply(w, operation, "400", "unsupported operation "+operation, "")
	}
}

// handleGetDomainInfo emits the full §8.3 getDomainInfo shape: every field the
// client decodes, with Yes/No booleans and position attributes on the
// nameservers.
func (f *fakeNamesilo) handleGetDomainInfo(w http.ResponseWriter, operation, domain string) {
	f.mu.Lock()
	d, ok := f.domains[domain]
	if ok {
		d = cloneFakeDomain(d)
	}
	f.mu.Unlock()

	if !ok {
		writeReply(w, operation, "200", "Domain is not active, or does not belong to this user", "")
		return
	}

	var b strings.Builder
	b.WriteString("<created>" + xmlEscape(d.created) + "</created>")
	b.WriteString("<expires>" + xmlEscape(d.expires) + "</expires>")
	b.WriteString("<status>" + xmlEscape(d.status) + "</status>")
	b.WriteString("<locked>" + yesNo(d.locked) + "</locked>")
	b.WriteString("<private>" + yesNo(d.private) + "</private>")
	b.WriteString("<auto_renew>" + yesNo(d.autoRenew) + "</auto_renew>")
	b.WriteString("<traffic_type></traffic_type>")
	b.WriteString("<email_verification_required>" + yesNo(false) + "</email_verification_required>")
	b.WriteString("<portfolio></portfolio>")
	b.WriteString("<forward_url></forward_url>")
	b.WriteString("<forward_type></forward_type>")
	b.WriteString("<nameservers>")
	for i, ns := range d.nameservers {
		b.WriteString(`<nameserver position="` + strconv.Itoa(i+1) + `">` + xmlEscape(ns) + "</nameserver>")
	}
	b.WriteString("</nameservers>")
	b.WriteString("<contact_ids>")
	b.WriteString("<registrant>" + xmlEscape(d.roles.Registrant) + "</registrant>")
	b.WriteString("<administrative>" + xmlEscape(d.roles.Administrative) + "</administrative>")
	b.WriteString("<technical>" + xmlEscape(d.roles.Technical) + "</technical>")
	b.WriteString("<billing>" + xmlEscape(d.roles.Billing) + "</billing>")
	b.WriteString("</contact_ids>")

	writeReply(w, operation, "300", "success", b.String())
}

// handleChangeNameServers replaces the domain's nameservers with ns1..nsN and
// replies 300.
func (f *fakeNamesilo) handleChangeNameServers(w http.ResponseWriter, operation string, query map[string][]string) {
	domain := firstValue(query, "domain")

	nameservers := make([]string, 0, 13)
	for i := 1; i <= 13; i++ {
		if v := firstValue(query, fmt.Sprintf("ns%d", i)); v != "" {
			nameservers = append(nameservers, v)
		}
	}

	f.mu.Lock()
	d, ok := f.domains[domain]
	if ok {
		nameservers = append([]string(nil), nameservers...)
	}
	f.mu.Unlock()

	if !ok {
		writeReply(w, operation, "200", "Domain is not active, or does not belong to this user", "")
		return
	}

	f.mu.Lock()
	d.nameservers = nameservers
	f.mu.Unlock()

	writeReply(w, operation, "300", "success", "")
}

// cloneFakeDomain copies a domain so a handler can render it after releasing
// the lock. Slices and the roles struct are copied; the DS records are not
// mutated by any handler in this task.
func cloneFakeDomain(d *fakeDomain) *fakeDomain {
	out := *d
	out.nameservers = append([]string(nil), d.nameservers...)
	out.dsRecords = append([]namesilo.DSRecord(nil), d.dsRecords...)
	return &out
}

// writeReply wraps inner content in the XML envelope every operation uses.
func writeReply(w http.ResponseWriter, operation, code, detail, inner string) {
	w.Header().Set("Content-Type", "text/xml; charset=utf-8")
	fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?>`+"\n"+
		"<namesilo>\n"+
		"<request><operation>%s</operation><ip>127.0.0.1</ip></request>\n"+
		"<reply>\n"+
		"<code>%s</code>\n"+
		"<detail>%s</detail>\n"+
		"%s\n"+
		"</reply>\n"+
		"</namesilo>\n",
		xmlEscape(operation), code, xmlEscape(detail), inner)
}

// logEntry renders one request as "operation|k=v&k=v". Parameters other than
// the key are sorted for determinism; the key is omitted entirely so the log
// cannot leak it.
func logEntry(operation string, query map[string][]string) string {
	params := make([]string, 0, len(query))
	for k, values := range query {
		if k == "key" {
			continue
		}
		for _, v := range values {
			params = append(params, k+"="+v)
		}
	}
	sort.Strings(params)
	return operation + "|" + strings.Join(params, "&")
}

// opOf is the operation part of a log entry.
func opOf(entry string) string {
	if i := strings.IndexByte(entry, '|'); i >= 0 {
		return entry[:i]
	}
	return entry
}

// firstValue reads the first value of a query parameter.
func firstValue(query map[string][]string, name string) string {
	if values := query[name]; len(values) > 0 {
		return values[0]
	}
	return ""
}

// xmlEscape escapes text for the reply's character data.
func xmlEscape(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

// yesNo renders the API's boolean spelling.
func yesNo(v bool) string {
	if v {
		return "Yes"
	}
	return "No"
}
