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

	// contacts is the account's contact profiles, keyed by contact_id, guarded
	// by mu like the domains.
	contacts map[string]namesilo.Contact

	// ignorePaging makes listDomains answer every request with page 1
	// regardless of the page parameter, the way an API that ignores the
	// paging parameters would. The domains paging-guard test turns it on to
	// drive the client's duplicate-page guard and the data source's
	// incomplete-list warning (§7).
	ignorePaging bool

	// nextContactID is the contactAdd ID sequence: the first assigned ID is
	// 1001.
	nextContactID int
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

	// forwardURL and forwardType are the domain's forwarding fields, exposed
	// exactly as the API sends them: "N/A" when forwarding is off (§7).
	forwardURL  string
	forwardType string

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
		domains:       make(map[string]*fakeDomain),
		failures:      make(map[string]fakeFailure),
		contacts:      make(map[string]namesilo.Contact),
		nextContactID: 1000,
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
	// forwardURL and forwardType default to the API's "no forwarding" value,
	// exactly what a plain domain replies with (§7).
	d := &fakeDomain{status: "active", forwardURL: "N/A", forwardType: "N/A"}
	f.domains[name] = d
	return d
}

// setIgnorePaging flips the listDomains knob that answers every request with
// page 1 regardless of the page parameter.
func (f *fakeNamesilo) setIgnorePaging(ignore bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ignorePaging = ignore
}

// setForward rewrites one domain's forwarding fields out of band, the way a
// change in NameSilo's web UI would. The domain data source test calls it from
// PreConfig to prove forward_url and forward_type pass through unchanged.
func (f *fakeNamesilo) setForward(name, forwardURL, forwardType string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if d, ok := f.domains[name]; ok {
		d.forwardURL = forwardURL
		d.forwardType = forwardType
	}
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

// setLocked flips one domain's registrar lock out of band, the way a change in
// NameSilo's web UI would. The lock drift tests call it from PreConfig; the
// already-in-state tests call it from a PreApply plan check, which runs after
// the plan but before the apply, so the toggle's call lands on the real
// handler's already-in-state reply.
func (f *fakeNamesilo) setLocked(name string, locked bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if d, ok := f.domains[name]; ok {
		d.locked = locked
	}
}

// setAutoRenew flips one domain's auto-renew flag out of band, the way a
// change in NameSilo's web UI would. The auto-renew drift tests call it from
// PreConfig; the already-in-state tests call it from a PreApply plan check,
// which runs after the plan but before the apply, so the toggle's call lands
// on the real handler's already-in-state reply.
func (f *fakeNamesilo) setAutoRenew(name string, autoRenew bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if d, ok := f.domains[name]; ok {
		d.autoRenew = autoRenew
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

// setRoles reassigns one domain's contact associations out of band, the way a
// change in NameSilo's web UI would. The drift tests call it from PreConfig.
func (f *fakeNamesilo) setRoles(name string, roles namesilo.ContactRoles) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if d, ok := f.domains[name]; ok {
		d.roles = roles
	}
}

// rolesFor returns a copy of one domain's contact associations and whether the
// domain exists, so a CheckDestroy can assert the destroy left them intact
// without racing a handler.
func (f *fakeNamesilo) rolesFor(name string) (namesilo.ContactRoles, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.domains[name]
	if !ok {
		return namesilo.ContactRoles{}, false
	}
	return d.roles, true
}

// failWith makes an operation always reply with the given code and detail.
func (f *fakeNamesilo) failWith(operation, code, detail string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failures[operation] = fakeFailure{code: code, detail: detail}
}

// clearFailure removes a failure injection. The destroy-blocked harness test
// arms contactDelete with failWith to surface the "still associated with a
// domain" refusal, then clears it so a follow-up destroy can clean up.
func (f *fakeNamesilo) clearFailure(operation string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.failures, operation)
}

// seedContact stores a profile directly under the given contact_id. Tests use
// it to seed the account's existing profiles (an import target, the account
// default, the gone-profile scenario) before the provider ever runs.
func (f *fakeNamesilo) seedContact(id string, contact namesilo.Contact) {
	f.mu.Lock()
	defer f.mu.Unlock()
	contact.ID = id
	f.contacts[id] = contact
}

// mutateContact edits a stored profile out of band, the way a change in
// NameSilo's web UI would. The drift tests call it from PreConfig. It reports
// whether the profile existed.
func (f *fakeNamesilo) mutateContact(id string, edit func(*namesilo.Contact)) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	contact, ok := f.contacts[id]
	if !ok {
		return false
	}
	edit(&contact)
	f.contacts[id] = contact
	return true
}

