package bingo

import (
	"bytes"
	"testing"
)

func testVERT013aMIR(t testing.TB) VERT013aMIRModule {
	t.Helper()
	target, err := CanonicalObjectLayoutTarget(ObjectLayoutX8664Triple)
	if err != nil {
		t.Fatal(err)
	}
	hir := testVERT013aHIR(t)
	layout, err := PlanObjectLayout(hir.Classes.Classes[0].InstanceTypeKey, target, []ObjectLayoutPropertyInput{{Key: "value", Kind: ObjectPropertyData, Representation: "f64"}})
	if err != nil {
		t.Fatal(err)
	}
	module, err := LowerVERT013aMIR(hir, layout)
	if err != nil {
		t.Fatal(err)
	}
	return module
}

func TestVERT013aMIRRoundTripAndReaderIsolation(t *testing.T) {
	module := testVERT013aMIR(t)
	encoded, hash, err := CanonicalVERT013aMIR(module)
	if err != nil {
		t.Fatal(err)
	}
	if module.ContentHash != hash {
		t.Fatal("lowering did not bind MIR hash")
	}
	decoded, err := DecodeVERT013aMIR(encoded)
	if err != nil || decoded.ContentHash != hash {
		t.Fatalf("decode VERT-013a MIR = %#v / %v", decoded, err)
	}
	if _, err := DecodeVERT010MIR(encoded); err == nil {
		t.Fatal("VERT-010 reader accepted VERT-013a")
	}
	if _, err := DecodeVERT011MIR(encoded); err == nil {
		t.Fatal("VERT-011 reader accepted VERT-013a")
	}
	if _, err := DecodeVERT012MIR(encoded); err == nil {
		t.Fatal("VERT-012 reader accepted VERT-013a")
	}
	canonical, _, err := CanonicalVERT013aMIR(*decoded)
	if err != nil || !bytes.Equal(encoded, canonical) {
		t.Fatal("VERT-013a MIR is not canonical")
	}
}

func TestVERT013aMIRRejectsTampering(t *testing.T) {
	for name, edit := range map[string]func(*VERT013aMIRModule){
		"schema":         func(m *VERT013aMIRModule) { m.SchemaVersion-- },
		"class contract": func(m *VERT013aMIRModule) { m.ClassContractHash = typeKeyA },
		"layout type":    func(m *VERT013aMIRModule) { m.Layout.TypeKey = typeKeyA },
		"trace field":    func(m *VERT013aMIRModule) { m.Layout.TraceOffsets = []uint32{m.Layout.Properties[0].FieldOffset} },
		"constructor alloc after init": func(m *VERT013aMIRModule) {
			m.Functions[0].Instructions[0], m.Functions[0].Instructions[2] = m.Functions[0].Instructions[2], m.Functions[0].Instructions[0]
		},
		"receiver mismatch":  func(m *VERT013aMIRModule) { m.Functions[2].Instructions[2].Operands[0] = 3 },
		"method direct call": func(m *VERT013aMIRModule) { m.Functions[2].Instructions[1].Callee = 1 },
		"field offset":       func(m *VERT013aMIRModule) { m.Functions[1].Instructions[0].FieldOffset += 8 },
		"root reload":        func(m *VERT013aMIRModule) { m.GCSafety.Blocks[0].Instructions[8].Kind = GCOpRefUse },
	} {
		t.Run(name, func(t *testing.T) {
			module := testVERT013aMIR(t)
			edit(&module)
			if err := VerifyVERT013aMIR(module); err == nil {
				t.Fatal("tampered VERT-013a MIR accepted")
			}
		})
	}
}

func TestVERT013aBoundMIRRejectsTampering(t *testing.T) {
	module := testVERT013aMIR(t)
	bindings := make([]BoundCapability, len(module.LogicalCapabilityRequirements))
	for index, requirement := range module.LogicalCapabilityRequirements {
		bindings[index] = BoundCapability{LogicalName: requirement, SymbolName: "test_" + string(requirement), SignatureHash: typeKeyA}
	}
	bound, err := NewVERT013aBoundMIR(module, typeKeyB, typeKeyC, bindings)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeVERT013aBoundMIR(mustVERT013aBoundMIR(t, bound)); err != nil {
		t.Fatal(err)
	}
	bound.Closure.Bindings[0].SymbolName = "substituted"
	if err := VerifyVERT013aBoundMIR(bound); err == nil {
		t.Fatal("tampered VERT-013a bound MIR accepted")
	}
}

func mustVERT013aBoundMIR(t testing.TB, bound VERT013aBoundMIR) []byte {
	t.Helper()
	encoded, _, err := CanonicalVERT013aBoundMIR(bound)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}
