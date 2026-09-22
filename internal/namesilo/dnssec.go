// SPDX-License-Identifier: GPL-3.0-or-later

package namesilo

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// parseDSInt reads one of the DS record's integer elements: key_tag,
// digest_type, or algorithm. The error names the operation and the field and
// nothing else (§8.4: field names only, however public a digest is).
func parseDSInt(operation, field, value string) (int64, error) {
	n, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s: cannot interpret <%s> value %q as an integer", operation, field, value)
	}
	return n, nil
}

// dsRecordXML is one ds_record element in a dnsSecListRecords reply. The
// element names are the response's own spelling: the full-word algorithm and
// the snake_case key_tag and digest_type. The request parameters spell two of
// the same fields differently (keyTag, digestType, alg), and the asymmetry is
// the contract, not a bug to fix (§12.1).
type dsRecordXML struct {
	Digest     string `xml:"digest"`
	DigestType string `xml:"digest_type"`
	Algorithm  string `xml:"algorithm"`
	KeyTag     string `xml:"key_tag"`
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
		if record.KeyTag, err = parseDSInt("dnsSecListRecords", "key_tag", r.KeyTag); err != nil {
			return nil, err
		}
		if record.Algorithm, err = parseDSInt("dnsSecListRecords", "algorithm", r.Algorithm); err != nil {
			return nil, err
		}
		if record.DigestType, err = parseDSInt("dnsSecListRecords", "digest_type", r.DigestType); err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, nil
}

// dsRecordParams builds the parameters the DNSSEC write operations share.
// The request carries the digest unchanged, and the field names are the
// API's request spelling: camelCase keyTag and digestType and the abbreviated
// alg, never the response's full-word algorithm (§12.1).
func dsRecordParams(domain string, record DSRecord) map[string]string {
	return map[string]string{
		"domain":     domain,
		"digest":     record.Digest,
		"keyTag":     strconv.FormatInt(record.KeyTag, 10),
		"digestType": strconv.FormatInt(record.DigestType, 10),
		"alg":        strconv.FormatInt(record.Algorithm, 10),
	}
}

// AddDSRecord adds one DS record to the domain's DNSSEC delegation.
func (c *Client) AddDSRecord(ctx context.Context, domain string, record DSRecord) error {
	return c.call(ctx, "dnsSecAddRecord", dsRecordParams(domain, record), nil)
}

// DeleteDSRecord removes one DS record from the domain's DNSSEC delegation.
func (c *Client) DeleteDSRecord(ctx context.Context, domain string, record DSRecord) error {
	return c.call(ctx, "dnsSecDeleteRecord", dsRecordParams(domain, record), nil)
}
