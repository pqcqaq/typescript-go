package bingo

import (
	"strings"
	"testing"
)

func testVERT012MIR(t testing.TB) VERT012MIRModule {
	t.Helper()
	hir := testVERT012HIR(t)
	target, err := CanonicalObjectLayoutTarget(ObjectLayoutX8664Triple)
	if err != nil {
		t.Fatal(err)
	}
	cellKey, environmentKey := VERT012LayoutTypeKeys(hir.Closures.ContentHash)
	cell, err := PlanObjectLayout(cellKey, target, []ObjectLayoutPropertyInput{{Key: "value", Kind: ObjectPropertyData, Representation: "f64"}})
	if err != nil {
		t.Fatal(err)
	}
	environment, err := PlanObjectLayout(environmentKey, target, []ObjectLayoutPropertyInput{{Key: "cell", Kind: ObjectPropertyData, Representation: "gc-ref", Reference: true}})
	if err != nil {
		t.Fatal(err)
	}
	module, err := LowerVERT012MIR(hir, cell, environment)
	if err != nil {
		t.Fatal(err)
	}
	return module
}

func TestVERT012MIRRoundTrip(t *testing.T) {
	module := testVERT012MIR(t)
	encoded, hash, err := CanonicalVERT012MIR(module)
	if err != nil {
		t.Fatal(err)
	}
	if module.ContentHash != hash {
		t.Fatal("MIR lowerer did not bind hash")
	}
	decoded, err := DecodeVERT012MIR(encoded)
	if err != nil || decoded.ContentHash != hash {
		t.Fatalf("decode VERT-012 MIR = %#v / %v", decoded, err)
	}
	if _, err := DecodeVERT011MIR(encoded); err == nil {
		t.Fatal("VERT-011 reader accepted VERT-012 MIR")
	}
}

func TestVERT012MIRRejectsTampering(t *testing.T) {
	for name, edit := range map[string]func(*VERT012MIRModule){
		"cell offset":       func(m *VERT012MIRModule) { m.Functions[0].Instructions[1].FieldOffset++ },
		"environment trace": func(m *VERT012MIRModule) { m.Layouts[1].Contract.TraceOffsets = nil },
		"lost cell root":    func(m *VERT012MIRModule) { m.GCSafety.Blocks[0].Instructions[8].ActiveSlots = nil },
		"direct call":       func(m *VERT012MIRModule) { m.Functions[1].Instructions[5].Kind = "call.direct" },
		"second closure":    func(m *VERT012MIRModule) { m.Functions[1].Instructions[6].Operands[0] = 4 },
	} {
		t.Run(name, func(t *testing.T) {
			module := testVERT012MIR(t)
			edit(&module)
			if err := VerifyVERT012MIR(module); err == nil {
				t.Fatal("tampered VERT-012 MIR accepted")
			}
		})
	}
}

func TestVERT012BoundMIRRoundTripAndTamper(t *testing.T) {
	module := testVERT012MIR(t)
	bindings := make([]BoundCapability, len(module.LogicalCapabilityRequirements))
	for index, requirement := range module.LogicalCapabilityRequirements {
		bindings[index] = BoundCapability{LogicalName: requirement, SymbolName: "bound_" + string(requirement), SignatureHash: strings.Repeat("a", 64)}
	}
	bound, err := NewVERT012BoundMIR(module, strings.Repeat("b", 64), strings.Repeat("c", 64), bindings)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _, err := CanonicalVERT012BoundMIR(bound)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeVERT012BoundMIR(encoded); err != nil {
		t.Fatal(err)
	}
	bound.Closure.Bindings[0].SymbolName = "forged"
	if err := VerifyVERT012BoundMIR(bound); err == nil {
		t.Fatal("forged VERT-012 binding accepted")
	}
}
