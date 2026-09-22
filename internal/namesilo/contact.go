// SPDX-License-Identifier: GPL-3.0-or-later

package namesilo

import (
	"context"
	"fmt"
)

// Contact mirrors the API's contact payload. An empty string field means the
// API sent an empty element (or the field was never set); the provider layer
// maps empty strings to null, so the client returns what the API said.
type Contact struct {
	ID             string
	DefaultProfile bool
	Nickname       string // nn
	Company        string // cp
	FirstName      string // fn
	LastName       string // ln
	Address        string // ad
	Address2       string // ad2
	City           string // cy
	State          string // st
	Zip            string // zp
	Country        string // ct
	Email          string // em
	Phone          string // ph
	Fax            string // fx

	UsNexusCategory      string // usnc
	UsApplicationPurpose string // usap
	CaLegalForm          string // calf
	CaLanguage           string // caln
	CaAgreementVersion   string // caag
	CaWhoisDisplay       string // cawd
	EuCitizenshipCountry string // eucs
}

// contactXML is one <contact> element in a contactList reply. The reply uses
// the full snake_case element names, not the short forms the request carries:
// nickname, company, first_name, and so on, while the request parameters are
// nn, cp, fn. The TLD-specific elements (us_nexus_category, and the rest)
// follow the same reply convention but were not exercised by the live probe,
// because its profiles set none of them. Unset fields arrive as empty elements
// and decode to empty strings (§8.4).
type contactXML struct {
	ContactID      string `xml:"contact_id"`
	DefaultProfile string `xml:"default_profile"`

	Nickname             string `xml:"nickname"`
	Company              string `xml:"company"`
	FirstName            string `xml:"first_name"`
	LastName             string `xml:"last_name"`
	Address              string `xml:"address"`
	Address2             string `xml:"address2"`
	City                 string `xml:"city"`
	State                string `xml:"state"`
	Zip                  string `xml:"zip"`
	Country              string `xml:"country"`
	Email                string `xml:"email"`
	Phone                string `xml:"phone"`
	Fax                  string `xml:"fax"`
	UsNexusCategory      string `xml:"us_nexus_category"`
	UsApplicationPurpose string `xml:"us_application_purpose"`
	CaLegalForm          string `xml:"ca_legal_form"`
	CaLanguage           string `xml:"ca_language"`
	CaAgreementVersion   string `xml:"ca_agreement_version"`
	CaWhoisDisplay       string `xml:"ca_whois_display"`
	EuCitizenshipCountry string `xml:"eu_citizenship_country"`
}

// parseDefaultProfile reads the API's default_profile element. Only 1 and 0
// are accepted; anything else is an error naming the operation and the field,
// because guessing would silently re-point the account's default profile
// (§8.4).
func parseDefaultProfile(operation, value string) (bool, error) {
	switch value {
	case "1":
		return true, nil
	case "0":
		return false, nil
	default:
		return false, fmt.Errorf("%s: cannot interpret <default_profile> value %q as 1 or 0", operation, value)
	}
}

// contact converts one reply element to a Contact, interpreting the strict
// default_profile element on the way.
func (x contactXML) contact(operation string) (Contact, error) {
	defaultProfile, err := parseDefaultProfile(operation, x.DefaultProfile)
	if err != nil {
		return Contact{}, err
	}
	return Contact{
		ID:                   x.ContactID,
		DefaultProfile:       defaultProfile,
		Nickname:             x.Nickname,
		Company:              x.Company,
		FirstName:            x.FirstName,
		LastName:             x.LastName,
		Address:              x.Address,
		Address2:             x.Address2,
		City:                 x.City,
		State:                x.State,
		Zip:                  x.Zip,
		Country:              x.Country,
		Email:                x.Email,
		Phone:                x.Phone,
		Fax:                  x.Fax,
		UsNexusCategory:      x.UsNexusCategory,
		UsApplicationPurpose: x.UsApplicationPurpose,
		CaLegalForm:          x.CaLegalForm,
		CaLanguage:           x.CaLanguage,
		CaAgreementVersion:   x.CaAgreementVersion,
		CaWhoisDisplay:       x.CaWhoisDisplay,
		EuCitizenshipCountry: x.EuCitizenshipCountry,
	}, nil
}

