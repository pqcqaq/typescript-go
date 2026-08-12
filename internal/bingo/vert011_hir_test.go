package bingo

import (
	"bytes"
	"strings"
	"testing"
)

func testVERT011HIR(t testing.TB) HIRModule {
	t.Helper()
	origin := Origin{File: "/project/property-nullish-assign.ts", Start: 1, End: 2}
	object := ObjectSemanticContract{
		SchemaVersion: ObjectSemanticContractSchemaVersion,
		TypeKey:       typeKeyA,
		Identity:      ObjectIdentityReference,
		Equality:      ObjectEqualityReference,
		Properties: []ObjectPropertyContract{
			{Key: "backing", Kind: ObjectPropertyData, ReadTypeKey: typeKeyB, WriteTypeKey: typeKeyB, Visibility: "public"},
			{Key: "result", Kind: ObjectPropertyAccessor, ReadTypeKey: typeKeyB, WriteTypeKey: typeKeyC, Visibility: "public"},
		},
	}
	_, objectHash, err := CanonicalObjectSemanticContract(object)
	if err != nil {
		t.Fatal(err)
	}
	object.ContentHash = objectHash
	places := PlaceRefContract{
		SchemaVersion:   PlaceRefSchemaVersion,
		ObjectContracts: []ObjectSemanticContract{object},
		EvaluationOrder: []ValueID{3, 4},
		Places: []PropertyPlaceRef{{
			ID: 1, Receiver: 3, Key: 4, AccessSyntax: PlaceAccessComputed, AccessPlan: PlaceAccessAccessor,
			ObjectTypeKey: typeKeyA, PropertyKey: "result", PropertySymbolKey: "symbol/result",
			ReadTypeKey: typeKeyB, WriteTypeKey: typeKeyC, ReadType: TypeNullableNumber, WriteType: TypeNumber,
			Mutability: PlaceMutable, Required: true, GetterSymbolKey: "symbol/get", SetterSymbolKey: "symbol/set",
			BackingPropertyKey: "backing", BackingPropertySymbolKey: "symbol/backing",
			LoadEffects: []Effect{EffectCall, EffectRead, EffectThrow}, StoreEffects: []Effect{EffectCall, EffectThrow, EffectWrite}, Origin: origin,
		}},
	}
	_, placeHash, err := CanonicalPlaceRefContract(places)
	if err != nil {
		t.Fatal(err)
	}
	places.ContentHash = placeHash
	requirements := VERT010LogicalCapabilities()
	empty := []RuntimeCapabilityID{}
	return HIRModule{
		SchemaVersion:                 VERT011HIRSchemaVersion,
		Provenance:                    testHIRProvenance(requirements),
		LogicalCapabilityRequirements: requirements,
		PlaceRefs:                     &places,
		Functions: []HIRFunction{{
			ID: 1, Name: "propertyNullishAssign", Exported: true, ReturnType: TypeNumber, Origin: origin,
			Parameters: []HIRParameter{{Name: "value", Value: 1, Type: TypeNullableNumber, Origin: origin}},
			Blocks: []HIRBlock{
				{ID: 1, Operations: []HIROp{
					{ID: 2, Kind: "object.alloc", Type: TypeObject, Effect: EffectAllocate, Effects: []Effect{EffectAllocate}, ObjectTypeKey: typeKeyA, LogicalCapabilityRequirements: []RuntimeCapabilityID{"rt.gc.alloc"}, Origin: origin},
					{ID: 3, Kind: "object.field.init", Type: TypeObject, Operands: []ValueID{2, 1}, Effect: EffectWrite, Effects: []Effect{EffectWrite}, ObjectTypeKey: typeKeyA, PropertySymbolKey: "symbol/backing", LogicalCapabilityRequirements: empty, Origin: origin},
					{ID: 4, Kind: "evaluate.key", Type: TypeString, Effect: EffectPure, Effects: []Effect{EffectPure}, LogicalCapabilityRequirements: empty, Origin: origin},
					{ID: 5, Kind: "place.make", Type: TypeVoid, Operands: []ValueID{3, 4}, PlaceID: 1, Effect: EffectPure, Effects: []Effect{EffectPure}, LogicalCapabilityRequirements: empty, Origin: origin},
					{ID: 6, Kind: "place.load", Type: TypeNullableNumber, PlaceID: 1, Effect: EffectCall, Effects: []Effect{EffectCall, EffectRead, EffectThrow}, LogicalCapabilityRequirements: empty, Origin: origin},
					{ID: 7, Kind: "is_nullish", Type: TypeBoolean, Operands: []ValueID{6}, Effect: EffectPure, Effects: []Effect{EffectPure}, LogicalCapabilityRequirements: empty, Origin: origin},
				}, Terminator: HIRTerminator{Kind: "condbranch", Value: 7, Successors: []BlockID{2, 3}, Origin: origin}},
				{ID: 2, Operations: []HIROp{
					{ID: 8, Kind: "number.constant", Type: TypeNumber, NumberBits: "3ff0000000000000", Effect: EffectPure, Effects: []Effect{EffectPure}, LogicalCapabilityRequirements: empty, Origin: origin},
					{ID: 9, Kind: "place.store", Type: TypeNumber, Operands: []ValueID{8}, PlaceID: 1, Effect: EffectCall, Effects: []Effect{EffectCall, EffectThrow, EffectWrite}, LogicalCapabilityRequirements: empty, Origin: origin},
				}, Terminator: HIRTerminator{Kind: "branch", Successors: []BlockID{4}, Origin: origin}},
				{ID: 3, Operations: []HIROp{
					{ID: 10, Kind: "unwrap_nullable", Type: TypeNumber, Operands: []ValueID{6}, Effect: EffectPure, Effects: []Effect{EffectPure}, LogicalCapabilityRequirements: empty, Origin: origin},
				}, Terminator: HIRTerminator{Kind: "branch", Successors: []BlockID{4}, Origin: origin}},
				{ID: 4, Operations: []HIROp{
					{ID: 11, Kind: "phi", Type: TypeNumber, Operands: []ValueID{9, 10}, IncomingBlocks: []BlockID{2, 3}, Effect: EffectPure, Effects: []Effect{EffectPure}, LogicalCapabilityRequirements: empty, Origin: origin},
				}, Terminator: HIRTerminator{Kind: "return", Value: 11, Origin: origin}},
			},
		}},
	}
}

