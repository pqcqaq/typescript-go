package bingo

import (
	"bytes"
	"strings"
	"testing"
)

type vert010TestTB interface {
	Helper()
	Fatal(args ...any)
}

func testVERT010MIR(t vert010TestTB) VERT010MIRModule {
	t.Helper()
	target, err := CanonicalObjectLayoutTarget(ObjectLayoutX8664Triple)
	if err != nil {
		t.Fatal(err)
	}
	layout, err := PlanObjectLayout(typeKeyB, target, []ObjectLayoutPropertyInput{{Key: "value", Kind: ObjectPropertyData, Representation: "f64"}})
	if err != nil {
		t.Fatal(err)
	}
	gc, err := FinalizeGCSafetyPlan(GCSafetyPlan{
		FunctionKey: typeKeyB,
		Slots:       []GCRootSlot{{ID: 1, TraceLayoutHash: layout.ContentHash}},
		Blocks: []GCSafetyBlock{{ID: 1, Terminator: "return", Instructions: []GCInstruction{
			{ID: 1, Kind: GCOpFrameLink},
			{ID: 2, Kind: GCOpRootClear, Slot: 1},
			{ID: 3, Kind: GCOpRootPublish, ActiveSlots: []GCRootSlotID{}},
			{ID: 4, Kind: GCOpSafepoint, SafepointKind: "allocation", MayAllocate: true},
			{ID: 5, Kind: GCOpRefDef, Value: 1},
			{ID: 6, Kind: GCOpRootStore, Slot: 1, Value: 1},
			{ID: 7, Kind: GCOpRootPublish, ActiveSlots: []GCRootSlotID{1}},
			{ID: 8, Kind: GCOpSafepoint, SafepointKind: "forced-collection", MayAllocate: true},
			{ID: 9, Kind: GCOpRootReload, Slot: 1, Value: 1},
			{ID: 10, Kind: GCOpRefUse, Uses: []GCValueID{1}},
			{ID: 11, Kind: GCOpFrameUnlink},
		}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	origin := Origin{File: "/project/objectalias.ts", Start: 1, End: 2}
	fieldOffset := layout.Properties[0].FieldOffset
	property := "symbol/value"
	return VERT010MIRModule{
		SchemaVersion:                 VERT010MIRSchemaVersion,
		HIRHash:                       typeKeyA,
		LogicalCapabilityRequirements: VERT010LogicalCapabilities(),
		Layout: VERT010MIRLayoutBinding{
			SemanticTypeKey: layout.TypeKey, LayoutContentHash: layout.ContentHash, SchemaHash: layout.SchemaHash, Target: layout.Target,
			ObjectSize: layout.ObjectSize, ObjectAlign: layout.ObjectAlign, Contract: layout,
			Fields: []VERT010MIRFieldBinding{{PropertySymbolKey: property, Representation: VERT010RepF64, FieldOffset: fieldOffset, PresenceBit: -1}},
		},
		GCSafety: gc,
		Function: VERT010MIRFunction{Name: "objectAlias", ReturnType: VERT010RepF64, Origin: origin, Instructions: []VERT010MIRInstruction{
			{ID: 2, Kind: "object.alloc", Type: VERT010RepGcRef, Effect: EffectAllocate, Origin: origin},
			{ID: 3, Kind: "object.field.init", Type: VERT010RepGcRef, Operands: []ValueID{2, 1}, PropertySymbolKey: property, FieldOffset: fieldOffset, Effect: EffectWrite, Origin: origin},
			{ID: 4, Kind: "object.alias", Type: VERT010RepGcRef, Operands: []ValueID{3}, Effect: EffectPure, Origin: origin},
			{ID: 5, Kind: "object.field.load", Type: VERT010RepF64, Operands: []ValueID{4}, PropertySymbolKey: property, FieldOffset: fieldOffset, Effect: EffectRead, Origin: origin},
			{ID: 6, Kind: "f64.const", Type: VERT010RepF64, NumberBits: "3ff0000000000000", Effect: EffectPure, Origin: origin},
			{ID: 7, Kind: "fadd", Type: VERT010RepF64, Operands: []ValueID{5, 6}, Effect: EffectPure, Origin: origin},
			{ID: 8, Kind: "object.field.store", Type: VERT010RepGcRef, Operands: []ValueID{4, 7}, PropertySymbolKey: property, FieldOffset: fieldOffset, Effect: EffectWrite, Origin: origin},
			{ID: 9, Kind: "object.field.load", Type: VERT010RepF64, Operands: []ValueID{3}, PropertySymbolKey: property, FieldOffset: fieldOffset, Effect: EffectRead, Origin: origin},
		}},
	}
}

func TestVERT010MIRBindsLayoutAndGCSafety(t *testing.T) {
	module := testVERT010MIR(t)
	if err := VerifyVERT010MIR(module); err != nil {
		t.Fatal(err)
	}
	encoded, hash, err := CanonicalVERT010MIR(module)
	if err != nil || len(encoded) == 0 || len(hash) != 64 {
		t.Fatalf("canonical VERT-010 MIR = %d/%q/%v", len(encoded), hash, err)
	}
}

func TestLowerVERT010MIRFromCanonicalHIR(t *testing.T) {
	hir := testVERT010Module()
	_, hirHash, err := CanonicalVERT010ObjectHIR(hir)
	if err != nil {
		t.Fatal(err)
	}
	hir.ContentHash = hirHash
	target, err := CanonicalObjectLayoutTarget(ObjectLayoutX8664Triple)
	if err != nil {
		t.Fatal(err)
	}
	layout, err := PlanObjectLayout(hir.ObjectTypes[0].TypeKey, target, []ObjectLayoutPropertyInput{{Key: "value", Kind: ObjectPropertyData, Representation: "f64"}})
	if err != nil {
		t.Fatal(err)
	}
	first, err := LowerVERT010MIR(hir, layout)
	if err != nil {
		t.Fatal(err)
	}
	second, err := LowerVERT010MIR(hir, layout)
	if err != nil {
		t.Fatal(err)
	}
	firstBytes, _, err := CanonicalVERT010MIR(first)
	if err != nil {
		t.Fatal(err)
	}
	secondBytes, _, err := CanonicalVERT010MIR(second)
	if err != nil || string(firstBytes) != string(secondBytes) {
		t.Fatalf("MIR lowering is not deterministic: %v", err)
	}
	decoded, err := DecodeVERT010MIR(firstBytes)
	if err != nil || decoded.ContentHash != first.ContentHash {
		t.Fatalf("decode MIR = %#v / %v", decoded, err)
	}
}

func TestVERT010BoundMIRRejectsCapabilitySubstitution(t *testing.T) {
	module := testVERT010MIR(t)
	_, hash, err := CanonicalVERT010MIR(module)
	if err != nil {
		t.Fatal(err)
	}
	module.ContentHash = hash
	bindings := make([]BoundCapability, len(module.LogicalCapabilityRequirements))
	for index, requirement := range module.LogicalCapabilityRequirements {
		bindings[index] = BoundCapability{LogicalName: requirement, SymbolName: "symbol_" + string(requirement), SignatureHash: typeKeyA}
	}
	bound, err := NewVERT010BoundMIR(module, typeKeyB, typeKeyC, bindings)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyVERT010BoundMIR(bound); err != nil {
		t.Fatal(err)
	}
	bound.Closure.Bindings[0].SymbolName = "bingo_substituted_v1"
	if err := VerifyVERT010BoundMIR(bound); err == nil {
		t.Fatal("substituted bound symbol was accepted")
	}
}

func TestVERT010BoundMIRCanonicalRoundTrip(t *testing.T) {
	module := testVERT010MIR(t)
	_, hash, err := CanonicalVERT010MIR(module)
	if err != nil {
		t.Fatal(err)
	}
	module.ContentHash = hash
	bindings := make([]BoundCapability, len(module.LogicalCapabilityRequirements))
	for index, requirement := range module.LogicalCapabilityRequirements {
		bindings[index] = BoundCapability{LogicalName: requirement, SymbolName: "symbol_" + string(requirement), SignatureHash: typeKeyA}
	}
	bound, err := NewVERT010BoundMIR(module, typeKeyB, typeKeyC, bindings)
	if err != nil {
		t.Fatal(err)
	}
	encoded, hash, err := CanonicalVERT010BoundMIR(bound)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeVERT010BoundMIR(encoded)
	if err != nil {
		t.Fatal(err)
	}
	reencoded, decodedHash, err := CanonicalVERT010BoundMIR(*decoded)
	if err != nil {
		t.Fatal(err)
	}
	if hash != decodedHash || !bytes.Equal(encoded, reencoded) {
		t.Fatal("bound MIR canonical round trip was not deterministic")
	}
	unknown := bytes.Replace(encoded, []byte(`"schemaVersion":1`), []byte(`"schemaVersion":1,"unknown":true`), 1)
	if _, err := DecodeVERT010BoundMIR(unknown); err == nil {
		t.Fatal("unknown bound MIR member was accepted")
	}
	tampered := bytes.Replace(encoded, []byte(bindings[0].SymbolName), []byte("symbol_substitution"), 1)
	if _, err := DecodeVERT010BoundMIR(tampered); err == nil {
		t.Fatal("capability substitution with stale content hashes was accepted")
	}
}

func TestVERT010MIRRejectsLayoutAndRootTampering(t *testing.T) {
	tests := []struct {
		name string
		edit func(*VERT010MIRModule)
	}{
		{"old schema", func(module *VERT010MIRModule) { module.SchemaVersion = VERT010MIRSchemaVersion - 1 }},
		{"unknown schema", func(module *VERT010MIRModule) { module.SchemaVersion = VERT010MIRSchemaVersion + 1 }},
		{"layout hash", func(module *VERT010MIRModule) { module.Layout.LayoutContentHash = typeKeyC }},
		{"layout schema hash", func(module *VERT010MIRModule) { module.Layout.SchemaHash = typeKeyC }},
		{"target hash", func(module *VERT010MIRModule) { module.Layout.Target.DataLayoutHash = typeKeyC }},
		{"trace mismatch", func(module *VERT010MIRModule) {
			module.Layout.Contract.TraceOffsets = []uint32{module.Layout.Fields[0].FieldOffset}
		}},
		{"field offset", func(module *VERT010MIRModule) { module.Function.Instructions[3].FieldOffset += 8 }},
		{"representation", func(module *VERT010MIRModule) { module.Layout.Fields[0].Representation = VERT010RepGcRef }},
		{"root proof", func(module *VERT010MIRModule) {
			module.GCSafety.Blocks[0].Instructions[8].Kind = GCOpRefUse
			module.GCSafety.Blocks[0].Instructions[8].Uses = []GCValueID{1}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			module := testVERT010MIR(t)
			test.edit(&module)
			if err := VerifyVERT010MIR(module); err == nil {
				t.Fatal("tampered VERT-010 MIR was accepted")
			} else if strings.TrimSpace(err.Error()) == "" {
				t.Fatal("tamper rejection has no diagnostic")
			}
		})
	}
}
