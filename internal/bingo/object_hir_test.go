package bingo

import (
	"math"
	"slices"
	"strings"
	"testing"
)

func testVERT010ObjectType() HIRObjectType {
	typ := HIRObjectType{
		TypeKey: strings.Repeat("b", 64),
		Properties: []HIRObjectProperty{{
			Key: "value", SymbolKey: "symbol/value", SourceTypeKey: strings.Repeat("d", 64), Type: TypeNumber, Mutable: true, Required: true,
		}},
	}
	typ.SemanticContractHash, _ = VERT010ObjectSemanticContractHash(typ)
	return typ
}

func testVERT010Module() HIRModule {
	origin := Origin{File: "/project/objectalias.ts", Start: 1, End: 2}
	typ := testVERT010ObjectType()
	key := typ.TypeKey
	property := typ.Properties[0].SymbolKey
	empty := []RuntimeCapabilityID{}
	return HIRModule{
		SchemaVersion:                 VERT010HIRSchemaVersion,
		Provenance:                    testHIRProvenance(VERT010LogicalCapabilities()),
		LogicalCapabilityRequirements: VERT010LogicalCapabilities(),
		ObjectTypes:                   []HIRObjectType{typ},
		Functions: []HIRFunction{{
			ID: 1, Name: "objectAlias", Exported: true, ReturnType: TypeNumber, Origin: origin,
			Parameters: []HIRParameter{{Name: "value", Value: 1, Type: TypeNumber, Origin: origin}},
			Blocks: []HIRBlock{{ID: 1, Operations: []HIROp{
				{ID: 2, Kind: "object.alloc", Type: TypeObject, Effect: EffectAllocate, ObjectTypeKey: key, LogicalCapabilityRequirements: []RuntimeCapabilityID{"rt.gc.alloc"}, Origin: origin},
				{ID: 3, Kind: "object.field.init", Type: TypeObject, Operands: []ValueID{2, 1}, Effect: EffectWrite, ObjectTypeKey: key, PropertySymbolKey: property, LogicalCapabilityRequirements: empty, Origin: origin},
				{ID: 4, Kind: "object.alias", Type: TypeObject, Operands: []ValueID{3}, Effect: EffectPure, ObjectTypeKey: key, LogicalCapabilityRequirements: empty, Origin: origin},
				{ID: 5, Kind: "object.field.load", Type: TypeNumber, Operands: []ValueID{4}, Effect: EffectRead, ObjectTypeKey: key, PropertySymbolKey: property, LogicalCapabilityRequirements: empty, Origin: origin},
				{ID: 6, Kind: "number.constant", Type: TypeNumber, NumberBits: "3ff0000000000000", Effect: EffectPure, LogicalCapabilityRequirements: empty, Origin: origin},
				{ID: 7, Kind: "binary", Type: TypeNumber, Operands: []ValueID{5, 6}, Operator: "+", Effect: EffectPure, LogicalCapabilityRequirements: empty, Origin: origin},
				{ID: 8, Kind: "object.field.store", Type: TypeObject, Operands: []ValueID{4, 7}, Effect: EffectWrite, ObjectTypeKey: key, PropertySymbolKey: property, LogicalCapabilityRequirements: empty, Origin: origin},
				{ID: 9, Kind: "object.field.load", Type: TypeNumber, Operands: []ValueID{3}, Effect: EffectRead, ObjectTypeKey: key, PropertySymbolKey: property, LogicalCapabilityRequirements: empty, Origin: origin},
			}, Terminator: HIRTerminator{Kind: "return", Value: 9, Origin: origin}}},
		}},
	}
}

func TestVerifyVERT010ObjectHIR(t *testing.T) {
	module := testVERT010Module()
	if err := VerifyVERT010ObjectHIR(module); err != nil {
		t.Fatal(err)
	}
	encoded, hash, err := CanonicalVERT010ObjectHIR(module)
	if err != nil {
		t.Fatal(err)
	}
	module.ContentHash = hash
	if err := VerifyCanonicalVERT010ObjectHIR(module); err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeVERT010ObjectHIR(encoded)
	if err != nil || decoded.ContentHash != hash {
		t.Fatalf("decode VERT-010 HIR = %#v / %v", decoded, err)
	}
	if math.Float64frombits(0x3ff0000000000000) != 1 {
		t.Fatal("invalid binary64 test constant")
	}
}

