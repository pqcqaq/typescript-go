package bingo

import "testing"

func testVERT013aHIR(t testing.TB) HIRModule {
	t.Helper()
	contract := testClassContract()
	_, hash, err := CanonicalClassContract(contract)
	if err != nil {
		t.Fatal(err)
	}
	contract.ContentHash = hash
	module, err := NewVERT013aClassHIR(testHIRProvenance(VERT013aLogicalCapabilities()), contract)
	if err != nil {
		t.Fatal(err)
	}
	return module
}

func TestVERT013aClassHIRRoundTripAndReaderIsolation(t *testing.T) {
	module := testVERT013aHIR(t)
	encoded, hash, err := CanonicalVERT013aClassHIR(module)
	if err != nil {
		t.Fatal(err)
	}
	if module.ContentHash != hash {
		t.Fatal("constructor did not bind HIR hash")
	}
	decoded, err := DecodeVERT013aClassHIR(encoded)
	if err != nil || decoded.ContentHash != hash {
		t.Fatalf("decode VERT-013a HIR = %#v / %v", decoded, err)
	}
	for name, decode := range map[string]func([]byte) error{
		"primitive": func(data []byte) error { _, err := DecodePhase2HIR(data); return err },
		"VERT-010":  func(data []byte) error { _, err := DecodeVERT010ObjectHIR(data); return err },
		"VERT-011":  func(data []byte) error { _, err := DecodeVERT011PlaceHIR(data); return err },
		"VERT-012":  func(data []byte) error { _, err := DecodeVERT012ClosureHIR(data); return err },
	} {
		t.Run(name, func(t *testing.T) {
			if err := decode(encoded); err == nil {
				t.Fatal("old reader accepted VERT-013a")
			}
		})
	}
}

func TestVERT013aClassHIRRejectsTampering(t *testing.T) {
	for name, edit := range map[string]func(*HIRModule){
		"schema": func(m *HIRModule) { m.SchemaVersion-- },
		"allocation after init": func(m *HIRModule) {
			m.Functions[0].Blocks[0].Operations[0], m.Functions[0].Blocks[0].Operations[2] = m.Functions[0].Blocks[0].Operations[2], m.Functions[0].Blocks[0].Operations[0]
		},
		"missing field initializer": func(m *HIRModule) {
			m.Functions[0].Blocks[0].Operations = append(m.Functions[0].Blocks[0].Operations[:1], m.Functions[0].Blocks[0].Operations[2:]...)
		},
		"different receiver":            func(m *HIRModule) { m.Functions[2].Blocks[0].Operations[2].Operands[0] = 3 },
		"method extracted":              func(m *HIRModule) { m.Functions[2].Blocks[0].Operations[1].Kind = "call.indirect" },
		"wrong field identity":          func(m *HIRModule) { m.Functions[1].Blocks[0].Operations[0].PropertySymbolKey = "field:other" },
		"missing allocation capability": func(m *HIRModule) { m.Functions[0].Blocks[0].Operations[0].LogicalCapabilityRequirements = nil },
	} {
		t.Run(name, func(t *testing.T) {
			module := testVERT013aHIR(t)
			edit(&module)
			if err := VerifyVERT013aClassHIR(module); err == nil {
				t.Fatal("tampered VERT-013a HIR accepted")
			}
		})
	}
}
