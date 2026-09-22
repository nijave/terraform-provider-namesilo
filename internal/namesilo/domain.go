// SPDX-License-Identifier: GPL-3.0-or-later

package namesilo

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// ContactRoles is a set of contact associations on a domain. An empty string
// means "not managed", not "clear this role": the API has no way to clear a
// role, so an absent association is indistinguishable from an unmanaged one.
type ContactRoles struct {
	Registrant     string
	Administrative string
	Technical      string
	Billing        string
}

// DomainInfo is the getDomainInfo reply. Nameservers are the raw strings the
// API sent, uppercase and trailing-dotted included; normalization belongs to
// the provider layer (§6.1).
type DomainInfo struct {
	Created                   string
	Expires                   string
	Status                    string
	Locked                    bool
	Private                   bool
	AutoRenew                 bool
	TrafficType               string
	EmailVerificationRequired bool
	Portfolio                 string
	ForwardURL                string
	ForwardType               string
	Nameservers               []string
	Contacts                  ContactRoles
}

// DomainSummary is one listDomains row: the name element's text plus its
// created and expires attributes.
type DomainSummary struct {
	Name    string
	Created string
	Expires string
}

// DomainList is every page ListDomains fetched, plus the API's pager total.
// Truncated is true when paging stopped short of Total because a page added no
// new names (the duplicate-page guard), so the provider can warn that the
// inventory is incomplete without silently looping forever.
type DomainList struct {
	Domains   []DomainSummary
	Total     int64
	Truncated bool
}

// nameserverXML is one nameserver in the reply. The position attribute is an
// index the client preserves in element order; only the chardata is the name.
type nameserverXML struct {
	Position string `xml:"position,attr"`
	Value    string `xml:",chardata"`
}

// parseYesNo reads one of the API's boolean elements. Only Yes and No, in any
// case, are accepted; any other value is an error naming the operation and the
// field, because guessing would silently flip a domain's WHOIS exposure, lock
// state, auto-renewal, or verification state (§8.4).
func parseYesNo(operation, field, value string) (bool, error) {
	switch strings.ToLower(value) {
	case "yes":
		return true, nil
	case "no":
		return false, nil
	default:
		return false, fmt.Errorf("%s: cannot interpret <%s> value %q as Yes or No", operation, field, value)
	}
}

// getDomainInfoReply is the whole getDomainInfo reply body. replyHeader is
// embedded so the envelope matches the shape every operation shares.
type getDomainInfoReply struct {
	Reply struct {
		replyHeader
		Created                   string `xml:"created"`
		Expires                   string `xml:"expires"`
		Status                    string `xml:"status"`
		Locked                    string `xml:"locked"`
		Private                   string `xml:"private"`
		AutoRenew                 string `xml:"auto_renew"`
		TrafficType               string `xml:"traffic_type"`
		EmailVerificationRequired string `xml:"email_verification_required"`
		Portfolio                 string `xml:"portfolio"`
		ForwardURL                string `xml:"forward_url"`
		ForwardType               string `xml:"forward_type"`
		Nameservers               struct {
			Nameserver []nameserverXML `xml:"nameserver"`
		} `xml:"nameservers"`
		ContactIDs struct {
			Registrant     string `xml:"registrant"`
			Administrative string `xml:"administrative"`
			Technical      string `xml:"technical"`
			Billing        string `xml:"billing"`
		} `xml:"contact_ids"`
	} `xml:"reply"`
}

// GetDomainInfo reads one domain's registrar state.
func (c *Client) GetDomainInfo(ctx context.Context, domain string) (DomainInfo, error) {
	var reply getDomainInfoReply
	if err := c.call(ctx, "getDomainInfo", map[string]string{"domain": domain}, &reply); err != nil {
		return DomainInfo{}, err
	}
	r := reply.Reply

	info := DomainInfo{
		Created:     r.Created,
		Expires:     r.Expires,
		Status:      r.Status,
		TrafficType: r.TrafficType,
		Portfolio:   r.Portfolio,
		ForwardURL:  r.ForwardURL,
		ForwardType: r.ForwardType,
		Nameservers: make([]string, 0, len(r.Nameservers.Nameserver)),
		Contacts: ContactRoles{
			Registrant:     r.ContactIDs.Registrant,
			Administrative: r.ContactIDs.Administrative,
			Technical:      r.ContactIDs.Technical,
			Billing:        r.ContactIDs.Billing,
		},
	}
	for _, ns := range r.Nameservers.Nameserver {
		info.Nameservers = append(info.Nameservers, ns.Value)
	}

	var err error
	if info.Locked, err = parseYesNo("getDomainInfo", "locked", r.Locked); err != nil {
		return DomainInfo{}, err
	}
	if info.Private, err = parseYesNo("getDomainInfo", "private", r.Private); err != nil {
		return DomainInfo{}, err
	}
	if info.AutoRenew, err = parseYesNo("getDomainInfo", "auto_renew", r.AutoRenew); err != nil {
		return DomainInfo{}, err
	}
	if info.EmailVerificationRequired, err = parseYesNo("getDomainInfo", "email_verification_required", r.EmailVerificationRequired); err != nil {
		return DomainInfo{}, err
	}
	return info, nil
}

