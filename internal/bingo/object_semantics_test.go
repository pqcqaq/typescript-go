package bingo

import (
	"strings"
	"testing"
)

const (
	typeKeyA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	typeKeyB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	typeKeyC = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
)

func TestObjectSemanticContractCanonicalRoundTrip(t *testing.T) {
	contract := canonicalObjectContract(t, "object-a", []ObjectPropertyContract{{
		Key: "value", Kind: ObjectPropertyData, ReadTypeKey: typeKeyA, Visibility: "public", Readonly: true,
	}})

	encoded, hash, err := CanonicalObjectSemanticContract(contract)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeObjectSemanticContract(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.ContentHash != hash || decoded.TypeKey != contract.TypeKey {
		t.Fatalf("unexpected round trip: %#v", decoded)
	}
}

func TestObjectSemanticContractStrictDecode(t *testing.T) {
	contract := canonicalObjectContract(t, "object-a", nil)
	encoded, _, err := CanonicalObjectSemanticContract(contract)
	if err != nil {
		t.Fatal(err)
	}

	tests := map[string][]byte{
		"unknown field": []byte(strings.Replace(encodedString(encoded), `"contentHash":`, `"unknown":true,"contentHash":`, 1)),
		"old schema":    []byte(strings.Replace(encodedString(encoded), `"schemaVersion":1`, `"schemaVersion":0`, 1)),
		"tampered hash": []byte(strings.Replace(encodedString(encoded), contract.ContentHash, typeKeyB, 1)),
	}
	for name, data := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeObjectSemanticContract(data); err == nil {
				t.Fatal("expected rejection")
			}
		})
	}
}

func TestObjectSemanticContractRejectsMalformedProperties(t *testing.T) {
	tests := map[string][]ObjectPropertyContract{
		"unsorted": {
			{Key: "z", Kind: ObjectPropertyData, ReadTypeKey: typeKeyA, Visibility: "public"},
			{Key: "a", Kind: ObjectPropertyData, ReadTypeKey: typeKeyA, Visibility: "public"},
		},
		"duplicate": {
			{Key: "a", Kind: ObjectPropertyData, ReadTypeKey: typeKeyA, Visibility: "public"},
			{Key: "a", Kind: ObjectPropertyData, ReadTypeKey: typeKeyA, Visibility: "public"},
		},
		"readonly write": {
			{Key: "a", Kind: ObjectPropertyData, ReadTypeKey: typeKeyA, WriteTypeKey: typeKeyA, Readonly: true, Visibility: "public"},
		},
	}
	for name, properties := range tests {
		t.Run(name, func(t *testing.T) {
			contract := baseObjectContract("object-a", properties)
			if _, _, err := CanonicalObjectSemanticContract(contract); err == nil {
				t.Fatal("expected rejection")
			}
		})
	}
}

func TestPlanObjectConversionIdentityAndConflict(t *testing.T) {
	source := canonicalObjectContract(t, "same", nil)
	plan, err := PlanObjectConversion(source, source, staticImplicitRequest())
	if err != nil || plan.Decision != ObjectDecisionIdentity || !plan.PreservesIdentity {
		t.Fatalf("identity plan = %#v, %v", plan, err)
	}

	target := canonicalObjectContract(t, "same", []ObjectPropertyContract{{Key: "x", Kind: ObjectPropertyData, ReadTypeKey: typeKeyA, Visibility: "public"}})
	if _, err := PlanObjectConversion(source, target, staticImplicitRequest()); err == nil {
		t.Fatal("expected conflicting type-key contracts to fail")
	}
}

