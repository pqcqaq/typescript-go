package bingo

import (
	"bytes"
	"strings"
	"testing"
)

func testPlaceRefContract() PlaceRefContract {
	origin := Origin{File: "/project/property-place.ts", Start: 1, End: 2}
	object := baseObjectContract("object-a", []ObjectPropertyContract{
		{Key: "accessor", Kind: ObjectPropertyAccessor, ReadTypeKey: typeKeyB, WriteTypeKey: typeKeyC, Visibility: "public"},
		{Key: "value", Kind: ObjectPropertyData, ReadTypeKey: typeKeyB, WriteTypeKey: typeKeyB, Visibility: "public"},
	})
	_, objectHash, err := CanonicalObjectSemanticContract(object)
	if err != nil {
		panic(err)
	}
	object.ContentHash = objectHash
	return PlaceRefContract{
		SchemaVersion:   PlaceRefSchemaVersion,
		ObjectContracts: []ObjectSemanticContract{object},
		EvaluationOrder: []ValueID{1, 2, 3},
		Places: []PropertyPlaceRef{
			{ID: 1, Receiver: 1, AccessSyntax: PlaceAccessDirect, AccessPlan: PlaceAccessStaticData, ObjectTypeKey: typeKeyA, PropertyKey: "value", PropertySymbolKey: "symbol/value", ReadTypeKey: typeKeyB, WriteTypeKey: typeKeyB, ReadType: TypeNumber, WriteType: TypeNumber, Mutability: PlaceMutable, Required: true, LoadEffects: []Effect{EffectRead}, StoreEffects: []Effect{EffectWrite}, Origin: origin},
			{ID: 2, Receiver: 2, Key: 3, AccessSyntax: PlaceAccessComputed, AccessPlan: PlaceAccessAccessor, ObjectTypeKey: typeKeyA, PropertyKey: "accessor", PropertySymbolKey: "symbol/accessor", ReadTypeKey: typeKeyB, WriteTypeKey: typeKeyC, ReadType: TypeNullableNumber, WriteType: TypeNumber, Mutability: PlaceMutable, Required: true, GetterSymbolKey: "symbol/get", SetterSymbolKey: "symbol/set", BackingPropertyKey: "value", BackingPropertySymbolKey: "symbol/value", LoadEffects: []Effect{EffectCall, EffectRead, EffectThrow}, StoreEffects: []Effect{EffectCall, EffectThrow, EffectWrite}, Origin: origin},
		},
	}
}

func TestPlaceRefContractCanonicalRoundTrip(t *testing.T) {
	contract := testPlaceRefContract()
	encoded, hash, err := CanonicalPlaceRefContract(contract)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodePlaceRefContract(encoded)
	if err != nil {
		t.Fatal(err)
	}
	reencoded, decodedHash, err := CanonicalPlaceRefContract(*decoded)
	if err != nil || hash != decodedHash || !bytes.Equal(encoded, reencoded) {
		t.Fatalf("PlaceRef canonical round trip failed: %v", err)
	}
}

func TestPlaceRefContractRejectsTampering(t *testing.T) {
	tests := []struct {
		name string
		edit func(*PlaceRefContract)
	}{
		{"old schema", func(value *PlaceRefContract) { value.SchemaVersion = 0 }},
		{"unknown schema", func(value *PlaceRefContract) { value.SchemaVersion++ }},
		{"non-dense ID", func(value *PlaceRefContract) { value.Places[1].ID = 3 }},
		{"evaluation order", func(value *PlaceRefContract) {
			value.EvaluationOrder[1], value.EvaluationOrder[2] = value.EvaluationOrder[2], value.EvaluationOrder[1]
		}},
		{"direct key", func(value *PlaceRefContract) { value.Places[0].Key = 4 }},
		{"missing computed key", func(value *PlaceRefContract) { value.Places[1].Key = 0 }},
		{"property identity", func(value *PlaceRefContract) { value.Places[0].PropertySymbolKey = "" }},
		{"property membership", func(value *PlaceRefContract) { value.Places[0].PropertyKey = "missing" }},
		{"object contract hash", func(value *PlaceRefContract) { value.ObjectContracts[0].ContentHash = typeKeyC }},
		{"object contract order", func(value *PlaceRefContract) {
			other := value.ObjectContracts[0]
			other.TypeKey = strings.Repeat("0", 64)
			other.ContentHash = ""
			_, hash, err := CanonicalObjectSemanticContract(other)
			if err != nil {
				panic(err)
			}
			other.ContentHash = hash
			value.ObjectContracts = append(value.ObjectContracts, other)
		}},
		{"read type", func(value *PlaceRefContract) { value.Places[0].ReadTypeKey = "bad" }},
		{"static type mismatch", func(value *PlaceRefContract) { value.Places[0].WriteType = TypeBoolean }},
		{"readonly store", func(value *PlaceRefContract) { value.Places[0].Mutability = PlaceReadonly }},
		{"pure getter", func(value *PlaceRefContract) { value.Places[1].LoadEffects = []Effect{EffectPure} }},
		{"missing setter", func(value *PlaceRefContract) { value.Places[1].SetterSymbolKey = "" }},
		{"missing accessor backing", func(value *PlaceRefContract) { value.Places[1].BackingPropertyKey = "" }},
		{"physical offset leakage", func(value *PlaceRefContract) { value.Places[0].AccessPlan = PlaceAccessPlan("offset:24") }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			contract := testPlaceRefContract()
			test.edit(&contract)
			if _, _, err := CanonicalPlaceRefContract(contract); err == nil {
				t.Fatal("tampered PlaceRef contract was accepted")
			}
		})
	}
}

func TestPlaceRefContractAllowsSavedReceiverReuse(t *testing.T) {
	contract := testPlaceRefContract()
	contract.Places[1].Receiver = contract.Places[0].Receiver
	contract.EvaluationOrder = []ValueID{1, 1, 3}
	if _, _, err := CanonicalPlaceRefContract(contract); err != nil {
		t.Fatalf("saved SSA receiver reuse was rejected: %v", err)
	}
}

func TestPlaceRefContractStrictDecoder(t *testing.T) {
	encoded, _, err := CanonicalPlaceRefContract(testPlaceRefContract())
	if err != nil {
		t.Fatal(err)
	}
	unknown := bytes.Replace(encoded, []byte(`"schemaVersion":1`), []byte(`"schemaVersion":1,"unknown":true`), 1)
	if _, err := DecodePlaceRefContract(unknown); err == nil {
		t.Fatal("unknown PlaceRef member was accepted")
	}
	tampered := bytes.Replace(encoded, []byte(typeKeyA), []byte(typeKeyC), 1)
	if _, err := DecodePlaceRefContract(tampered); err == nil || !strings.Contains(err.Error(), "hash mismatch") {
		t.Fatalf("stale PlaceRef hash error = %v", err)
	}
	if _, err := DecodePlaceRefContract(make([]byte, maxPlaceRefContractBytes+1)); err == nil {
		t.Fatal("oversized PlaceRef contract was accepted")
	}
}
