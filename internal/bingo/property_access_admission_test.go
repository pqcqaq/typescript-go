package bingo

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPropertyAccessAdmissionClassifiesKeyDomains(t *testing.T) {
	key := strings.Repeat("a", 64)
	tests := []struct {
		domain                   PropertyKeyDomain
		keys                     []string
		profile                  PropertyAccessProfile
		source, decision, reason string
	}{
		{PropertyKeyDirect, []string{"value"}, PropertyAccessStatic, "", string(PropertyAccessPlaceRef), ""},
		{PropertyKeyLiteral, []string{"value"}, PropertyAccessStatic, "", string(PropertyAccessPlaceRef), ""},
		{PropertyKeyLiteralUnion, []string{"left", "right"}, PropertyAccessStatic, "", string(PropertyAccessFiniteDispatch), ""},
		{PropertyKeyUnknown, nil, PropertyAccessStatic, "", string(PropertyAccessReject), PropertyAccessReasonUnknownKeyStatic},
		{PropertyKeyUnknown, nil, PropertyAccessInterop, "node/access", string(PropertyAccessDynamicBoundary), ""},
	}
	for _, test := range tests {
		admission, err := BuildPropertyAccessAdmission(key, test.domain, test.keys, test.profile, test.source)
		if err != nil {
			t.Fatal(err)
		}
		if string(admission.Decision) != test.decision || admission.Reason != test.reason {
			t.Fatalf("unexpected admission: %#v", admission)
		}
		encoded, _, err := CanonicalPropertyAccessAdmission(admission)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := DecodePropertyAccessAdmission(encoded); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPropertyAccessAdmissionRejectsTampering(t *testing.T) {
	base, err := BuildPropertyAccessAdmission(strings.Repeat("a", 64), PropertyKeyUnknown, nil, PropertyAccessInterop, "node/access")
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*PropertyAccessAdmission){
		"decision":        func(v *PropertyAccessAdmission) { v.Decision = PropertyAccessPlaceRef },
		"effects":         func(v *PropertyAccessAdmission) { v.Effects = []Effect{EffectRead} },
		"profile":         func(v *PropertyAccessAdmission) { v.Profile = PropertyAccessStatic },
		"keys":            func(v *PropertyAccessAdmission) { v.Keys = []string{"value"} },
		"boundary kind":   func(v *PropertyAccessAdmission) { v.Boundary.Kind = "ffi-import" },
		"boundary source": func(v *PropertyAccessAdmission) { v.Boundary.SourceID = "node/other" },
	} {
		t.Run(name, func(t *testing.T) {
			value := base
			if base.Boundary != nil {
				copy := *base.Boundary
				value.Boundary = &copy
			}
			mutate(&value)
			if err := VerifyCanonicalPropertyAccessAdmission(value); err == nil {
				t.Fatal("accepted tampering")
			}
		})
	}
	encoded, _, err := CanonicalPropertyAccessAdmission(base)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(encoded, &raw); err != nil {
		t.Fatal(err)
	}
	raw["unknown"] = true
	unknown, _ := json.Marshal(raw)
	if _, err := DecodePropertyAccessAdmission(unknown); err == nil {
		t.Fatal("accepted unknown member")
	}
	if _, err := DecodePropertyAccessAdmission(make([]byte, maxPropertyAccessAdmissionBytes+1)); err == nil {
		t.Fatal("accepted oversized artifact")
	}
}

func FuzzDecodePropertyAccessAdmission(f *testing.F) {
	value, err := BuildPropertyAccessAdmission(strings.Repeat("a", 64), PropertyKeyUnknown, nil, PropertyAccessInterop, "node/access")
	if err != nil {
		f.Fatal(err)
	}
	encoded, _, err := CanonicalPropertyAccessAdmission(value)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(encoded)
	f.Fuzz(func(t *testing.T, data []byte) { _, _ = DecodePropertyAccessAdmission(data) })
}
