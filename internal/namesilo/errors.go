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
var alreadyInState = map[string]string{
	"addAutoRenewal":    "250",
	"removeAutoRenewal": "251",
	"domainLock":        "252",
	"domainUnlock":      "253",
	"addPrivacy":        "255",
	"removePrivacy":     "256",
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
