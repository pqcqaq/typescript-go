package bingo

import (
	"strings"
	"testing"
)

func validPhase2ChooseHIR() HIRModule {
	requirements := make([]RuntimeCapabilityID, 0)
	return HIRModule{
		SchemaVersion:                 HIRSchemaVersion,
		Provenance:                    testHIRProvenance(requirements),
		LogicalCapabilityRequirements: requirements,
		Functions: []HIRFunction{{
			ID: 1, Name: "choose", Exported: true, ReturnType: TypeNumber, Origin: testOrigin(0, 120),
			Parameters: []HIRParameter{
				{Name: "flag", Value: 1, Type: TypeBoolean, Origin: testOrigin(16, 29)},
				{Name: "left", Value: 2, Type: TypeNumber, Origin: testOrigin(31, 43)},
				{Name: "right", Value: 3, Type: TypeNumber, Origin: testOrigin(45, 58)},
			},
			Blocks: []HIRBlock{
				{ID: 1, Operations: []HIROp{}, Terminator: HIRTerminator{Kind: "condbranch", Value: 1, Successors: []BlockID{2, 3}, Origin: testOrigin(70, 78)}},
				{ID: 2, Operations: []HIROp{}, Terminator: HIRTerminator{Kind: "return", Value: 2, Origin: testOrigin(80, 92)}},
				{ID: 3, Operations: []HIROp{}, Terminator: HIRTerminator{Kind: "return", Value: 3, Origin: testOrigin(94, 108)}},
			},
		}},
	}
}

func validPhase2LocalCallHIR() HIRModule {
	requirements := make([]RuntimeCapabilityID, 0)
	return HIRModule{
		SchemaVersion:                 HIRSchemaVersion,
		Provenance:                    testHIRProvenance(requirements),
		LogicalCapabilityRequirements: requirements,
		Functions: []HIRFunction{
			{
				ID: 1, Name: "add", ReturnType: TypeNumber, Origin: testOrigin(0, 78),
				Parameters: []HIRParameter{
					{Name: "left", Value: 1, Type: TypeNumber, Origin: testOrigin(13, 25)},
					{Name: "right", Value: 2, Type: TypeNumber, Origin: testOrigin(27, 40)},
				},
				Blocks: []HIRBlock{{
					ID:         1,
					Operations: []HIROp{{ID: 3, Kind: "binary", Type: TypeNumber, Operands: []ValueID{1, 2}, Operator: "+", Effect: EffectPure, LogicalCapabilityRequirements: []RuntimeCapabilityID{}, Origin: testOrigin(61, 73)}},
					Terminator: HIRTerminator{Kind: "return", Value: 3, Origin: testOrigin(54, 74)},
				}},
			},
			{
				ID: 2, Name: "compute", Exported: true, ReturnType: TypeNumber, Origin: testOrigin(80, 220),
				Parameters: []HIRParameter{
					{Name: "left", Value: 1, Type: TypeNumber, Origin: testOrigin(104, 116)},
					{Name: "right", Value: 2, Type: TypeNumber, Origin: testOrigin(118, 131)},
				},
				Blocks: []HIRBlock{{
					ID: 1,
					Operations: []HIROp{
						{ID: 3, Kind: "call", Type: TypeNumber, Operands: []ValueID{1, 2}, Callee: 1, Effect: EffectCall, LogicalCapabilityRequirements: []RuntimeCapabilityID{}, Origin: testOrigin(154, 171)},
						{ID: 4, Kind: "binary", Type: TypeNumber, Operands: []ValueID{3, 2}, Operator: "+", Effect: EffectPure, LogicalCapabilityRequirements: []RuntimeCapabilityID{}, Origin: testOrigin(182, 196)},
					},
					Terminator: HIRTerminator{Kind: "return", Value: 4, Origin: testOrigin(200, 213)},
				}},
			},
		},
	}
}

func TestPhase2ChooseHIRIsCanonicalAndKeepsPhase2AFrozen(t *testing.T) {
	module := validPhase2ChooseHIR()
	encoded, hash, err := CanonicalPhase2HIR(module)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) == 0 || len(hash) != 64 {
		t.Fatalf("canonical Phase 2B HIR = %d bytes / %q", len(encoded), hash)
	}
	module.ContentHash = hash
	if err := VerifyCanonicalPhase2HIR(module); err != nil {
		t.Fatal(err)
	}
	if err := VerifyHIR(module); err == nil {
		t.Fatal("Phase 2B choose HIR was accepted by the frozen Phase 2A verifier")
	}
}