// contactParams maps a Contact onto the operation's query parameters, under
// the API's short names. Every field is sent, empty values included, so the
// request is deterministic (§8.1). withID adds contact_id, which contactUpdate
// carries and contactAdd does not.
func (contact Contact) contactParams(withID bool) map[string]string {
	params := map[string]string{
		"nn":   contact.Nickname,
		"cp":   contact.Company,
		"fn":   contact.FirstName,
		"ln":   contact.LastName,
		"ad":   contact.Address,
		"ad2":  contact.Address2,
		"cy":   contact.City,
		"st":   contact.State,
		"zp":   contact.Zip,
		"ct":   contact.Country,
		"em":   contact.Email,
		"ph":   contact.Phone,
		"fx":   contact.Fax,
		"usnc": contact.UsNexusCategory,
		"usap": contact.UsApplicationPurpose,
		"calf": contact.CaLegalForm,
		"caln": contact.CaLanguage,
		"caag": contact.CaAgreementVersion,
		"cawd": contact.CaWhoisDisplay,
		"eucs": contact.EuCitizenshipCountry,
	}
	if withID {
		params["contact_id"] = contact.ID
	}
	return params
}

// contactListReply is the whole contactList reply body. Contact is a slice, so
// a single <contact> element in the reply is a one-element result, not a
// scalar (§8.4).
type contactListReply struct {
	Reply struct {
		replyHeader
		Contact []contactXML `xml:"contact"`
	} `xml:"reply"`
}

// ListContacts reads the account's contact profiles. An empty contactID asks
// for every profile; a non-empty one asks for that profile (or none). The
// contact_id parameter is always sent, even empty (§8.1).
func (c *Client) ListContacts(ctx context.Context, contactID string) ([]Contact, error) {
	var reply contactListReply
	if err := c.call(ctx, "contactList", map[string]string{"contact_id": contactID}, &reply); err != nil {
		return nil, err
	}
	contacts := make([]Contact, 0, len(reply.Reply.Contact))
	for _, x := range reply.Reply.Contact {
		contact, err := x.contact("contactList")
		if err != nil {
			return nil, err
		}
		contacts = append(contacts, contact)
	}
	return contacts, nil
}

// contactAddReply is the whole contactAdd reply body, which carries the new
// profile's contact_id.
type contactAddReply struct {
	Reply struct {
		replyHeader
		ContactID string `xml:"contact_id"`
	} `xml:"reply"`
}

// AddContact creates a profile and returns the API's new contact_id. Every
// field is sent, empty values included (§8.1). A success reply without a
// <contact_id> element is an error rather than an empty id in state.
func (c *Client) AddContact(ctx context.Context, contact Contact) (string, error) {
	var reply contactAddReply
	if err := c.call(ctx, "contactAdd", contact.contactParams(false), &reply); err != nil {
		return "", err
	}
	if reply.Reply.ContactID == "" {
		return "", fmt.Errorf("contactAdd: the reply has no <contact_id> element")
	}
	return reply.Reply.ContactID, nil
}

// UpdateContact replaces a profile's fields. It sends contact_id plus every
// field (§8.3).
func (c *Client) UpdateContact(ctx context.Context, contact Contact) error {
	return c.call(ctx, "contactUpdate", contact.contactParams(true), nil)
}

// DeleteContact removes a profile. It sends only contact_id.
func (c *Client) DeleteContact(ctx context.Context, contactID string) error {
	return c.call(ctx, "contactDelete", map[string]string{"contact_id": contactID}, nil)
}

// AssociateContacts points a domain's roles at contact profiles. Only the
// non-empty roles are sent (§8.3): an empty role means "not managed", and the
// API has no way to clear a role, so an absent parameter is the only way to
// leave an association alone.
func (c *Client) AssociateContacts(ctx context.Context, domain string, roles ContactRoles) error {
	params := map[string]string{"domain": domain}
	if roles.Registrant != "" {
		params["registrant"] = roles.Registrant
	}
	if roles.Administrative != "" {
		params["administrative"] = roles.Administrative
	}
	if roles.Technical != "" {
		params["technical"] = roles.Technical
	}
	if roles.Billing != "" {
		params["billing"] = roles.Billing
	}
	return c.call(ctx, "contactDomainAssociate", params, nil)
}
