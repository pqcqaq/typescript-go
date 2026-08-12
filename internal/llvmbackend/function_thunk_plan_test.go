package llvmbackend

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/internal/bingo"
)

func backendFunctionThunkMIR(t testing.TB, dataLayoutHash string) bingo.FunctionThunkMIRArtifact {
	t.Helper()
	graph, err := bingo.BuildTypeRelationGraph([]bingo.TypeRelationNode{{TypeKey: "type/animal", DeclarationKey: "decl/animal"}, {TypeKey: "type/dog", DeclarationKey: "decl/dog"}}, []bingo.TypeRelationEdge{{SubTypeKey: "type/dog", SuperTypeKey: "type/animal", Path: "Dog.base"}})
	if err != nil {
		t.Fatal(err)
	}
	source := bingo.FunctionThunkSignature{ParameterTypeKey: "type/animal", ReturnTypeKey: "type/dog", Effects: []bingo.FunctionThunkEffect{bingo.FunctionThunkEffectRead}, CallingConvention: bingo.FunctionThunkCallingConvention, EnvironmentABI: bingo.FunctionThunkEnvironmentABI}
	target := bingo.FunctionThunkSignature{ParameterTypeKey: "type/dog", ReturnTypeKey: "type/animal", Effects: []bingo.FunctionThunkEffect{bingo.FunctionThunkEffectRead}, CallingConvention: bingo.FunctionThunkCallingConvention, EnvironmentABI: bingo.FunctionThunkEnvironmentABI}
	contract, err := bingo.BuildFunctionThunkContract(source, target, graph)
	if err != nil {
		t.Fatal(err)
	}
	hir, err := bingo.BuildFunctionThunkHIRArtifact(strings.Repeat("1", 64), strings.Repeat("2", 64), strings.Repeat("3", 64), "node/assignment", contract)
	if err != nil {
		t.Fatal(err)
	}
	mir, err := bingo.LowerFunctionThunkMIR(hir, FirstSliceTriple, dataLayoutHash)
	if err != nil {
		t.Fatal(err)
	}
	return mir
}

func TestFunctionThunkBackendPlanRoundTripAndTampering(t *testing.T) {
	plan, err := BuildFunctionThunkBackendPlan(backendFunctionThunkMIR(t, strings.Repeat("a", 64)))
	if err != nil {
		t.Fatal(err)
	}
	encoded, _, err := CanonicalFunctionThunkBackendPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeFunctionThunkBackendPlan(encoded); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*FunctionThunkBackendPlan){
		"MIR hash":       func(v *FunctionThunkBackendPlan) { v.MIRHash = strings.Repeat("b", 64) },
		"function":       func(v *FunctionThunkBackendPlan) { v.FunctionName = "other" },
		"environment":    func(v *FunctionThunkBackendPlan) { v.EnvironmentABI = "raw-pointer" },
		"representation": func(v *FunctionThunkBackendPlan) { v.ParameterRepresentation = "ptr" },
		"runtime":        func(v *FunctionThunkBackendPlan) { v.RuntimeCalls = []string{"malloc"} },
		"identity":       func(v *FunctionThunkBackendPlan) { v.PreservesEnvironment = false },
	} {
		t.Run(name, func(t *testing.T) {
			value := plan
			mutate(&value)
			if _, _, err := CanonicalFunctionThunkBackendPlan(value); err == nil {
				t.Fatal("accepted tampered plan")
			}
		})
	}
	var raw map[string]any
	if err := json.Unmarshal(encoded, &raw); err != nil {
		t.Fatal(err)
	}
	raw["unknown"] = true
	unknown, _ := json.Marshal(raw)
	if _, err := DecodeFunctionThunkBackendPlan(unknown); err == nil {
		t.Fatal("accepted unknown member")
	}
}
