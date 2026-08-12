package bingo

import "testing"

func testClassAccessMIR(t testing.TB) ClassAccessMIRModule {
	t.Helper()
	hir := testClassAccessHIR(t)
	module, err := LowerClassAccessMIR(hir, ClassAccessMIRTarget{TargetContextHash: typeKeyA, Triple: "x86_64-unknown-linux-gnu", DataLayoutHash: typeKeyB, LLVMDataLayoutHash: typeKeyC})
	if err != nil {
		t.Fatal(err)
	}
	return module
}

func TestClassAccessMIRRoundTripAndOldReaderIsolation(t *testing.T) {
	module := testClassAccessMIR(t)
	encoded, hash, err := CanonicalClassAccessMIR(module)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeClassAccessMIR(encoded)
	if err != nil || decoded.ContentHash != hash {
		t.Fatalf("access MIR decode = %#v / %v", decoded, err)
	}
	if _, err := DecodeVERT013bMIR(encoded); err == nil {
		t.Fatal("VERT-013b reader accepted access MIR v13")
	}
	if module.Authorizations[0].Operation != "class.field.load.authorized" || module.Authorizations[2].Operation != "class.method.call.authorized" {
		t.Fatal("access MIR did not preserve member operation kinds")
	}
	if len(module.Functions) != 5 || module.Functions[1].Instructions[0].Operation != "class.alloc" || module.Functions[4].Instructions[3].Operation != "fadd" || module.Functions[4].ReturnValue != 4 {
		t.Fatal("access MIR did not preserve the authorized execution CFG")
	}
}

func TestClassAccessMIRRejectsTampering(t *testing.T) {
	for name, mutate := range map[string]func(*ClassAccessMIRModule){
		"HIR hash":              func(m *ClassAccessMIRModule) { m.HIRHash = typeKeyC },
		"execution hash":        func(m *ClassAccessMIRModule) { m.ExecutionHash = typeKeyC },
		"execution initializer": func(m *ClassAccessMIRModule) { m.Execution.Functions[0].FieldInitBits[0] = "0000000000000000" },
		"target context":        func(m *ClassAccessMIRModule) { m.Target.TargetContextHash = "bad" },
		"triple":                func(m *ClassAccessMIRModule) { m.Target.Triple = "" },
		"data layout":           func(m *ClassAccessMIRModule) { m.Target.DataLayoutHash = "bad" },
		"LLVM data layout":      func(m *ClassAccessMIRModule) { m.Target.LLVMDataLayoutHash = "bad" },
		"operation":             func(m *ClassAccessMIRModule) { m.Authorizations[0].Operation = "class.field.load" },
		"representation":        func(m *ClassAccessMIRModule) { m.Authorizations[0].Representation = VERT013aRepGcRef },
		"member":                func(m *ClassAccessMIRModule) { m.Authorizations[1].MemberSymbolKey = typeKeyC },
		"receiver":              func(m *ClassAccessMIRModule) { m.Authorizations[1].Request.ReceiverClassID = 1 },
		"decision":              func(m *ClassAccessMIRModule) { m.Authorizations[2].Decision.Allowed = false },
		"function name":         func(m *ClassAccessMIRModule) { m.Functions[2].Name = "Other.readSecret" },
		"initializer bits":      func(m *ClassAccessMIRModule) { m.Functions[0].Instructions[0].NumberBits = "0000000000000000" },
		"allocation class":      func(m *ClassAccessMIRModule) { m.Functions[1].Instructions[0].ClassID = 1 },
		"super callee":          func(m *ClassAccessMIRModule) { m.Functions[1].Instructions[1].Callee = 3 },
		"load authorization":    func(m *ClassAccessMIRModule) { m.Functions[2].Instructions[0].AuthorizationID = 2 },
		"load receiver":         func(m *ClassAccessMIRModule) { m.Functions[3].Instructions[0].Operands[0] = 1 },
		"call callee":           func(m *ClassAccessMIRModule) { m.Functions[4].Instructions[1].MemberSymbolKey = "other" },
		"call order": func(m *ClassAccessMIRModule) {
			m.Functions[4].Instructions[1], m.Functions[4].Instructions[2] = m.Functions[4].Instructions[2], m.Functions[4].Instructions[1]
		},
		"return": func(m *ClassAccessMIRModule) { m.Functions[4].ReturnValue = 3 },
	} {
		t.Run(name, func(t *testing.T) {
			module := testClassAccessMIR(t)
			mutate(&module)
			if err := VerifyClassAccessMIR(module); err == nil {
				t.Fatal("tampered access MIR accepted")
			}
		})
	}
}
