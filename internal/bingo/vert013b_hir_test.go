package bingo

import "testing"

func testVERT013bHIR(t testing.TB) HIRModule {
	t.Helper()
	contract := testVERT013bContract(t)
	_, hash, err := CanonicalVERT013bClassContract(contract)
	if err != nil {
		t.Fatal(err)
	}
	contract.ContentHash = hash
	module, err := NewVERT013bDerivedHIR(testHIRProvenance(VERT013bLogicalCapabilities()), contract)
	if err != nil {
		t.Fatal(err)
	}
	return module
}

func TestVERT013bDerivedHIRRoundTripAndReaderIsolation(t *testing.T) {
	module := testVERT013bHIR(t)
	if got := module.Functions[0].Blocks[0].Terminator.Value; got != 1 {
		t.Fatalf("base constructor returned value %d, want complete receiver 1", got)
	}
	if got := module.Functions[1].Blocks[0].Operations[1].Operands[0]; got != 3 {
		t.Fatalf("super receiver = %d, want derived allocation 3", got)
	}
	encoded, hash, err := CanonicalVERT013bDerivedHIR(module)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeVERT013bDerivedHIR(encoded)
	if err != nil || decoded.ContentHash != hash {
		t.Fatalf("decode VERT-013b HIR = %#v / %v", decoded, err)
	}
	for name, decode := range map[string]func([]byte) error{
		"primitive": func(data []byte) error { _, err := DecodePhase2HIR(data); return err },
		"VERT-010":  func(data []byte) error { _, err := DecodeVERT010ObjectHIR(data); return err },
		"VERT-011":  func(data []byte) error { _, err := DecodeVERT011PlaceHIR(data); return err },
		"VERT-012":  func(data []byte) error { _, err := DecodeVERT012ClosureHIR(data); return err },
		"VERT-013a": func(data []byte) error { _, err := DecodeVERT013aClassHIR(data); return err },
	} {
		t.Run(name, func(t *testing.T) {
			if err := decode(encoded); err == nil {
				t.Fatal("old reader accepted HIR v13")
			}
		})
	}
}

func TestVERT013bDerivedHIRRejectsTampering(t *testing.T) {
	for name, mutate := range map[string]func(*HIRModule){
		"capability digest":         func(m *HIRModule) { m.Provenance.LogicalCapabilityRequirementsDigest = "00" },
		"function name":             func(m *HIRModule) { m.Functions[0].Name = "Other.constructor" },
		"parameter type":            func(m *HIRModule) { m.Functions[1].Parameters[1].Type = TypeBoolean },
		"super after derived init":  func(m *HIRModule) { ops := m.Functions[1].Blocks[0].Operations; ops[1], ops[3] = ops[3], ops[1] },
		"wrong super callee":        func(m *HIRModule) { m.Functions[1].Blocks[0].Operations[1].Callee = 2 },
		"different method receiver": func(m *HIRModule) { m.Functions[3].Blocks[0].Operations[2].Operands[0] = 4 },
		"operation effect": func(m *HIRModule) {
			m.Functions[2].Blocks[0].Operations[0].Effect = EffectPure
		},
		"return value": func(m *HIRModule) { m.Functions[3].Blocks[0].Terminator.Value = 5 },
		"base field substitution": func(m *HIRModule) {
			m.Functions[2].Blocks[0].Operations[0].PropertySymbolKey = m.DerivedClasses.Classes[1].Fields[0].SymbolKey
		},
	} {
		t.Run(name, func(t *testing.T) {
			module := testVERT013bHIR(t)
			mutate(&module)
			if err := VerifyVERT013bDerivedHIR(module); err == nil {
				t.Fatal("tampered HIR accepted")
			}
		})
	}
}
