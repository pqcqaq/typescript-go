package bingo

import (
	"bytes"
	"strings"
	"testing"
)

func functionThunkFixture(t testing.TB) FunctionThunkContract {
	t.Helper()
	graph, err := BuildTypeRelationGraph(
		[]TypeRelationNode{
			{TypeKey: "type/animal", DeclarationKey: "decl/animal"},
			{TypeKey: "type/dog", DeclarationKey: "decl/dog"},
		},
		[]TypeRelationEdge{{SubTypeKey: "type/dog", SuperTypeKey: "type/animal", Path: "Dog.base"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	source := FunctionThunkSignature{ParameterTypeKey: "type/animal", ReturnTypeKey: "type/dog", Effects: []FunctionThunkEffect{FunctionThunkEffectRead}, CallingConvention: FunctionThunkCallingConvention, EnvironmentABI: FunctionThunkEnvironmentABI}
	target := FunctionThunkSignature{ParameterTypeKey: "type/dog", ReturnTypeKey: "type/animal", Effects: []FunctionThunkEffect{FunctionThunkEffectRead, FunctionThunkEffectThrow}, CallingConvention: FunctionThunkCallingConvention, EnvironmentABI: FunctionThunkEnvironmentABI}
	contract, err := BuildFunctionThunkContract(source, target, graph)
	if err != nil {
		t.Fatal(err)
	}
	return contract
}

func TestFunctionThunkContractBindsContravariantParameterAndCovariantReturn(t *testing.T) {
	contract := functionThunkFixture(t)
	if !contract.PreservesEnvironment || !slicesEqualStrings(contract.ParameterPath, []string{"type/dog", "type/animal"}) || !slicesEqualStrings(contract.ReturnPath, []string{"type/dog", "type/animal"}) {
		t.Fatalf("unexpected thunk proof: %#v", contract)
	}
	encoded, _, err := CanonicalFunctionThunkContract(contract)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeFunctionThunkContract(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.ContentHash != contract.ContentHash {
		t.Fatal("function thunk round trip changed identity")
	}
}

func TestFunctionThunkContractRejectsSubstitution(t *testing.T) {
	base := functionThunkFixture(t)
	for name, mutate := range map[string]func(*FunctionThunkContract){
		"parameter direction": func(value *FunctionThunkContract) {
			value.Source.ParameterTypeKey, value.Target.ParameterTypeKey = value.Target.ParameterTypeKey, value.Source.ParameterTypeKey
		},
		"return direction": func(value *FunctionThunkContract) {
			value.Source.ReturnTypeKey, value.Target.ReturnTypeKey = value.Target.ReturnTypeKey, value.Source.ReturnTypeKey
		},
		"parameter path": func(value *FunctionThunkContract) { value.ParameterPath = []string{"type/animal"} },
		"return path":    func(value *FunctionThunkContract) { value.ReturnPath = []string{"type/animal"} },
		"source effect": func(value *FunctionThunkContract) {
			value.Source.Effects = []FunctionThunkEffect{FunctionThunkEffectWrite}
		},
		"effect order": func(value *FunctionThunkContract) {
			value.Target.Effects = []FunctionThunkEffect{FunctionThunkEffectThrow, FunctionThunkEffectRead}
		},
		"calling convention":   func(value *FunctionThunkContract) { value.Target.CallingConvention = "cdecl" },
		"environment ABI":      func(value *FunctionThunkContract) { value.Source.EnvironmentABI = "raw-ptr" },
		"allocate":             func(value *FunctionThunkContract) { value.Allocates = true },
		"copy":                 func(value *FunctionThunkContract) { value.Copies = true },
		"runtime check":        func(value *FunctionThunkContract) { value.RuntimeChecks = true },
		"suspend":              func(value *FunctionThunkContract) { value.MaySuspend = true },
		"host entry":           func(value *FunctionThunkContract) { value.MayEnterHost = true },
		"environment identity": func(value *FunctionThunkContract) { value.PreservesEnvironment = false },
		"relation":             func(value *FunctionThunkContract) { value.Relations.Edges = nil },
	} {
		t.Run(name, func(t *testing.T) {
			value := base
			value.ParameterPath = append([]string(nil), base.ParameterPath...)
			value.ReturnPath = append([]string(nil), base.ReturnPath...)
			value.Source.Effects = append([]FunctionThunkEffect(nil), base.Source.Effects...)
			value.Target.Effects = append([]FunctionThunkEffect(nil), base.Target.Effects...)
			value.Relations.Nodes = append([]TypeRelationNode(nil), base.Relations.Nodes...)
			value.Relations.Edges = append([]TypeRelationEdge(nil), base.Relations.Edges...)
			mutate(&value)
			value.ContentHash = ""
			if _, _, err := CanonicalFunctionThunkContract(value); err == nil {
				t.Fatal("function thunk accepted substituted proof")
			}
		})
	}
}

func TestDecodeFunctionThunkContractRejectsStrictAndOversizedInput(t *testing.T) {
	contract := functionThunkFixture(t)
	encoded, _, err := CanonicalFunctionThunkContract(contract)
	if err != nil {
		t.Fatal(err)
	}
	unknown := bytes.Replace(encoded, []byte(`"schemaVersion":1`), []byte(`"schemaVersion":1,"unknown":true`), 1)
	if _, err := DecodeFunctionThunkContract(unknown); err == nil {
		t.Fatal("function thunk accepted unknown member")
	}
	stale := bytes.Replace(encoded, []byte(contract.ContentHash), []byte(strings.Repeat("a", 64)), 1)
	if _, err := DecodeFunctionThunkContract(stale); err == nil {
		t.Fatal("function thunk accepted stale hash")
	}
	if _, err := DecodeFunctionThunkContract(make([]byte, maxFunctionThunkBytes+1)); err == nil {
		t.Fatal("function thunk accepted oversized input")
	}
}

func slicesEqualStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func FuzzDecodeFunctionThunkContract(f *testing.F) {
	contract := functionThunkFixture(f)
	encoded, _, err := CanonicalFunctionThunkContract(contract)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(encoded)
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = DecodeFunctionThunkContract(data)
	})
}
