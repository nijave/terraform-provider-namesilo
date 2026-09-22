// SPDX-License-Identifier: GPL-3.0-or-later

package namesilo

import "context"

// AddPrivacy turns WHOIS privacy on for the domain. The reply code 255 means
// the domain is already private, which call treats as success for this
// operation only.
func (c *Client) AddPrivacy(ctx context.Context, domain string) error {
	return c.call(ctx, "addPrivacy", map[string]string{"domain": domain}, nil)
}

// RemovePrivacy turns WHOIS privacy off for the domain. The reply code 256
// means the domain is already not private, which call treats as success for
// this operation only.
func (c *Client) RemovePrivacy(ctx context.Context, domain string) error {
	return c.call(ctx, "removePrivacy", map[string]string{"domain": domain}, nil)
}
