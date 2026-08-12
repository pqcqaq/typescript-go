package bingo

import "testing"

func testVERT012HIR(t testing.TB) HIRModule {
	t.Helper()
	contract := testClosureContract()
	_, hash, err := CanonicalClosureContract(contract)
	if err != nil {
		t.Fatal(err)
	}
	contract.ContentHash = hash
	module, err := NewVERT012ClosureHIR(testHIRProvenance(VERT012LogicalCapabilities()), contract)
	if err != nil {
		t.Fatal(err)
	}
	return module
}

func TestVERT012ClosureHIRRoundTrip(t *testing.T) {
	module := testVERT012HIR(t)
	encoded, hash, err := CanonicalVERT012ClosureHIR(module)
	if err != nil {
		t.Fatal(err)
	}
	if module.ContentHash != hash {
		t.Fatal("constructor did not bind HIR hash")
	}
	decoded, err := DecodeVERT012ClosureHIR(encoded)
	if err != nil || decoded.ContentHash != hash {
		t.Fatalf("decode VERT-012 HIR = %#v / %v", decoded, err)
	}
	if _, err := DecodePhase2HIR(encoded); err == nil {
		t.Fatal("primitive HIR reader accepted VERT-012")
	}
	if _, err := DecodeVERT010ObjectHIR(encoded); err == nil {
		t.Fatal("VERT-010 reader accepted VERT-012")
	}
	if _, err := DecodeVERT011PlaceHIR(encoded); err == nil {
		t.Fatal("VERT-011 reader accepted VERT-012")
	}
}

func TestVERT012ClosureHIRRejectsTampering(t *testing.T) {
	for name, edit := range map[string]func(*HIRModule){
		"schema":                        func(m *HIRModule) { m.SchemaVersion-- },
		"copied environment":            func(m *HIRModule) { m.Functions[1].Blocks[0].Operations[2].Operands[0] = 3 },
		"direct call":                   func(m *HIRModule) { m.Functions[1].Blocks[0].Operations[3].Kind = "call.direct" },
		"different closure second call": func(m *HIRModule) { m.Functions[1].Blocks[0].Operations[4].Operands[0] = 3 },
		"store before load": func(m *HIRModule) {
			m.Functions[0].Blocks[0].Operations[0], m.Functions[0].Blocks[0].Operations[3] = m.Functions[0].Blocks[0].Operations[3], m.Functions[0].Blocks[0].Operations[0]
		},
		"missing alloc capability": func(m *HIRModule) { m.Functions[1].Blocks[0].Operations[0].LogicalCapabilityRequirements = nil },
	} {
		t.Run(name, func(t *testing.T) {
			module := testVERT012HIR(t)
			edit(&module)
			if err := VerifyVERT012ClosureHIR(module); err == nil {
				t.Fatal("tampered VERT-012 HIR accepted")
			}
		})
	}
}
