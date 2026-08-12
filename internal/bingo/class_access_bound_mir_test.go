package bingo

import "testing"

func TestClassAccessBoundMIRRoundTripAndTamperRejection(t *testing.T) {
	layout := testClassAccessLayout(t)
	requirements := layout.MIR.HIR.LogicalCapabilityRequirements
	bindings := make([]BoundCapability, 0, len(requirements))
	for _, requirement := range requirements {
		bindings = append(bindings, BoundCapability{LogicalName: requirement, SymbolName: "rt_" + string(requirement), SignatureHash: typeKeyA})
	}
	bound, err := NewClassAccessBoundMIR(layout, layout.MIR.Target.TargetContextHash, typeKeyB, bindings)
	if err != nil {
		t.Fatal(err)
	}
	encoded, hash, err := CanonicalClassAccessBoundMIR(bound)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeClassAccessBoundMIR(encoded)
	if err != nil || decoded.ContentHash != hash {
		t.Fatalf("bound access MIR decode = %#v / %v", decoded, err)
	}
	for name, mutate := range map[string]func(*ClassAccessBoundMIR){
		"target":       func(m *ClassAccessBoundMIR) { m.TargetContextHash = typeKeyC },
		"layout hash":  func(m *ClassAccessBoundMIR) { m.LayoutHash = typeKeyC },
		"gc safepoint": func(m *ClassAccessBoundMIR) { m.GCSafety.Blocks[0].Instructions[3].SafepointKind = "forged" },
		"capability":   func(m *ClassAccessBoundMIR) { m.Closure.Bindings[0].SymbolName = "forged" },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := bound
			mutate(&candidate)
			if err := VerifyClassAccessBoundMIR(candidate); err == nil {
				t.Fatal("tampered bound access MIR accepted")
			}
		})
	}
}
