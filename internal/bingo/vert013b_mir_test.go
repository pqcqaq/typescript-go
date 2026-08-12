package bingo

import "testing"

func testVERT013bMIR(t testing.TB) VERT013bMIRModule {
	t.Helper()
	hir := testVERT013bHIR(t)
	layout := testVERT013bLayout(t)
	module, err := LowerVERT013bMIR(hir, layout)
	if err != nil {
		t.Fatal(err)
	}
	return module
}

func TestVERT013bMIRRoundTripAndReaderIsolation(t *testing.T) {
	module := testVERT013bMIR(t)
	encoded, hash, err := CanonicalVERT013bMIR(module)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeVERT013bMIR(encoded)
	if err != nil || decoded.ContentHash != hash {
		t.Fatalf("decode VERT-013b MIR = %#v / %v", decoded, err)
	}
	if _, err := DecodeVERT013aMIR(encoded); err == nil {
		t.Fatal("MIR v10 reader accepted MIR v11")
	}
}

func TestVERT013bMIRRejectsTampering(t *testing.T) {
	for name, mutate := range map[string]func(*VERT013bMIRModule){
		"class contract": func(m *VERT013bMIRModule) { m.ClassContractHash = typeKeyC },
		"base prefix": func(m *VERT013bMIRModule) {
			m.Functions[2].Instructions[0].FieldOffset = m.Layout.Derived.Properties[1].FieldOffset
		},
		"super callee": func(m *VERT013bMIRModule) { m.Functions[1].Instructions[1].Callee = 2 },
		"super receiver": func(m *VERT013bMIRModule) {
			m.Functions[1].Instructions[1].Operands[0] = 1
		},
		"method receiver": func(m *VERT013bMIRModule) {
			m.Functions[3].Instructions[2].Operands[0] = 4
		},
		"root reload": func(m *VERT013bMIRModule) {
			m.GCSafety.Blocks[0].Instructions[8].Kind = GCOpRefUse
		},
	} {
		t.Run(name, func(t *testing.T) {
			module := testVERT013bMIR(t)
			mutate(&module)
			if err := VerifyVERT013bMIR(module); err == nil {
				t.Fatal("tampered MIR accepted")
			}
		})
	}
}

func TestVERT013bBoundMIRRoundTripAndTampering(t *testing.T) {
	module := testVERT013bMIR(t)
	bindings := make([]BoundCapability, len(module.LogicalCapabilityRequirements))
	for i, requirement := range module.LogicalCapabilityRequirements {
		bindings[i] = BoundCapability{LogicalName: requirement, SymbolName: "test_" + string(requirement), SignatureHash: typeKeyA}
	}
	bound, err := NewVERT013bBoundMIR(module, typeKeyB, typeKeyC, bindings)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _, err := CanonicalVERT013bBoundMIR(bound)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeVERT013bBoundMIR(encoded); err != nil {
		t.Fatal(err)
	}
	bound.Closure.Bindings[0].SymbolName = "forged"
	if err := VerifyVERT013bBoundMIR(bound); err == nil {
		t.Fatal("tampered bound MIR accepted")
	}
}
