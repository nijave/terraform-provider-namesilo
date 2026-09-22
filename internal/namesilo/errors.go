// SPDX-License-Identifier: GPL-3.0-or-later

package namesilo

import "fmt"

// successCodes are the reply codes that are success for every operation.
var successCodes = map[string]bool{
	"300": true,
	"301": true,
	"302": true,
}

// alreadyInState maps an operation to the reply code that means the requested
// state already holds. The code is success for that operation only: a 250 from
// any other operation is an error, because the classification is per operation.
//
// dnsSecDeleteRecord's 210 is a little different from the toggle codes above. A
// DS record that was added moments ago and has not activated yet cannot be
// deleted: the API answers 210 "There are no active records specified for
// deletion" -- and the delete takes effect anyway (a live probe added two
// records back-to-back and deleted one immediately: 210, but the record never
// appeared in dnsSecListRecords again, and deleting the other once active was a
// clean 300). The reconcile deletes records moments after creating them, so
// this code is routine, not an error. If a record ever lingers despite the 210,
// the next refresh re-surfaces it and a later apply deletes it once active, so
// tolerating the code is self-healing. Delete parameters always come from state
// parsed out of the API's own list, so a genuine parameter-mismatch 210 (the
// other meaning of the code) is unreachable in practice.
var alreadyInState = map[string]string{
	"addAutoRenewal":     "250",
	"removeAutoRenewal":  "251",
	"domainLock":         "252",
	"domainUnlock":       "253",
	"addPrivacy":         "255",
	"removePrivacy":      "256",
	"dnsSecDeleteRecord": "210",
}

// APIError is a NameSilo reply whose code is not success. It carries the
// operation, the server's code, and the server's detail text. The request URL
// and its query (which contains the API key) are deliberately absent, so an
// error can never leak the key.
type APIError struct {
	Operation string
	Code      string
	Detail    string
}

// Error formats the error as "NameSilo API error (operation): detail (code)".
func (e *APIError) Error() string {
	return fmt.Sprintf("NameSilo API error (%s): %s (code %s)", e.Operation, e.Detail, e.Code)
}