// removeContact deletes a stored profile out of band, the way a profile
// deleted in the web UI would. The gone tests call it from PreConfig.
func (f *fakeNamesilo) removeContact(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.contacts, id)
}

// contactFor returns a copy of a stored profile and whether it exists, so a
// CheckDestroy can assert a contact is gone without racing a handler.
func (f *fakeNamesilo) contactFor(id string) (namesilo.Contact, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	contact, ok := f.contacts[id]
	return contact, ok
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
	case "domainLock":
		f.handleDomainLock(w, "domainLock", query.Get("domain"))
	case "domainUnlock":
		f.handleDomainUnlock(w, "domainUnlock", query.Get("domain"))
	case "addAutoRenewal":
		f.handleAddAutoRenewal(w, "addAutoRenewal", query.Get("domain"))
	case "removeAutoRenewal":
		f.handleRemoveAutoRenewal(w, "removeAutoRenewal", query.Get("domain"))
	case "contactList":
		f.handleContactList(w, "contactList", query)
	case "contactAdd":
		f.handleContactAdd(w, "contactAdd", query)
	case "contactUpdate":
		f.handleContactUpdate(w, "contactUpdate", query)
	case "contactDelete":
		f.handleContactDelete(w, "contactDelete", query)
	case "contactDomainAssociate":
		f.handleContactDomainAssociate(w, "contactDomainAssociate", query)
	case "listDomains":
		f.handleListDomains(w, "listDomains", query)
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
	b.WriteString("<forward_url>" + xmlEscape(d.forwardURL) + "</forward_url>")
	b.WriteString("<forward_type>" + xmlEscape(d.forwardType) + "</forward_type>")
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

// handleListDomains pages the account's domains: page and pageSize slice the
// fake's domain set, sorted by name so the pages are deterministic, and the
// reply echoes the pager element the client's loop is driven by. When
// ignorePaging is set, every request is answered with page 1 regardless of the
// page parameter, the way a broken API behaves: the client sees a repeated
// page, stops with Truncated set, and the data source warns that the list may
// be incomplete (§7, §4 invariant 13).
func (f *fakeNamesilo) handleListDomains(w http.ResponseWriter, operation string, query map[string][]string) {
	page, err := strconv.ParseInt(firstValue(query, "page"), 10, 64)
	if err != nil || page < 1 {
		page = 1
	}
	pageSize, err := strconv.ParseInt(firstValue(query, "pageSize"), 10, 64)
	if err != nil || pageSize < 1 {
		pageSize = 100
	}

	f.mu.Lock()
	names := make([]string, 0, len(f.domains))
	for name := range f.domains {
		names = append(names, name)
	}
	sort.Strings(names)
	total := int64(len(names))
	if f.ignorePaging {
		page = 1
	}
	rows := make([]namesilo.DomainSummary, 0, pageSize)
	for i := (page - 1) * pageSize; i < page*pageSize && i < total; i++ {
		d := f.domains[names[i]]
		rows = append(rows, namesilo.DomainSummary{
			Name:    names[i],
			Created: d.created,
			Expires: d.expires,
		})
	}
	f.mu.Unlock()

	var b strings.Builder
	b.WriteString("<domains>")
	for _, row := range rows {
		b.WriteString(`<domain created="` + xmlEscape(row.Created) +
			`" expires="` + xmlEscape(row.Expires) + `">` + xmlEscape(row.Name) + "</domain>")
	}
	b.WriteString("</domains>")
	b.WriteString("<pager>")
	b.WriteString("<total>" + strconv.FormatInt(total, 10) + "</total>")
	b.WriteString("<pageSize>" + strconv.FormatInt(pageSize, 10) + "</pageSize>")
	b.WriteString("<page>" + strconv.FormatInt(page, 10) + "</page>")
	b.WriteString("</pager>")

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

// handleDomainLock locks the domain against transfer. A domain that is already
// locked answers with the real API's code 252 ("Domain is already locked")
// instead of 300, which is what exercises the client's alreadyInState
// classification and the resource's idempotence against a race with the web
// UI (§12.2).
func (f *fakeNamesilo) handleDomainLock(w http.ResponseWriter, operation, domain string) {
	f.mu.Lock()
	d, ok := f.domains[domain]
	alreadyLocked := ok && d.locked
	if ok && !d.locked {
		d.locked = true
	}
	f.mu.Unlock()

	if !ok {
		writeReply(w, operation, "200", "Domain is not active, or does not belong to this user", "")
		return
	}
	if alreadyLocked {
		writeReply(w, operation, "252", "Domain is already locked", "")
		return
	}
	writeReply(w, operation, "300", "success", "")
}

// handleDomainUnlock unlocks the domain. It is symmetric with handleDomainLock:
// a domain that is not locked answers code 253 ("Domain is already unlocked")
// instead of 300.
func (f *fakeNamesilo) handleDomainUnlock(w http.ResponseWriter, operation, domain string) {
	f.mu.Lock()
	d, ok := f.domains[domain]
	alreadyUnlocked := ok && !d.locked
	if ok && d.locked {
		d.locked = false
	}
	f.mu.Unlock()

	if !ok {
		writeReply(w, operation, "200", "Domain is not active, or does not belong to this user", "")
		return
	}
	if alreadyUnlocked {
		writeReply(w, operation, "253", "Domain is already unlocked", "")
		return
	}
	writeReply(w, operation, "300", "success", "")
}

// handleAddAutoRenewal turns auto-renewal on. A domain that already has
// auto-renew answers with the real API's code 250 ("Domain is already set to
// AutoRenew") instead of 300, exercising the client's alreadyInState
// classification the same way the privacy handlers do.
func (f *fakeNamesilo) handleAddAutoRenewal(w http.ResponseWriter, operation, domain string) {
	f.mu.Lock()
	d, ok := f.domains[domain]
	alreadyAutoRenew := ok && d.autoRenew
	if ok && !d.autoRenew {
		d.autoRenew = true
	}
	f.mu.Unlock()

	if !ok {
		writeReply(w, operation, "200", "Domain is not active, or does not belong to this user", "")
		return
	}
	if alreadyAutoRenew {
		writeReply(w, operation, "250", "Domain is already set to AutoRenew", "")
		return
	}
	writeReply(w, operation, "300", "success", "")
}

// handleRemoveAutoRenewal turns auto-renewal off. It is symmetric with
// handleAddAutoRenewal: a domain without auto-renew answers code 251 ("Domain
// is already set not to AutoRenew") instead of 300.
func (f *fakeNamesilo) handleRemoveAutoRenewal(w http.ResponseWriter, operation, domain string) {
	f.mu.Lock()
	d, ok := f.domains[domain]
	alreadyNotAutoRenew := ok && !d.autoRenew
	if ok && d.autoRenew {
		d.autoRenew = false
	}
	f.mu.Unlock()

	if !ok {
		writeReply(w, operation, "200", "Domain is not active, or does not belong to this user", "")
		return
	}
	if alreadyNotAutoRenew {
		writeReply(w, operation, "251", "Domain is already set not to AutoRenew", "")
		return
	}
	writeReply(w, operation, "300", "success", "")
}

// contactFromQuery reads a Contact off a contactAdd or contactUpdate request.
// Every field is sent under the API's short names, empty values included
// (§8.1), so an absent parameter and an empty one both read as "". Field names
// with no request parameter (contact_id on an add) are left empty.
func contactFromQuery(query map[string][]string) namesilo.Contact {
	return namesilo.Contact{
		ID:                   firstValue(query, "contact_id"),
		Nickname:             firstValue(query, "nn"),
		Company:              firstValue(query, "cp"),
		FirstName:            firstValue(query, "fn"),
		LastName:             firstValue(query, "ln"),
		Address:              firstValue(query, "ad"),
		Address2:             firstValue(query, "ad2"),
		City:                 firstValue(query, "cy"),
		State:                firstValue(query, "st"),
		Zip:                  firstValue(query, "zp"),
		Country:              firstValue(query, "ct"),
		Email:                firstValue(query, "em"),
		Phone:                firstValue(query, "ph"),
		Fax:                  firstValue(query, "fx"),
		UsNexusCategory:      firstValue(query, "usnc"),
		UsApplicationPurpose: firstValue(query, "usap"),
		CaLegalForm:          firstValue(query, "calf"),
		CaLanguage:           firstValue(query, "caln"),
		CaAgreementVersion:   firstValue(query, "caag"),
		CaWhoisDisplay:       firstValue(query, "cawd"),
		EuCitizenshipCountry: firstValue(query, "eucs"),
	}
}

// contactReply renders one <contact> element. Every field is emitted; unset
// optionals arrive as empty elements, the shape the client normalizes to empty
// strings. default_profile is always the strict 1/0 the client requires.
func contactReply(contact namesilo.Contact) string {
	defaultProfile := "0"
	if contact.DefaultProfile {
		defaultProfile = "1"
	}
	var b strings.Builder
	b.WriteString("<contact>")
	b.WriteString("<contact_id>" + xmlEscape(contact.ID) + "</contact_id>")
	b.WriteString("<default_profile>" + defaultProfile + "</default_profile>")
	b.WriteString("<nn>" + xmlEscape(contact.Nickname) + "</nn>")
	b.WriteString("<cp>" + xmlEscape(contact.Company) + "</cp>")
	b.WriteString("<fn>" + xmlEscape(contact.FirstName) + "</fn>")
	b.WriteString("<ln>" + xmlEscape(contact.LastName) + "</ln>")
	b.WriteString("<ad>" + xmlEscape(contact.Address) + "</ad>")
	b.WriteString("<ad2>" + xmlEscape(contact.Address2) + "</ad2>")
	b.WriteString("<cy>" + xmlEscape(contact.City) + "</cy>")
	b.WriteString("<st>" + xmlEscape(contact.State) + "</st>")
	b.WriteString("<zp>" + xmlEscape(contact.Zip) + "</zp>")
	b.WriteString("<ct>" + xmlEscape(contact.Country) + "</ct>")
	b.WriteString("<em>" + xmlEscape(contact.Email) + "</em>")
	b.WriteString("<ph>" + xmlEscape(contact.Phone) + "</ph>")
	b.WriteString("<fx>" + xmlEscape(contact.Fax) + "</fx>")
	b.WriteString("<usnc>" + xmlEscape(contact.UsNexusCategory) + "</usnc>")
	b.WriteString("<usap>" + xmlEscape(contact.UsApplicationPurpose) + "</usap>")
	b.WriteString("<calf>" + xmlEscape(contact.CaLegalForm) + "</calf>")
	b.WriteString("<caln>" + xmlEscape(contact.CaLanguage) + "</caln>")
	b.WriteString("<caag>" + xmlEscape(contact.CaAgreementVersion) + "</caag>")
	b.WriteString("<cawd>" + xmlEscape(contact.CaWhoisDisplay) + "</cawd>")
	b.WriteString("<eucs>" + xmlEscape(contact.EuCitizenshipCountry) + "</eucs>")
	b.WriteString("</contact>")
	return b.String()
}

// handleContactList renders the account's profiles. A non-empty contact_id
// asks for that one profile (or none); an empty one asks for every profile,
// ordered by contact_id for determinism.
func (f *fakeNamesilo) handleContactList(w http.ResponseWriter, operation string, query map[string][]string) {
	id := firstValue(query, "contact_id")

	f.mu.Lock()
	contacts := make([]namesilo.Contact, 0, len(f.contacts))
	if id != "" {
		if contact, ok := f.contacts[id]; ok {
			contacts = append(contacts, contact)
		}
	} else {
		for _, contact := range f.contacts {
			contacts = append(contacts, contact)
		}
		sort.Slice(contacts, func(i, j int) bool { return contacts[i].ID < contacts[j].ID })
	}
	f.mu.Unlock()

	var b strings.Builder
	for _, contact := range contacts {
		b.WriteString(contactReply(contact))
	}
	writeReply(w, operation, "300", "success", b.String())
}

// handleContactAdd stores the submitted profile under the next sequential
// contact_id and echoes it in the reply. The new profile is not the account
// default: only pre-seeded profiles carry default_profile = 1.
func (f *fakeNamesilo) handleContactAdd(w http.ResponseWriter, operation string, query map[string][]string) {
	contact := contactFromQuery(query)

	f.mu.Lock()
	f.nextContactID++
	id := strconv.Itoa(f.nextContactID)
	contact.ID = id
	contact.DefaultProfile = false
	f.contacts[id] = contact
	f.mu.Unlock()

	writeReply(w, operation, "300", "success", "<contact_id>"+id+"</contact_id>")
}

// handleContactUpdate replaces the stored profile's fields, preserving the
// account-level default_profile, which contactUpdate cannot set. An unknown
// contact_id answers the API's generic error code 210.
func (f *fakeNamesilo) handleContactUpdate(w http.ResponseWriter, operation string, query map[string][]string) {
	id := firstValue(query, "contact_id")
	contact := contactFromQuery(query)

	f.mu.Lock()
	existing, ok := f.contacts[id]
	if ok {
		contact.ID = id
		contact.DefaultProfile = existing.DefaultProfile
		f.contacts[id] = contact
	}
	f.mu.Unlock()

	if !ok {
		writeReply(w, operation, "210", "General error: unknown contact_id", "")
		return
	}
	writeReply(w, operation, "300", "success", "")
}

// handleContactDelete removes the profile. It is idempotent: deleting a
// profile that is already gone succeeds, the way a destroy retried after a
// manual web-UI delete should. The "still associated with a domain" refusal is
// injected through failWith, since it is a property of the account's
// associations rather than of the profile.
func (f *fakeNamesilo) handleContactDelete(w http.ResponseWriter, operation string, query map[string][]string) {
	id := firstValue(query, "contact_id")

	f.mu.Lock()
	delete(f.contacts, id)
	f.mu.Unlock()

	writeReply(w, operation, "300", "success", "")
}

// handleContactDomainAssociate points the request's roles at the contact IDs
// it carries and replies 300. Absent roles are left untouched: the API has no
// way to clear a role, so an omitted parameter means "not managed", and this
// is what makes the resource's partial management observable in the request
// log (§6.5). An empty value is treated as absent, matching the client, which
// sends only non-empty roles.
func (f *fakeNamesilo) handleContactDomainAssociate(w http.ResponseWriter, operation string, query map[string][]string) {
	domain := firstValue(query, "domain")

	f.mu.Lock()
	d, ok := f.domains[domain]
	if ok {
		if v := firstValue(query, "registrant"); v != "" {
			d.roles.Registrant = v
		}
		if v := firstValue(query, "administrative"); v != "" {
			d.roles.Administrative = v
		}
		if v := firstValue(query, "technical"); v != "" {
			d.roles.Technical = v
		}
		if v := firstValue(query, "billing"); v != "" {
			d.roles.Billing = v
		}
	}
	f.mu.Unlock()

	if !ok {
		writeReply(w, operation, "200", "Domain is not active, or does not belong to this user", "")
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
