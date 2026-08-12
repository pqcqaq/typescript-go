package bingo

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func testVERT011MIR(t testing.TB) VERT011MIRModule {
	t.Helper()
	hir := testVERT011HIR(t)
	_, hirHash, err := CanonicalVERT011PlaceHIR(hir)
	if err != nil {
		t.Fatal(err)
	}
	hir.ContentHash = hirHash
	target, err := CanonicalObjectLayoutTarget(ObjectLayoutX8664Triple)
	if err != nil {
		t.Fatal(err)
	}
	layout, err := PlanObjectLayout(hir.PlaceRefs.Places[0].ObjectTypeKey, target, []ObjectLayoutPropertyInput{
		{Key: "backing", Kind: ObjectPropertyData, Representation: "nullable-f64"},
		{Key: "result", Kind: ObjectPropertyAccessor},
	})
	if err != nil {
		t.Fatal(err)
	}
	module, err := LowerVERT011MIR(hir, layout)
	if err != nil {
		t.Fatal(err)
	}
	return module
}

func TestLowerVERT011MIRCanonicalRoundTrip(t *testing.T) {
	first := testVERT011MIR(t)
	second := testVERT011MIR(t)
	firstBytes, firstHash, err := CanonicalVERT011MIR(first)
	if err != nil {
		t.Fatal(err)
	}
	secondBytes, secondHash, err := CanonicalVERT011MIR(second)
	if err != nil {
		t.Fatal(err)
	}
	if firstHash != secondHash || !bytes.Equal(firstBytes, secondBytes) {
		t.Fatal("VERT-011 MIR lowering is not deterministic")
	}
	decoded, err := DecodeVERT011MIR(firstBytes)
	if err != nil {
		t.Fatal(err)
	}
	reencoded, decodedHash, err := CanonicalVERT011MIR(*decoded)
	if err != nil || decodedHash != firstHash || !bytes.Equal(firstBytes, reencoded) {
		t.Fatalf("VERT-011 MIR round trip failed: %v", err)
	}
}

func TestVERT011MIRRejectsLayoutCFGAndAccessorTampering(t *testing.T) {
	tests := []struct {
		name string
		edit func(*VERT011MIRModule)
	}{
		{"old schema", func(module *VERT011MIRModule) { module.SchemaVersion-- }},
		{"unknown schema", func(module *VERT011MIRModule) { module.SchemaVersion++ }},
		{"layout hash", func(module *VERT011MIRModule) { module.Layout.LayoutContentHash = typeKeyC }},
		{"nullable representation", func(module *VERT011MIRModule) { module.Layout.Fields[0].Representation = VERT011RepF64 }},
		{"backing offset", func(module *VERT011MIRModule) { module.Function.Blocks[0].Instructions[1].FieldOffset += 8 }},
		{"getter substitution", func(module *VERT011MIRModule) {
			module.Function.Blocks[0].Instructions[4].AccessorSymbolKey = "other/getter"
		}},
		{"setter substitution", func(module *VERT011MIRModule) {
			module.Function.Blocks[1].Instructions[1].AccessorSymbolKey = "other/setter"
		}},
		{"getter signature", func(module *VERT011MIRModule) { module.Accessors.GetterSignature = "cdecl(ptr)->f64" }},
		{"setter signature", func(module *VERT011MIRModule) { module.Accessors.SetterSignature = "cdecl(ptr,nullable-f64)->void" }},
		{"copied receiver", func(module *VERT011MIRModule) { module.Function.Blocks[0].Instructions[4].Operands[0] = 4 }},
		{"copied key", func(module *VERT011MIRModule) { module.Function.Blocks[1].Instructions[1].Operands[1] = 3 }},
		{"wrong branch", func(module *VERT011MIRModule) { module.Function.Blocks[0].Terminator.Successors = []BlockID{3, 2} }},
		{"RHS on skip edge", func(module *VERT011MIRModule) {
			module.Function.Blocks[2].Instructions = append(module.Function.Blocks[2].Instructions, module.Function.Blocks[1].Instructions[0])
		}},
		{"wrong phi", func(module *VERT011MIRModule) { module.Function.Blocks[3].Instructions[0].Operands[0] = 8 }},
		{"forged getter effect", func(module *VERT011MIRModule) {
			module.Function.Blocks[0].Instructions[4].Effects = []Effect{EffectPure}
		}},
		{"GC layout substitution", func(module *VERT011MIRModule) { module.GCSafety.Slots[0].TraceLayoutHash = typeKeyC }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			module := cloneVERT011MIR(t, testVERT011MIR(t))
			test.edit(&module)
			if err := VerifyVERT011MIR(module); err == nil {
				t.Fatal("tampered VERT-011 MIR was accepted")
			}
		})
	}
}