func TestVerifyVERT010ObjectHIRRejectsAliasAndOrderTampering(t *testing.T) {
	tests := []struct {
		name string
		edit func(*HIRModule)
	}{
		{"old schema", func(module *HIRModule) { module.SchemaVersion = VERT010HIRSchemaVersion - 1 }},
		{"unknown schema", func(module *HIRModule) { module.SchemaVersion = VERT010HIRSchemaVersion + 1 }},
		{"forged semantic contract", func(module *HIRModule) { module.ObjectTypes[0].SemanticContractHash = strings.Repeat("c", 64) }},
		{"missing initialization", func(module *HIRModule) {
			module.Functions[0].Blocks[0].Operations = append(module.Functions[0].Blocks[0].Operations[:1], module.Functions[0].Blocks[0].Operations[2:]...)
		}},
		{"wrong allocation effect", func(module *HIRModule) { module.Functions[0].Blocks[0].Operations[0].Effect = EffectPure }},
		{"missing allocation capability", func(module *HIRModule) {
			module.Functions[0].Blocks[0].Operations[0].LogicalCapabilityRequirements = []RuntimeCapabilityID{}
		}},
		{"copied alias", func(module *HIRModule) { module.Functions[0].Blocks[0].Operations[2].Operands[0] = 2 }},
		{"store through original", func(module *HIRModule) { module.Functions[0].Blocks[0].Operations[6].Operands[0] = 3 }},
		{"return through alias", func(module *HIRModule) { module.Functions[0].Blocks[0].Operations[7].Operands[0] = 4 }},
		{"wrong property", func(module *HIRModule) {
			module.Functions[0].Blocks[0].Operations[3].PropertySymbolKey = "symbol/other"
		}},
		{"spurious barrier capability", func(module *HIRModule) {
			module.LogicalCapabilityRequirements = append(module.LogicalCapabilityRequirements, "rt.gc.write_barrier")
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			module := testVERT010Module()
			test.edit(&module)
			if err := VerifyVERT010ObjectHIR(module); err == nil {
				t.Fatal("tampered VERT-010 HIR was accepted")
			}
		})
	}
}

func TestVERT010ObjectTypeAndOperationShapes(t *testing.T) {
	types := []HIRObjectType{testVERT010ObjectType()}
	if err := VerifyVERT010ObjectTypes(types); err != nil {
		t.Fatal(err)
	}
	operations := []HIROp{
		{ID: 2, Kind: "object.alloc", Type: TypeObject, Effect: EffectAllocate, ObjectTypeKey: types[0].TypeKey, LogicalCapabilityRequirements: []RuntimeCapabilityID{"rt.gc.alloc"}},
		{ID: 3, Kind: "object.field.init", Type: TypeObject, Operands: []ValueID{2, 1}, Effect: EffectWrite, ObjectTypeKey: types[0].TypeKey, PropertySymbolKey: "symbol/value", LogicalCapabilityRequirements: []RuntimeCapabilityID{}},
		{ID: 4, Kind: "object.alias", Type: TypeObject, Operands: []ValueID{3}, Effect: EffectPure, ObjectTypeKey: types[0].TypeKey, LogicalCapabilityRequirements: []RuntimeCapabilityID{}},
		{ID: 5, Kind: "object.field.load", Type: TypeNumber, Operands: []ValueID{4}, Effect: EffectRead, ObjectTypeKey: types[0].TypeKey, PropertySymbolKey: "symbol/value", LogicalCapabilityRequirements: []RuntimeCapabilityID{}},
		{ID: 7, Kind: "object.field.store", Type: TypeObject, Operands: []ValueID{4, 6}, Effect: EffectWrite, ObjectTypeKey: types[0].TypeKey, PropertySymbolKey: "symbol/value", LogicalCapabilityRequirements: []RuntimeCapabilityID{}},
	}
	for _, operation := range operations {
		if err := VerifyVERT010ObjectOperationShape(operation, types); err != nil {
			t.Fatalf("%s: %v", operation.Kind, err)
		}
	}
	if got := VERT010LogicalCapabilities(); !slices.IsSorted(got) || !slices.Equal(got, vert010Capabilities) {
		t.Fatalf("unexpected capability closure: %v", got)
	}
}

func TestVERT010ObjectShapesRejectTampering(t *testing.T) {
	tests := []struct {
		name string
		edit func(*HIRObjectType, *HIROp)
	}{
		{"semantic hash", func(typ *HIRObjectType, _ *HIROp) { typ.SemanticContractHash = "bad" }},
		{"property symbol", func(_ *HIRObjectType, op *HIROp) { op.PropertySymbolKey = "symbol/other" }},
		{"allocation capability", func(_ *HIRObjectType, op *HIROp) { op.LogicalCapabilityRequirements = nil }},
		{"physical offset leakage", func(_ *HIRObjectType, op *HIROp) { op.Operator = "24" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			typ := testVERT010ObjectType()
			op := HIROp{ID: 2, Kind: "object.alloc", Type: TypeObject, Effect: EffectAllocate, ObjectTypeKey: typ.TypeKey, LogicalCapabilityRequirements: []RuntimeCapabilityID{"rt.gc.alloc"}}
			if test.name == "property symbol" {
				op = HIROp{ID: 3, Kind: "object.field.load", Type: TypeNumber, Operands: []ValueID{2}, Effect: EffectRead, ObjectTypeKey: typ.TypeKey, PropertySymbolKey: "symbol/value", LogicalCapabilityRequirements: []RuntimeCapabilityID{}}
			}
			test.edit(&typ, &op)
			if err := VerifyVERT010ObjectOperationShape(op, []HIRObjectType{typ}); err == nil {
				t.Fatal("tampered object HIR was accepted")
			}
		})
	}
}
