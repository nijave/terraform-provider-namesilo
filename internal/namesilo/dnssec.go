// SPDX-License-Identifier: GPL-3.0-or-later

package namesilo

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// parseDSInt reads one of the DS record's integer elements: keyTag,
// digestType, or algorithm. The error names the operation and the field and
// nothing else (§8.4: field names only, however public a digest is).
func parseDSInt(operation, field, value string) (int64, error) {
	n, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s: cannot interpret <%s> value %q as an integer", operation, field, value)
	}
	return n, nil
}

// dsRecordXML is one ds_record element in a dnsSecListRecords reply. The
// element names are the response's own spelling: camelCase keyTag and
// digestType and the full-word algorithm. The write request shares keyTag and
// digestType but abbreviates the third field to alg, so the reply's algorithm
// and the request's alg are the same field under two names. The asymmetry is
// the contract, not a bug to fix (§12.1).
type dsRecordXML struct {
	Digest     string `xml:"digest"`
	DigestType string `xml:"digestType"`
	Algorithm  string `xml:"algorithm"`
	KeyTag     string `xml:"keyTag"`
}

// dnsSecListRecordsReply is the whole dnsSecListRecords reply body.
type dnsSecListRecordsReply struct {
	Reply struct {
		replyHeader
		Records []dsRecordXML `xml:"ds_record"`
	} `xml:"reply"`
}

// ListDSRecords returns the domain's DS records in the order the API sent
// them. The digest is returned raw, uppercase included: the provider layer
// normalizes it at plan time via NormalizeDSRecord (§6.2). A bare <ds_record/>
// with no fields, the shape NameSilo emits when it has nothing to say, is
// dropped rather than returned as a zero record (§8.4). An unsigned domain is
// an empty non-nil slice, not an error.
func (c *Client) ListDSRecords(ctx context.Context, domain string) ([]DSRecord, error) {
	var reply dnsSecListRecordsReply
	if err := c.call(ctx, "dnsSecListRecords", map[string]string{"domain": domain}, &reply); err != nil {
		return nil, err
	}

	records := make([]DSRecord, 0, len(reply.Reply.Records))
	for _, r := range reply.Reply.Records {
		if strings.TrimSpace(r.KeyTag) == "" &&
			strings.TrimSpace(r.Algorithm) == "" &&
			strings.TrimSpace(r.DigestType) == "" &&
			strings.TrimSpace(r.Digest) == "" {
			continue
		}
		record := DSRecord{Digest: r.Digest}
		var err error
		if record.KeyTag, err = parseDSInt("dnsSecListRecords", "keyTag", r.KeyTag); err != nil {
			return nil, err
		}
		if record.Algorithm, err = parseDSInt("dnsSecListRecords", "algorithm", r.Algorithm); err != nil {
			return nil, err
		}
		if record.DigestType, err = parseDSInt("dnsSecListRecords", "digestType", r.DigestType); err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, nil
}

// dsRecordParams builds the parameters the DNSSEC write operations share.
// The request carries the digest as the caller set it, and the field names are
// the API's request spelling: camelCase keyTag and digestType and the
// abbreviated alg, never the response's full-word algorithm (§12.1). Callers
// that need the API's stored digest case adjust it before calling.
func dsRecordParams(domain string, record DSRecord) map[string]string {
	return map[string]string{
		"domain":     domain,
		"digest":     record.Digest,
		"keyTag":     strconv.FormatInt(record.KeyTag, 10),
		"digestType": strconv.FormatInt(record.DigestType, 10),
		"alg":        strconv.FormatInt(record.Algorithm, 10),
	}
}

// AddDSRecord adds one DS record to the domain's DNSSEC delegation. The digest
// is sent as given: dnsSecAddRecord accepts any case, and the API stores it
// uppercased.
func (c *Client) AddDSRecord(ctx context.Context, domain string, record DSRecord) error {
	return c.call(ctx, "dnsSecAddRecord", dsRecordParams(domain, record), nil)
}

// DeleteDSRecord removes one DS record from the domain's DNSSEC delegation.
//
// The request sends the digest uppercased. The API matches the digest
// case-sensitively against the stored form and stores digests uppercased
// (dnsSecAddRecord accepts any case; dnsSecListRecords echoes uppercase), so a
// lowercase delete is answered with code 210 "There are no active records
// specified for deletion". The provider keeps the digest lowercase in state for
// diff equality (NormalizeDSRecord), so the stored-case form is rebuilt here on
// the wire only.
func (c *Client) DeleteDSRecord(ctx context.Context, domain string, record DSRecord) error {
	record.Digest = strings.ToUpper(record.Digest)
	return c.call(ctx, "dnsSecDeleteRecord", dsRecordParams(domain, record), nil)
}