func TestDecodeVERT011MIRRejectsUnknownAndStaleHash(t *testing.T) {
	module := testVERT011MIR(t)
	encoded, _, err := CanonicalVERT011MIR(module)
	if err != nil {
		t.Fatal(err)
	}
	unknown := bytes.Replace(encoded, []byte(`"schemaVersion":8`), []byte(`"schemaVersion":8,"unknown":true`), 1)
	if _, err := DecodeVERT011MIR(unknown); err == nil {
		t.Fatal("unknown VERT-011 MIR member was accepted")
	}
	tampered := bytes.Replace(encoded, []byte(module.Place.GetterSymbolKey), []byte(strings.Repeat("g", len(module.Place.GetterSymbolKey))), 1)
	if _, err := DecodeVERT011MIR(tampered); err == nil {
		t.Fatal("stale VERT-011 MIR hash was accepted")
	}
	if _, err := DecodeStructuralFirstSliceMIR(encoded); err == nil {
		t.Fatal("MIR v6 reader accepted VERT-011 MIR v8")
	}
	if _, err := DecodeVERT010MIR(encoded); err == nil {
		t.Fatal("VERT-010 MIR v7 reader accepted VERT-011 MIR v8")
	}
}

func TestVERT011BoundMIRCanonicalRoundTripAndSubstitution(t *testing.T) {
	module := testVERT011MIR(t)
	bindings := make([]BoundCapability, len(module.LogicalCapabilityRequirements))
	for index, requirement := range module.LogicalCapabilityRequirements {
		bindings[index] = BoundCapability{LogicalName: requirement, SymbolName: "symbol_" + string(requirement), SignatureHash: typeKeyA}
	}
	bound, err := NewVERT011BoundMIR(module, typeKeyB, typeKeyC, bindings)
	if err != nil {
		t.Fatal(err)
	}
	encoded, hash, err := CanonicalVERT011BoundMIR(bound)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeVERT011BoundMIR(encoded)
	if err != nil {
		t.Fatal(err)
	}
	reencoded, decodedHash, err := CanonicalVERT011BoundMIR(*decoded)
	if err != nil || hash != decodedHash || !bytes.Equal(encoded, reencoded) {
		t.Fatalf("VERT-011 bound MIR round trip failed: %v", err)
	}
	bound.Closure.Bindings[0].SymbolName = "substituted"
	if err := VerifyVERT011BoundMIR(bound); err == nil {
		t.Fatal("VERT-011 bound capability substitution was accepted")
	}
	unknown := bytes.Replace(encoded, []byte(`"schemaVersion":1`), []byte(`"schemaVersion":1,"unknown":true`), 1)
	if _, err := DecodeVERT011BoundMIR(unknown); err == nil {
		t.Fatal("unknown VERT-011 bound MIR member was accepted")
	}
}

func cloneVERT011MIR(t testing.TB, module VERT011MIRModule) VERT011MIRModule {
	t.Helper()
	encoded, err := json.Marshal(module)
	if err != nil {
		t.Fatal(err)
	}
	var clone VERT011MIRModule
	if err := json.Unmarshal(encoded, &clone); err != nil {
		t.Fatal(err)
	}
	return clone
}
