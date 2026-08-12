package bingo

import (
	"strings"
	"testing"
)

func testClassAccessContract() ClassAccessContract {
	return ClassAccessContract{
		SchemaVersion: ClassAccessContractSchemaVersion,
		Classes: []ClassAccessClass{
			{ID: 1, SymbolKey: "class/Base", InstanceTypeKey: typeKeyA},
			{ID: 2, SymbolKey: "class/Derived", InstanceTypeKey: typeKeyB, BaseClassID: 1},
			{ID: 3, SymbolKey: "class/Leaf", InstanceTypeKey: typeKeyC, BaseClassID: 2},
			{ID: 4, SymbolKey: "class/Other", InstanceTypeKey: "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"},
		},
		Members: []ClassAccessMember{
			{ID: 1, OwnerClassID: 1, SymbolKey: "field/private", Name: "privateValue", Kind: ClassAccessField, Visibility: ClassMemberPrivate, PrivateIdentity: "private/Base/privateValue"},
			{ID: 2, OwnerClassID: 1, SymbolKey: "field/protected", Name: "protectedValue", Kind: ClassAccessField, Visibility: ClassMemberProtected},
			{ID: 3, OwnerClassID: 1, SymbolKey: "field/public", Name: "publicValue", Kind: ClassAccessMethod, Visibility: ClassMemberPublic},
			{ID: 4, OwnerClassID: 2, SymbolKey: "method/derived", Name: "readValue", Kind: ClassAccessMethod, Visibility: ClassMemberPublic},
		},
	}
}

func TestClassAccessContractCanonicalRoundTrip(t *testing.T) {
	contract := testClassAccessContract()
	encoded, hash, err := CanonicalClassAccessContract(contract)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeClassAccessContract(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.ContentHash != hash || len(decoded.Classes) != 4 || len(decoded.Members) != 4 {
		t.Fatalf("unexpected class access round trip: %#v", decoded)
	}
	if _, err := DecodeVERT013bClassContract(encoded); err == nil {
		t.Fatal("VERT-013b reader accepted class access contract")
	}
}

func TestClassAccessContractStrictDecodeAndTamperRejection(t *testing.T) {
	contract := testClassAccessContract()
	encoded, hash, err := CanonicalClassAccessContract(contract)
	if err != nil {
		t.Fatal(err)
	}
	tests := map[string][]byte{
		"unknown member": []byte(strings.Replace(string(encoded), `"contentHash":`, `"unknown":true,"contentHash":`, 1)),
		"old schema":     []byte(strings.Replace(string(encoded), `"schemaVersion":1`, `"schemaVersion":0`, 1)),
		"stale hash":     []byte(strings.Replace(string(encoded), hash, typeKeyA, 1)),
	}
	for name, data := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeClassAccessContract(data); err == nil {
				t.Fatal("tampered class access contract accepted")
			}
		})
	}

	mutations := map[string]func(*ClassAccessContract){
		"nondense class":      func(c *ClassAccessContract) { c.Classes[1].ID = 4 },
		"forward base":        func(c *ClassAccessContract) { c.Classes[1].BaseClassID = 3 },
		"duplicate symbol":    func(c *ClassAccessContract) { c.Classes[1].SymbolKey = c.Classes[0].SymbolKey },
		"invalid type key":    func(c *ClassAccessContract) { c.Classes[0].InstanceTypeKey = "invalid" },
		"aliased base type":   func(c *ClassAccessContract) { c.Classes[1].InstanceTypeKey = c.Classes[0].InstanceTypeKey },
		"missing owner":       func(c *ClassAccessContract) { c.Members[0].OwnerClassID = 9 },
		"unknown member kind": func(c *ClassAccessContract) { c.Members[0].Kind = "property" },
		"duplicate member":    func(c *ClassAccessContract) { c.Members[1].SymbolKey = c.Members[0].SymbolKey },
		"private no identity": func(c *ClassAccessContract) { c.Members[0].PrivateIdentity = "" },
		"protected identity":  func(c *ClassAccessContract) { c.Members[1].PrivateIdentity = "forged" },
		"duplicate identity": func(c *ClassAccessContract) {
			c.Members[1].Visibility = ClassMemberPrivate
			c.Members[1].PrivateIdentity = c.Members[0].PrivateIdentity
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			candidate := testClassAccessContract()
			mutate(&candidate)
			if _, _, err := CanonicalClassAccessContract(candidate); err == nil {
				t.Fatal("malformed class access contract accepted")
			}
		})
	}
}

func TestPlanClassMemberAccess(t *testing.T) {
	contract := testClassAccessContract()
	tests := []struct {
		name    string
		request ClassAccessRequest
		allowed bool
		reason  string
	}{
		{"external public", ClassAccessRequest{ReceiverClassID: 2, MemberID: 3}, true, ClassAccessAllowed},
		{"base protected on derived", ClassAccessRequest{AccessingClassID: 1, ReceiverClassID: 2, MemberID: 2}, true, ClassAccessAllowed},
		{"derived protected on self", ClassAccessRequest{AccessingClassID: 2, ReceiverClassID: 2, MemberID: 2}, true, ClassAccessAllowed},
		{"derived protected on leaf", ClassAccessRequest{AccessingClassID: 2, ReceiverClassID: 3, MemberID: 2}, true, ClassAccessAllowed},
		{"external protected", ClassAccessRequest{ReceiverClassID: 2, MemberID: 2}, false, ClassAccessProtectedOutsideFamily},
		{"unrelated protected", ClassAccessRequest{AccessingClassID: 4, ReceiverClassID: 2, MemberID: 2}, false, ClassAccessProtectedOutsideFamily},
		{"derived through base receiver", ClassAccessRequest{AccessingClassID: 2, ReceiverClassID: 1, MemberID: 2}, false, ClassAccessProtectedReceiverIncompatible},
		{"owner private on derived", ClassAccessRequest{AccessingClassID: 1, ReceiverClassID: 2, MemberID: 1, PrivateIdentity: "private/Base/privateValue"}, true, ClassAccessAllowed},
		{"derived private", ClassAccessRequest{AccessingClassID: 2, ReceiverClassID: 2, MemberID: 1, PrivateIdentity: "private/Base/privateValue"}, false, ClassAccessPrivateOutsideOwner},
		{"forged private", ClassAccessRequest{AccessingClassID: 1, ReceiverClassID: 1, MemberID: 1, PrivateIdentity: "private/Other/privateValue"}, false, ClassAccessPrivateIdentityMismatch},
		{"wrong receiver family", ClassAccessRequest{ReceiverClassID: 4, MemberID: 3}, false, ClassAccessReceiverOutsideOwnerFamily},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			decision, err := PlanClassMemberAccess(contract, test.request)
			if err != nil {
				t.Fatal(err)
			}
			if decision.Allowed != test.allowed || decision.Reason != test.reason {
				t.Fatalf("decision = %#v, want allowed=%v reason=%q", decision, test.allowed, test.reason)
			}
		})
	}
}
