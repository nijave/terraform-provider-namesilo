// SPDX-License-Identifier: GPL-3.0-or-later

package provider

import (
	"reflect"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/nijave/terraform-provider-namesilo/internal/namesilo"
)

// fullContactForMapping returns a namesilo.Contact with every field set to a
// distinct non-zero value, so a field the mapping drops leaves a null or a zero
// the test can see.
func fullContactForMapping() namesilo.Contact {
	return namesilo.Contact{
		ID:                   "1001",
		DefaultProfile:       true,
		Nickname:             "nn",
		Company:              "cp",
		FirstName:            "fn",
		LastName:             "ln",
		Address:              "ad",
		Address2:             "ad2",
		City:                 "cy",
		State:                "st",
		Zip:                  "zp",
		Country:              "ct",
		Email:                "em",
		Phone:                "ph",
		Fax:                  "fx",
		UsNexusCategory:      "usnc",
		UsApplicationPurpose: "usap",
		CaLegalForm:          "calf",
		CaLanguage:           "caln",
		CaAgreementVersion:   "caag",
		CaWhoisDisplay:       "cawd",
		EuCitizenshipCountry: "eucs",
	}
}

// TestContactToModelMapsEveryContactField is the completeness guard for the
// shared contact mapping. It reflects over namesilo.Contact and asserts the
// resource's state model carries the same value for every field. A field added
// to the client's Contact but not to contactFieldsFromContact leaves a null
// (string) or a false (bool) in the model and fails here, so the mapping cannot
// silently fall behind the API payload.
func TestContactToModelMapsEveryContactField(t *testing.T) {
	contact := fullContactForMapping()
	model := contactToModel(contact)
	modelValue := reflect.ValueOf(model)
	contactValue := reflect.ValueOf(contact)
	contactType := contactValue.Type()

	for i := 0; i < contactType.NumField(); i++ {
		field := contactType.Field(i)
		mapped := modelValue.FieldByName(field.Name)
		if !mapped.IsValid() {
			t.Errorf("contactToModel does not map namesilo.Contact.%s", field.Name)
			continue
		}
		switch field.Type.Kind() {
		case reflect.String:
			got, ok := mapped.Interface().(types.String)
			if !ok {
				t.Errorf("namesilo.Contact.%s maps to %T, want types.String", field.Name, mapped.Interface())
				continue
			}
			want := contactValue.Field(i).String()
			if got.IsNull() || got.ValueString() != want {
				t.Errorf("namesilo.Contact.%s = %q, model holds %v", field.Name, want, got)
			}
		case reflect.Bool:
			got, ok := mapped.Interface().(types.Bool)
			if !ok {
				t.Errorf("namesilo.Contact.%s maps to %T, want types.Bool", field.Name, mapped.Interface())
				continue
			}
			want := contactValue.Field(i).Bool()
			if got.IsNull() || got.ValueBool() != want {
				t.Errorf("namesilo.Contact.%s = %v, model holds %v", field.Name, want, got)
			}
		default:
			t.Errorf("namesilo.Contact.%s has unsupported kind %s; extend this test to cover it",
				field.Name, field.Type.Kind())
		}
	}
}
