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
			ID: 1, Name: "choose", ReturnType: TypeNumber, Origin: testOrigin(0, 120),
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