func TestPlanObjectConversionReadonlyCovariance(t *testing.T) {
	source := canonicalObjectContract(t, "source", []ObjectPropertyContract{{Key: "x", Kind: ObjectPropertyData, ReadTypeKey: typeKeyA, WriteTypeKey: typeKeyA, Visibility: "public"}})
	target := canonicalObjectContract(t, "target", []ObjectPropertyContract{{Key: "x", Kind: ObjectPropertyData, ReadTypeKey: typeKeyB, Readonly: true, Visibility: "public"}})

	request := staticImplicitRequest()
	request.ReadRelations = []ObjectTypeRelation{{SourceTypeKey: typeKeyA, TargetTypeKey: typeKeyB, Reliable: true}}
	plan, err := PlanObjectConversion(source, target, request)
	if err != nil || plan.Decision != ObjectDecisionReadonlyView || !plan.PreservesIdentity || plan.ExposesWrites {
		t.Fatalf("readonly plan = %#v, %v", plan, err)
	}

	request.ReadRelations[0].Reliable = false
	plan, err = PlanObjectConversion(source, target, request)
	if err != nil || plan.Reason != ObjectReasonReadTypeUnproven {
		t.Fatalf("unreliable proof plan = %#v, %v", plan, err)
	}
	request.ReadRelations = nil
	plan, err = PlanObjectConversion(source, target, request)
	if err != nil || plan.Reason != ObjectReasonReadTypeUnproven {
		t.Fatalf("missing proof plan = %#v, %v", plan, err)
	}
}

func TestPlanObjectConversionMutableViewRequiresLayoutProof(t *testing.T) {
	source := canonicalObjectContract(t, "source", []ObjectPropertyContract{{Key: "x", Kind: ObjectPropertyData, ReadTypeKey: typeKeyA, WriteTypeKey: typeKeyA, Visibility: "public"}})
	target := canonicalObjectContract(t, "target", []ObjectPropertyContract{{Key: "x", Kind: ObjectPropertyData, ReadTypeKey: typeKeyA, WriteTypeKey: typeKeyA, Visibility: "public"}})
	plan, err := PlanObjectConversion(source, target, staticImplicitRequest())
	if err != nil || plan.Decision != ObjectDecisionMutableView || !plan.RequiresLayoutProof || plan.Reason != ObjectReasonMutableAliasRequiresLayoutProof {
		t.Fatalf("mutable plan = %#v, %v", plan, err)
	}
}

func TestPlanObjectConversionRejectsWritableMismatch(t *testing.T) {
	tests := []struct {
		name   string
		source ObjectPropertyContract
		target ObjectPropertyContract
		reason string
	}{
		{"covariant write", ObjectPropertyContract{ReadTypeKey: typeKeyA, WriteTypeKey: typeKeyA}, ObjectPropertyContract{ReadTypeKey: typeKeyB, WriteTypeKey: typeKeyB}, ObjectReasonMutableAliasTypeMismatch},
		{"optional write", ObjectPropertyContract{ReadTypeKey: typeKeyA, WriteTypeKey: typeKeyA}, ObjectPropertyContract{ReadTypeKey: typeKeyA, WriteTypeKey: typeKeyA, Optional: true}, ObjectReasonMutableAliasOptionalMismatch},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.source.Key, test.target.Key = "x", "x"
			test.source.Kind, test.target.Kind = ObjectPropertyData, ObjectPropertyData
			test.source.Visibility, test.target.Visibility = "public", "public"
			source := canonicalObjectContract(t, "source", []ObjectPropertyContract{test.source})
			target := canonicalObjectContract(t, "target", []ObjectPropertyContract{test.target})
			request := staticImplicitRequest()
			request.ReadRelations = []ObjectTypeRelation{{SourceTypeKey: typeKeyA, TargetTypeKey: typeKeyB, Reliable: true}}
			plan, err := PlanObjectConversion(source, target, request)
			if err != nil || plan.Reason != test.reason {
				t.Fatalf("plan = %#v, %v", plan, err)
			}
		})
	}
}

func TestPlanObjectConversionPrivateIdentity(t *testing.T) {
	property := func(identity string) ObjectPropertyContract {
		return ObjectPropertyContract{Key: "#x", Kind: ObjectPropertyData, ReadTypeKey: typeKeyA, Readonly: true, Visibility: "private", PrivateIdentity: identity}
	}
	source := canonicalObjectContract(t, "source", []ObjectPropertyContract{property("class-a")})
	target := canonicalObjectContract(t, "target", []ObjectPropertyContract{property("class-b")})
	plan, err := PlanObjectConversion(source, target, staticImplicitRequest())
	if err != nil || plan.Reason != ObjectReasonPrivateIdentityMismatch {
		t.Fatalf("plan = %#v, %v", plan, err)
	}
}

