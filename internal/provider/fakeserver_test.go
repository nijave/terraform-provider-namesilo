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

// setDSRecords mutates one domain's DS records out of band, the way a change
// in NameSilo's web UI would. The drift tests call it from PreConfig.
func (f *fakeNamesilo) setDSRecords(name string, records []namesilo.DSRecord) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if d, ok := f.domains[name]; ok {
		d.dsRecords = append([]namesilo.DSRecord(nil), records...)
	}
}

// setPrivate flips one domain's WHOIS privacy out of band, the way a change in
// NameSilo's web UI would. The privacy drift tests call it from PreConfig; the
// already-in-state tests call it from a PreApply plan check, which runs after
// the plan but before the apply, so the toggle's call lands on the real
// handler's already-in-state reply.
func (f *fakeNamesilo) setPrivate(name string, private bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if d, ok := f.domains[name]; ok {
		d.private = private
	}
}

// dsRecordsFor returns a copy of one domain's DS records, so a CheckDestroy
// can assert the destroy left none behind without racing a handler.
func (f *fakeNamesilo) dsRecordsFor(name string) []namesilo.DSRecord {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.domains[name]
	if !ok {
		return nil
	}
	return append([]namesilo.DSRecord(nil), d.dsRecords...)
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
	case "dnsSecListRecords":
		f.handleDNSSecListRecords(w, "dnsSecListRecords", query.Get("domain"))
	case "dnsSecAddRecord":
		f.handleDNSSecAddRecord(w, "dnsSecAddRecord", query)
	case "dnsSecDeleteRecord":
		f.handleDNSSecDeleteRecord(w, "dnsSecDeleteRecord", query)
	case "addPrivacy":
		f.handleAddPrivacy(w, "addPrivacy", query.Get("domain"))
	case "removePrivacy":
		f.handleRemovePrivacy(w, "removePrivacy", query.Get("domain"))
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

// handleDNSSecListRecords renders the domain's DS records in the response's
// own snake_case shape: digest, digest_type, algorithm, and key_tag. An
// unsigned domain emits no ds_record element at all, so the reply body is
// empty and the client's empty-slice contract is exercised end to end.
func (f *fakeNamesilo) handleDNSSecListRecords(w http.ResponseWriter, operation, domain string) {
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
	for _, record := range d.dsRecords {
		b.WriteString("<ds_record>")
		b.WriteString("<key_tag>" + strconv.FormatInt(record.KeyTag, 10) + "</key_tag>")
		b.WriteString("<algorithm>" + strconv.FormatInt(record.Algorithm, 10) + "</algorithm>")
		b.WriteString("<digest_type>" + strconv.FormatInt(record.DigestType, 10) + "</digest_type>")
		b.WriteString("<digest>" + xmlEscape(record.Digest) + "</digest>")
		b.WriteString("</ds_record>")
	}

	writeReply(w, operation, "300", "success", b.String())
}

// dsRecordFromQuery reads one DS record off a dnsSecAddRecord or
// dnsSecDeleteRecord request. The request spells the fields its own way
// (keyTag, digestType, alg), the asymmetry the client documents (§12.1).
func dsRecordFromQuery(query map[string][]string) (namesilo.DSRecord, error) {
	keyTag, err := strconv.ParseInt(firstValue(query, "keyTag"), 10, 64)
	if err != nil {
		return namesilo.DSRecord{}, fmt.Errorf("keyTag: %v", err)
	}
	algorithm, err := strconv.ParseInt(firstValue(query, "alg"), 10, 64)
	if err != nil {
		return namesilo.DSRecord{}, fmt.Errorf("alg: %v", err)
	}
	digestType, err := strconv.ParseInt(firstValue(query, "digestType"), 10, 64)
	if err != nil {
		return namesilo.DSRecord{}, fmt.Errorf("digestType: %v", err)
	}
	return namesilo.DSRecord{
		KeyTag:     keyTag,
		Algorithm:  algorithm,
		DigestType: digestType,
		Digest:     firstValue(query, "digest"),
	}, nil
}

// handleDNSSecAddRecord appends one DS record to the domain and replies 300.
// The record is stored as sent: the digest's case is the provider layer's
// concern, and the fake is deliberately not helpful about it.
func (f *fakeNamesilo) handleDNSSecAddRecord(w http.ResponseWriter, operation string, query map[string][]string) {
	domain := firstValue(query, "domain")
	record, err := dsRecordFromQuery(query)
	if err != nil {
		writeReply(w, operation, "400", "malformed DS record: "+err.Error(), "")
		return
	}

	f.mu.Lock()
	d, ok := f.domains[domain]
	if ok {
		d.dsRecords = append(d.dsRecords, record)
	}
	f.mu.Unlock()

	if !ok {
		writeReply(w, operation, "200", "Domain is not active, or does not belong to this user", "")
		return
	}
	writeReply(w, operation, "300", "success", "")
}

// handleDNSSecDeleteRecord removes the matching DS record, comparing tuples
// canonically so a record stored with a differently-cased digest still matches.
// Deleting a record that is not present is a no-op, which is what makes the
// provider's list-then-delete reconcile idempotent (§4 invariant 3).
func (f *fakeNamesilo) handleDNSSecDeleteRecord(w http.ResponseWriter, operation string, query map[string][]string) {
	domain := firstValue(query, "domain")
	record, err := dsRecordFromQuery(query)
	if err != nil {
		writeReply(w, operation, "400", "malformed DS record: "+err.Error(), "")
		return
	}
	want := namesilo.NormalizeDSRecord(record).Key()

	f.mu.Lock()
	d, ok := f.domains[domain]
	if ok {
		kept := make([]namesilo.DSRecord, 0, len(d.dsRecords))
		for _, existing := range d.dsRecords {
			if namesilo.NormalizeDSRecord(existing).Key() != want {
				kept = append(kept, existing)
			}
		}
		d.dsRecords = kept
	}
	f.mu.Unlock()

	if !ok {
		writeReply(w, operation, "200", "Domain is not active, or does not belong to this user", "")
		return
	}
	writeReply(w, operation, "300", "success", "")
}

// handleAddPrivacy turns WHOIS privacy on. A domain that is already private
// answers with the real API's code 255 ("Domain is already private") instead
// of 300, which is what exercises the client's alreadyInState classification
// and the resource's idempotence against a race with the web UI (§12.2).
func (f *fakeNamesilo) handleAddPrivacy(w http.ResponseWriter, operation, domain string) {
	f.mu.Lock()
	d, ok := f.domains[domain]
	alreadyPrivate := ok && d.private
	if ok && !d.private {
		d.private = true
	}
	f.mu.Unlock()

	if !ok {
		writeReply(w, operation, "200", "Domain is not active, or does not belong to this user", "")
		return
	}
	if alreadyPrivate {
		writeReply(w, operation, "255", "Domain is already private", "")
		return
	}
	writeReply(w, operation, "300", "success", "")
}

// handleRemovePrivacy turns WHOIS privacy off. It is symmetric with
// handleAddPrivacy: a domain that is not private answers code 256 ("Domain is
// already not private") instead of 300.
func (f *fakeNamesilo) handleRemovePrivacy(w http.ResponseWriter, operation, domain string) {
	f.mu.Lock()
	d, ok := f.domains[domain]
	alreadyNotPrivate := ok && !d.private
	if ok && d.private {
		d.private = false
	}
	f.mu.Unlock()

	if !ok {
		writeReply(w, operation, "200", "Domain is not active, or does not belong to this user", "")
		return
	}
	if alreadyNotPrivate {
		writeReply(w, operation, "256", "Domain is already not private", "")
		return
	}
	writeReply(w, operation, "300", "success", "")
}

// cloneFakeDomain copies a domain so a handler can render it after releasing
// the lock. Slices and the roles struct are copied, including the DS records.
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
