package bingo

import (
	"strings"
	"testing"
)

func testClassAccessHIR(t testing.TB) HIRModule {
	t.Helper()
	contract := testClassAccessContract()
	_, hash, err := CanonicalClassAccessContract(contract)
	if err != nil {
		t.Fatal(err)
	}
	contract.ContentHash = hash
	module, err := NewClassAccessHIR(testHIRProvenance(ClassAccessLogicalCapabilities()), contract)
	if err != nil {
		t.Fatal(err)
	}
	return module
}

func TestClassAccessHIRRoundTripAndOldReaderIsolation(t *testing.T) {
	module := testClassAccessHIR(t)
	if module.SchemaVersion != ClassAccessHIRSchemaVersion || len(module.ClassAccessProofs) != 4 || module.ClassAccessExecution == nil || len(module.Functions) != 5 || !module.Functions[4].Exported {
		t.Fatalf("unexpected access HIR: %#v", module)
	}
	encoded, hash, err := CanonicalClassAccessHIR(module)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeClassAccessHIR(encoded)
	if err != nil || decoded.ContentHash != hash {
		t.Fatalf("access HIR decode = %#v / %v", decoded, err)
	}
	if _, err := DecodeVERT013bDerivedHIR(encoded); err == nil {
		t.Fatal("VERT-013b reader accepted access HIR")
	}
}

func TestClassAccessHIRRejectsTampering(t *testing.T) {
	for name, mutate := range map[string]func(*HIRModule){
		"proof member":   func(m *HIRModule) { m.ClassAccessProofs[0].MemberSymbolKey = typeKeyA },
		"proof request":  func(m *HIRModule) { m.ClassAccessProofs[1].Request.ReceiverClassID = 1 },
		"proof decision": func(m *HIRModule) { m.ClassAccessProofs[2].Decision.Allowed = false },
		"contract visibility": func(m *HIRModule) {
			m.ClassAccess.Members[1].Visibility = ClassMemberPrivate
			m.ClassAccess.Members[1].PrivateIdentity = "forged"
		},
		"proof origin":           func(m *HIRModule) { m.ClassAccessProofs[3].Origin.Start++ },
		"execution initializer":  func(m *HIRModule) { m.ClassAccessExecution.Functions[0].FieldInitBits[0] = "0000000000000000" },
		"constructor allocation": func(m *HIRModule) { m.Functions[1].Blocks[0].Operations[0].Kind = "object.literal" },
		"constructor super":      func(m *HIRModule) { m.Functions[1].Blocks[0].Operations[1].Callee = 3 },
		"private load symbol": func(m *HIRModule) {
			m.Functions[2].Blocks[0].Operations[0].PropertySymbolKey = m.ClassAccess.Members[1].SymbolKey
		},
		"protected receiver": func(m *HIRModule) { m.Functions[3].Blocks[0].Operations[0].Operands[0] = 1 },
		"entry call order": func(m *HIRModule) {
			m.Functions[4].Blocks[0].Operations[1], m.Functions[4].Blocks[0].Operations[2] = m.Functions[4].Blocks[0].Operations[2], m.Functions[4].Blocks[0].Operations[1]
		},
	} {
		t.Run(name, func(t *testing.T) {
			module := testClassAccessHIR(t)
			mutate(&module)
			if err := VerifyClassAccessHIR(module); err == nil {
				t.Fatal("tampered access HIR accepted")
			}
		})
	}
	module := testClassAccessHIR(t)
	encoded, _, err := CanonicalClassAccessHIR(module)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeClassAccessHIR([]byte(strings.Replace(string(encoded), `"classAccessProofs":`, `"unknown":true,"classAccessProofs":`, 1))); err == nil {
		t.Fatal("unknown access HIR member accepted")
	}
}
