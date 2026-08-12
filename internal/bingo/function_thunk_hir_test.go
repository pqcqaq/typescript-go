package bingo

import (
	"bytes"
	"slices"
	"strings"
	"testing"
)

func functionThunkHIRFixture(t testing.TB) FunctionThunkHIRArtifact {
	t.Helper()
	artifact, err := BuildFunctionThunkHIRArtifact(strings.Repeat("1", 64), strings.Repeat("2", 64), strings.Repeat("3", 64), "node/assignment", functionThunkFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	return artifact
}

func TestFunctionThunkHIRMaterializesExplicitWrapper(t *testing.T) {
	artifact := functionThunkHIRFixture(t)
	if artifact.ReturnValueID != 4 || len(artifact.Operations) != 3 || artifact.Operations[0].Kind != "function.thunk.parameter.convert" || artifact.Operations[1].Kind != "function.thunk.source.call" || artifact.Operations[2].Kind != "function.thunk.return.convert" || len(artifact.Operations[0].Effects) != 0 || len(artifact.Operations[2].Effects) != 0 || !slices.Equal(artifact.Operations[1].Effects, artifact.Thunk.Source.Effects) {
		t.Fatalf("unexpected function thunk HIR: %#v", artifact)
	}
	encoded, _, err := CanonicalFunctionThunkHIRArtifact(artifact)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeFunctionThunkHIRArtifact(encoded); err != nil {
		t.Fatal(err)
	}
}

func TestFunctionThunkHIRRejectsSubstitution(t *testing.T) {
	base := functionThunkHIRFixture(t)
	for name, mutate := range map[string]func(*FunctionThunkHIRArtifact){
		"source signature":         func(value *FunctionThunkHIRArtifact) { value.SourceSignatureHash = strings.Repeat("4", 64) },
		"invalid frontend":         func(value *FunctionThunkHIRArtifact) { value.FrontendSnapshotHash = "not-a-hash" },
		"invalid target signature": func(value *FunctionThunkHIRArtifact) { value.TargetSignatureHash = "not-a-hash" },
		"missing assignment":       func(value *FunctionThunkHIRArtifact) { value.AssignmentNodeID = "" },
		"function":                 func(value *FunctionThunkHIRArtifact) { value.FunctionID = 2 },
		"parameter":                func(value *FunctionThunkHIRArtifact) { value.ParameterValueID = 2 },
		"return":                   func(value *FunctionThunkHIRArtifact) { value.ReturnValueID = 3 },
		"environment":              func(value *FunctionThunkHIRArtifact) { value.PreservesEnvironment = false },
		"operation order": func(value *FunctionThunkHIRArtifact) {
			value.Operations[0], value.Operations[1] = value.Operations[1], value.Operations[0]
		},
		"operand": func(value *FunctionThunkHIRArtifact) { value.Operations[1].Operands[0] = 1 },
		"type direction": func(value *FunctionThunkHIRArtifact) {
			value.Operations[0].SourceTypeKey, value.Operations[0].TargetTypeKey = value.Operations[0].TargetTypeKey, value.Operations[0].SourceTypeKey
		},
		"relation": func(value *FunctionThunkHIRArtifact) { value.Operations[0].RelationPath = []string{"type/animal"} },
		"callee": func(value *FunctionThunkHIRArtifact) {
			value.Operations[1].CalleeSignatureHash = strings.Repeat("5", 64)
		},
		"effects": func(value *FunctionThunkHIRArtifact) {
			value.Operations[0].Effects = []FunctionThunkEffect{FunctionThunkEffectRead}
		},
	} {
		t.Run(name, func(t *testing.T) {
			value := base
			value.Operations = append([]FunctionThunkHIROperation(nil), base.Operations...)
			for i := range value.Operations {
				value.Operations[i].Operands = slices.Clone(base.Operations[i].Operands)
				value.Operations[i].RelationPath = slices.Clone(base.Operations[i].RelationPath)
				value.Operations[i].Effects = slices.Clone(base.Operations[i].Effects)
			}
			mutate(&value)
			value.ContentHash = ""
			if _, _, err := CanonicalFunctionThunkHIRArtifact(value); err == nil {
				t.Fatal("function thunk HIR accepted substitution")
			}
		})
	}
}

func TestDecodeFunctionThunkHIRRejectsUnknownStaleAndOversized(t *testing.T) {
	artifact := functionThunkHIRFixture(t)
	encoded, _, err := CanonicalFunctionThunkHIRArtifact(artifact)
	if err != nil {
		t.Fatal(err)
	}
	unknown := bytes.Replace(encoded, []byte(`"schemaVersion":1`), []byte(`"schemaVersion":1,"unknown":true`), 1)
	if _, err := DecodeFunctionThunkHIRArtifact(unknown); err == nil {
		t.Fatal("function thunk HIR accepted unknown member")
	}
	stale := bytes.Replace(encoded, []byte(artifact.ContentHash), []byte(strings.Repeat("a", 64)), 1)
	if _, err := DecodeFunctionThunkHIRArtifact(stale); err == nil {
		t.Fatal("function thunk HIR accepted stale hash")
	}
	if _, err := DecodeFunctionThunkHIRArtifact(make([]byte, maxFunctionThunkHIRBytes+1)); err == nil {
		t.Fatal("function thunk HIR accepted oversized input")
	}
}

func FuzzDecodeFunctionThunkHIRArtifact(f *testing.F) {
	artifact := functionThunkHIRFixture(f)
	encoded, _, err := CanonicalFunctionThunkHIRArtifact(artifact)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(encoded)
	f.Fuzz(func(t *testing.T, data []byte) { _, _ = DecodeFunctionThunkHIRArtifact(data) })
}