func TestPhase2LocalAssignmentAndDirectCallHIRAreCanonical(t *testing.T) {
	module := validPhase2LocalCallHIR()
	_, hash, err := CanonicalPhase2HIR(module)
	if err != nil {
		t.Fatal(err)
	}
	module.ContentHash = hash
	if err := VerifyCanonicalPhase2HIR(module); err != nil {
		t.Fatal(err)
	}
	if err := VerifyHIR(module); err == nil {
		t.Fatal("multi-function Phase 2B HIR was accepted by the frozen Phase 2A verifier")
	}
}

func TestPhase2DirectCallHIRRejectsRehashedSignatureAndTargetTampering(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*HIRModule)
		want   string
	}{
		{name: "sparse function ID", mutate: func(module *HIRModule) { module.Functions[1].ID = 3 }, want: "canonical dense ID"},
		{name: "duplicate function name", mutate: func(module *HIRModule) { module.Functions[1].Name = "add" }, want: "duplicated"},
		{name: "missing callee", mutate: func(module *HIRModule) { module.Functions[1].Blocks[0].Operations[0].Callee = 9 }, want: "targets missing function"},
		{name: "recursive callee", mutate: func(module *HIRModule) { module.Functions[1].Blocks[0].Operations[0].Callee = 2 }, want: "earlier non-recursive"},
		{name: "argument type", mutate: func(module *HIRModule) { module.Functions[1].Parameters[0].Type = TypeBoolean }, want: "argument 0 has type"},
		{name: "result type", mutate: func(module *HIRModule) { module.Functions[1].Blocks[0].Operations[0].Type = TypeBoolean }, want: "result type"},
		{name: "call effect", mutate: func(module *HIRModule) { module.Functions[1].Blocks[0].Operations[0].Effect = EffectPure }, want: "outside"},
		{name: "two exports", mutate: func(module *HIRModule) { module.Functions[0].Exported = true }, want: "exactly one exported"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			module := validPhase2LocalCallHIR()
			test.mutate(&module)
			if err := VerifyPhase2HIR(module); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("VerifyPhase2HIR error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestPhase2ChooseHIRRejectsMalformedCFGAndTypes(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*HIRModule)
		want   string
	}{
		{name: "non boolean condition", mutate: func(module *HIRModule) { module.Functions[0].Parameters[0].Type = TypeNumber }, want: "conditional branch value"},
		{name: "duplicate successor", mutate: func(module *HIRModule) { module.Functions[0].Blocks[0].Terminator.Successors[1] = 2 }, want: "distinct successors"},
		{name: "missing successor", mutate: func(module *HIRModule) { module.Functions[0].Blocks[0].Terminator.Successors[1] = 9 }, want: "targets missing block"},
		{name: "unreachable block", mutate: func(module *HIRModule) {
			module.Functions[0].Blocks[0].Terminator.Kind = "branch"
			module.Functions[0].Blocks[0].Terminator.Value = 0
			module.Functions[0].Blocks[0].Terminator.Successors = []BlockID{2}
		}, want: "block 3 is unreachable"},
		{name: "wrong return type", mutate: func(module *HIRModule) { module.Functions[0].Blocks[1].Terminator.Value = 1 }, want: "return value 1 has type"},
		{name: "missing operation slice", mutate: func(module *HIRModule) { module.Functions[0].Blocks[2].Operations = nil }, want: "operations are missing"},
		{name: "sparse block", mutate: func(module *HIRModule) { module.Functions[0].Blocks[2].ID = 4 }, want: "canonical dense ID"},
		{name: "unsupported terminator", mutate: func(module *HIRModule) { module.Functions[0].Blocks[0].Terminator.Kind = "switch" }, want: "outside the Phase 2B primitive CFG subset"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			module := validPhase2ChooseHIR()
			test.mutate(&module)
			if err := VerifyPhase2HIR(module); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("VerifyPhase2HIR error = %v, want %q", err, test.want)
			}
		})
	}
}