// ChangeNameServers replaces the domain's delegation. It sends ns1..nsN for
// exactly the nameservers given; the API requires at least two and accepts at
// most thirteen, and the provider validates that before calling.
func (c *Client) ChangeNameServers(ctx context.Context, domain string, nameservers []string) error {
	params := make(map[string]string, len(nameservers)+1)
	params["domain"] = domain
	for i, ns := range nameservers {
		params[fmt.Sprintf("ns%d", i+1)] = ns
	}
	return c.call(ctx, "changeNameServers", params, nil)
}

// listDomainsReply is the whole listDomains reply body.
type listDomainsReply struct {
	Reply struct {
		replyHeader
		Domains struct {
			Domain []domainXML `xml:"domain"`
		} `xml:"domains"`
		Pager struct {
			Total    string `xml:"total"`
			PageSize string `xml:"pageSize"`
			Page     string `xml:"page"`
		} `xml:"pager"`
	} `xml:"reply"`
}

// domainXML is one listDomains row: chardata is the name and the dates are
// attributes. maxBid is not modeled; unknown attributes are ignored.
type domainXML struct {
	Name    string `xml:",chardata"`
	Created string `xml:"created,attr"`
	Expires string `xml:"expires,attr"`
}

// ListDomains pages through the account's domains and returns them in the
// order the API sent them. It stops when it has seen the API's reported total
// or when a page adds no new names; a repeat means the API is ignoring the
// page parameters, so it exits with Truncated set rather than looping forever.
// An empty account returns an empty non-nil slice and no error.
func (c *Client) ListDomains(ctx context.Context, pageSize int64) (DomainList, error) {
	list := DomainList{Domains: make([]DomainSummary, 0)}
	seen := make(map[string]bool)

	for page := int64(1); ; page++ {
		var reply listDomainsReply
		params := map[string]string{
			"page":     strconv.FormatInt(page, 10),
			"pageSize": strconv.FormatInt(pageSize, 10),
		}
		if err := c.call(ctx, "listDomains", params, &reply); err != nil {
			return DomainList{}, err
		}
		total, err := strconv.ParseInt(strings.TrimSpace(reply.Reply.Pager.Total), 10, 64)
		if err != nil {
			return DomainList{}, fmt.Errorf("listDomains: parsing <total> %q: %w", reply.Reply.Pager.Total, err)
		}
		list.Total = total

		added := 0
		for _, row := range reply.Reply.Domains.Domain {
			if seen[row.Name] {
				continue
			}
			seen[row.Name] = true
			list.Domains = append(list.Domains, DomainSummary{
				Name:    row.Name,
				Created: row.Created,
				Expires: row.Expires,
			})
			added++
		}

		if int64(len(seen)) >= list.Total {
			break
		}
		if added == 0 {
			list.Truncated = true
			break
		}
	}
	return list, nil
}

// DomainLock locks the domain against transfer.
func (c *Client) DomainLock(ctx context.Context, domain string) error {
	return c.call(ctx, "domainLock", map[string]string{"domain": domain}, nil)
}

// DomainUnlock unlocks the domain for transfer.
func (c *Client) DomainUnlock(ctx context.Context, domain string) error {
	return c.call(ctx, "domainUnlock", map[string]string{"domain": domain}, nil)
}

// AddAutoRenew turns auto-renewal on.
func (c *Client) AddAutoRenew(ctx context.Context, domain string) error {
	return c.call(ctx, "addAutoRenewal", map[string]string{"domain": domain}, nil)
}

// RemoveAutoRenew turns auto-renewal off.
func (c *Client) RemoveAutoRenew(ctx context.Context, domain string) error {
	return c.call(ctx, "removeAutoRenewal", map[string]string{"domain": domain}, nil)
}