func TestPlanObjectConversionPreservesAccessorKind(t *testing.T) {
	source := canonicalObjectContract(t, "source", []ObjectPropertyContract{{Key: "x", Kind: ObjectPropertyData, ReadTypeKey: typeKeyA, Readonly: true, Visibility: "public"}})
	target := canonicalObjectContract(t, "target", []ObjectPropertyContract{{Key: "x", Kind: ObjectPropertyAccessor, ReadTypeKey: typeKeyA, Readonly: true, Visibility: "public"}})
	plan, err := PlanObjectConversion(source, target, staticImplicitRequest())
	if err != nil || plan.Reason != ObjectReasonPropertyKindMismatch {
		t.Fatalf("plan = %#v, %v", plan, err)
	}
}

func TestPlanObjectConversionExplicitCopy(t *testing.T) {
	source := canonicalObjectContract(t, "source", []ObjectPropertyContract{{Key: "x", Kind: ObjectPropertyData, ReadTypeKey: typeKeyA, Visibility: "public", Readonly: true}})
	target := canonicalObjectContract(t, "target", []ObjectPropertyContract{{Key: "x", Kind: ObjectPropertyData, ReadTypeKey: typeKeyA, Visibility: "public", Readonly: true}})
	request := staticImplicitRequest()
	request.Mode = ObjectConversionExplicitCopy
	plan, err := PlanObjectConversion(source, target, request)
	if err != nil || plan.Decision != ObjectDecisionCopyNewIdentity || plan.PreservesIdentity {
		t.Fatalf("copy plan = %#v, %v", plan, err)
	}
}

func TestPlanObjectConversionDynamicBoundary(t *testing.T) {
	contract := canonicalObjectContract(t, "object", nil)
	request := ObjectConversionRequest{Mode: ObjectConversionDynamicBoundary, Profile: ObjectProfileStatic}
	plan, err := PlanObjectConversion(contract, contract, request)
	if err != nil || plan.Reason != ObjectReasonDynamicBoundaryStaticProfile {
		t.Fatalf("static dynamic plan = %#v, %v", plan, err)
	}
	request.Profile = ObjectProfileDynamic
	plan, err = PlanObjectConversion(contract, contract, request)
	if err != nil || plan.Decision != ObjectDecisionDynamicBoundary || !plan.PreservesIdentity {
		t.Fatalf("dynamic plan = %#v, %v", plan, err)
	}
}

func TestObjectEscapeLattice(t *testing.T) {
	joined, err := JoinObjectEscape(ObjectEscapeCaller, ObjectEscapeHeap)
	if err != nil || joined != ObjectEscapeHeap {
		t.Fatalf("join = %q, %v", joined, err)
	}
	joined, err = JoinObjectEscape(ObjectEscapeDynamic, ObjectEscapeLocal)
	if err != nil || joined != ObjectEscapeDynamic {
		t.Fatalf("join = %q, %v", joined, err)
	}
	if err := VerifyObjectEscapeTransition(ObjectEscapeHeap, ObjectEscapeCaller); err == nil || !strings.Contains(err.Error(), ObjectReasonEscapeDowngrade) {
		t.Fatalf("expected downgrade rejection, got %v", err)
	}
	if err := VerifyObjectEscapeTransition(ObjectEscapeCaller, ObjectEscapeHeap); err != nil {
		t.Fatal(err)
	}
}

func baseObjectContract(name string, properties []ObjectPropertyContract) ObjectSemanticContract {
	key := typeKeyA
	if name != "object-a" {
		key = typeKeyForName(name)
	}
	return ObjectSemanticContract{SchemaVersion: ObjectSemanticContractSchemaVersion, TypeKey: key, Identity: ObjectIdentityReference, Equality: ObjectEqualityReference, Properties: properties}
}

func canonicalObjectContract(t *testing.T, name string, properties []ObjectPropertyContract) ObjectSemanticContract {
	t.Helper()
	contract := baseObjectContract(name, properties)
	_, hash, err := CanonicalObjectSemanticContract(contract)
	if err != nil {
		t.Fatal(err)
	}
	contract.ContentHash = hash
	return contract
}

func staticImplicitRequest() ObjectConversionRequest {
	return ObjectConversionRequest{Mode: ObjectConversionImplicit, Profile: ObjectProfileStatic}
}

func typeKeyForName(name string) string {
	switch name {
	case "source":
		return typeKeyB
	case "target":
		return typeKeyC
	case "same":
		return typeKeyA
	default:
		return typeKeyC
	}
}

func encodedString(data []byte) string { return string(data) }