func TestVERT011PlaceHIRCanonicalRoundTrip(t *testing.T) {
	module := testVERT011HIR(t)
	encoded, hash, err := CanonicalVERT011PlaceHIR(module)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeVERT011PlaceHIR(encoded)
	if err != nil {
		t.Fatal(err)
	}
	reencoded, decodedHash, err := CanonicalVERT011PlaceHIR(*decoded)
	if err != nil || hash != decodedHash || !bytes.Equal(encoded, reencoded) {
		t.Fatalf("VERT-011 HIR canonical round trip failed: %v", err)
	}
}

func TestVERT011PlaceHIRRejectsEvaluationAndCFGTampering(t *testing.T) {
	tests := []struct {
		name string
		edit func(*HIRModule)
	}{
		{"old schema", func(module *HIRModule) { module.SchemaVersion-- }},
		{"unknown schema", func(module *HIRModule) { module.SchemaVersion++ }},
		{"receiver after key", func(module *HIRModule) {
			module.Functions[0].Blocks[0].Operations[1], module.Functions[0].Blocks[0].Operations[2] = module.Functions[0].Blocks[0].Operations[2], module.Functions[0].Blocks[0].Operations[1]
		}},
		{"copied key", func(module *HIRModule) { module.Functions[0].Blocks[0].Operations[3].Operands[1] = 3 }},
		{"wrong backing initialization", func(module *HIRModule) {
			module.Functions[0].Blocks[0].Operations[1].PropertySymbolKey = "symbol/result"
		}},
		{"load different place", func(module *HIRModule) { module.Functions[0].Blocks[0].Operations[4].PlaceID = 2 }},
		{"wrong nullish test", func(module *HIRModule) { module.Functions[0].Blocks[0].Operations[5].Kind = "is_truthy" }},
		{"reversed branch", func(module *HIRModule) { module.Functions[0].Blocks[0].Terminator.Successors = []BlockID{3, 2} }},
		{"RHS on non-assigning edge", func(module *HIRModule) {
			module.Functions[0].Blocks[2].Operations = append(module.Functions[0].Blocks[2].Operations, module.Functions[0].Blocks[1].Operations[0])
		}},
		{"store different place", func(module *HIRModule) { module.Functions[0].Blocks[1].Operations[1].PlaceID = 2 }},
		{"setter twice", func(module *HIRModule) {
			module.Functions[0].Blocks[1].Operations = append(module.Functions[0].Blocks[1].Operations, module.Functions[0].Blocks[1].Operations[1])
		}},
		{"missing phi predecessor", func(module *HIRModule) { module.Functions[0].Blocks[3].Operations[0].IncomingBlocks = []BlockID{2} }},
		{"wrong phi value", func(module *HIRModule) { module.Functions[0].Blocks[3].Operations[0].Operands[0] = 6 }},
		{"wrong RHS constant", func(module *HIRModule) { module.Functions[0].Blocks[1].Operations[0].NumberBits = "4000000000000000" }},
		{"forged getter effect", func(module *HIRModule) { module.Functions[0].Blocks[0].Operations[4].Effects = []Effect{EffectPure} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			module := testVERT011HIR(t)
			test.edit(&module)
			if err := VerifyVERT011PlaceHIR(module); err == nil {
				t.Fatal("tampered VERT-011 HIR was accepted")
			}
		})
	}
}

func TestVERT011StrictDecodeAndOldReadersRejectMetadata(t *testing.T) {
	module := testVERT011HIR(t)
	encoded, _, err := CanonicalVERT011PlaceHIR(module)
	if err != nil {
		t.Fatal(err)
	}
	unknown := bytes.Replace(encoded, []byte(`"schemaVersion":10`), []byte(`"schemaVersion":10,"unknown":true`), 1)
	if _, err := DecodeVERT011PlaceHIR(unknown); err == nil {
		t.Fatal("unknown VERT-011 HIR member was accepted")
	}
	tampered := bytes.Replace(encoded, []byte(typeKeyA), []byte(strings.Repeat("d", 64)), 1)
	if _, err := DecodeVERT011PlaceHIR(tampered); err == nil || !strings.Contains(err.Error(), "hash mismatch") {
		t.Fatalf("stale VERT-011 HIR hash error = %v", err)
	}
	oldObject := testVERT010Module()
	oldObject.PlaceRefs = module.PlaceRefs
	if err := VerifyVERT010ObjectHIR(oldObject); err == nil {
		t.Fatal("VERT-010 reader accepted PlaceRef metadata")
	}
	oldPrimitive := validPhase2ChooseHIR()
	oldPrimitive.PlaceRefs = module.PlaceRefs
	if err := VerifyPhase2HIR(oldPrimitive); err == nil {
		t.Fatal("Phase 2 HIR reader accepted PlaceRef metadata")
	}
	oldFirstSlice := validFirstSliceHIR()
	oldFirstSlice.PlaceRefs = module.PlaceRefs
	if err := VerifyHIR(oldFirstSlice); err == nil {
		t.Fatal("first-slice HIR reader accepted PlaceRef metadata")
	}
}
