package bingo

import (
	"encoding/json"
	"strings"
	"testing"
)

func testFunctionThunkMIR(t testing.TB) FunctionThunkMIRArtifact {
	t.Helper()
	hir := functionThunkHIRFixture(t)
	module, err := LowerFunctionThunkMIR(hir, "x86_64-unknown-linux-gnu", strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	return module
}

func TestFunctionThunkMIRRoundTrip(t *testing.T) {
	module := testFunctionThunkMIR(t)
	encoded, _, err := CanonicalFunctionThunkMIR(module)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeFunctionThunkMIR(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.ContentHash != module.ContentHash || decoded.FunctionRefABI.EnvironmentFieldIndex != 1 || decoded.Instructions[1].MaySafepoint {
		t.Fatal("function thunk MIR round trip changed ABI")
	}
}

func TestFunctionThunkMIRRejectsTampering(t *testing.T) {
	tests := map[string]func(*FunctionThunkMIRArtifact){
		"HIR hash":        func(v *FunctionThunkMIRArtifact) { v.HIRHash = strings.Repeat("b", 64) },
		"code ABI":        func(v *FunctionThunkMIRArtifact) { v.FunctionRefABI.CodeRepresentation = "ptr" },
		"environment ABI": func(v *FunctionThunkMIRArtifact) { v.FunctionRefABI.EnvironmentFieldIndex = 0 },
		"parameter rep":   func(v *FunctionThunkMIRArtifact) { v.ParameterRepresentation = "ptr" },
		"operation":       func(v *FunctionThunkMIRArtifact) { v.Instructions[0].Operation = "bitcast" },
		"operand":         func(v *FunctionThunkMIRArtifact) { v.Instructions[1].Operands[0] = 1 },
		"callee":          func(v *FunctionThunkMIRArtifact) { v.Instructions[1].CalleeSignatureHash = strings.Repeat("c", 64) },
		"effect":          func(v *FunctionThunkMIRArtifact) { v.Instructions[1].Effects = nil },
		"safepoint":       func(v *FunctionThunkMIRArtifact) { v.Instructions[1].MaySafepoint = true },
		"return":          func(v *FunctionThunkMIRArtifact) { v.ReturnValueID = 3 },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			value := testFunctionThunkMIR(t)
			mutate(&value)
			if _, _, err := CanonicalFunctionThunkMIR(value); err == nil {
				t.Fatal("accepted tampered MIR")
			}
		})
	}
}

func TestFunctionThunkMIRRejectsStaleTargetIdentity(t *testing.T) {
	for name, mutate := range map[string]func(*FunctionThunkMIRArtifact){
		"target":      func(v *FunctionThunkMIRArtifact) { v.TargetTriple = "aarch64-unknown-linux-gnu" },
		"data layout": func(v *FunctionThunkMIRArtifact) { v.DataLayoutHash = strings.Repeat("b", 64) },
	} {
		t.Run(name, func(t *testing.T) {
			value := testFunctionThunkMIR(t)
			mutate(&value)
			if err := VerifyCanonicalFunctionThunkMIR(value); err == nil {
				t.Fatal("accepted stale target identity")
			}
		})
	}
}

func TestDecodeFunctionThunkMIRRejectsUnknownAndOversize(t *testing.T) {
	module := testFunctionThunkMIR(t)
	encoded, _, err := CanonicalFunctionThunkMIR(module)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(encoded, &raw); err != nil {
		t.Fatal(err)
	}
	raw["unknown"] = true
	unknown, _ := json.Marshal(raw)
	if _, err := DecodeFunctionThunkMIR(unknown); err == nil {
		t.Fatal("accepted unknown member")
	}
	if _, err := DecodeFunctionThunkMIR(make([]byte, maxFunctionThunkMIRBytes+1)); err == nil {
		t.Fatal("accepted oversized artifact")
	}
}

func FuzzDecodeFunctionThunkMIR(f *testing.F) {
	module := testFunctionThunkMIR(f)
	encoded, _, err := CanonicalFunctionThunkMIR(module)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(encoded)
	f.Fuzz(func(t *testing.T, data []byte) { _, _ = DecodeFunctionThunkMIR(data) })
}
