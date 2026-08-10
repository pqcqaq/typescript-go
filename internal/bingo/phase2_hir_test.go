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

func validPhase2LoopHIR() HIRModule {
	requirements := make([]RuntimeCapabilityID, 0)
	return HIRModule{
		SchemaVersion:                 HIRSchemaVersion,
		Provenance:                    testHIRProvenance(requirements),
		LogicalCapabilityRequirements: requirements,
		Functions: []HIRFunction{{
			ID: 1, Name: "compute", Exported: true, ReturnType: TypeNumber, Origin: testOrigin(0, 180),
			Parameters: []HIRParameter{
				{Name: "step", Value: 1, Type: TypeNumber, Origin: testOrigin(24, 36)},
				{Name: "limit", Value: 2, Type: TypeNumber, Origin: testOrigin(38, 51)},
			},
			Blocks: []HIRBlock{
				{ID: 1, Operations: []HIROp{}, Terminator: HIRTerminator{Kind: "branch", Successors: []BlockID{2}, Origin: testOrigin(68, 73)}},
				{ID: 2, Operations: []HIROp{
					{ID: 3, Kind: "phi", Type: TypeNumber, Operands: []ValueID{1, 5}, IncomingBlocks: []BlockID{1, 3}, Effect: EffectPure, LogicalCapabilityRequirements: []RuntimeCapabilityID{}, Origin: testOrigin(76, 81)},
					{ID: 4, Kind: "compare", Type: TypeBoolean, Operands: []ValueID{3, 2}, Operator: "<", Effect: EffectPure, LogicalCapabilityRequirements: []RuntimeCapabilityID{}, Origin: testOrigin(83, 96)},
				}, Terminator: HIRTerminator{Kind: "condbranch", Value: 4, Successors: []BlockID{3, 4}, Origin: testOrigin(68, 100)}},
				{ID: 3, Operations: []HIROp{{ID: 5, Kind: "binary", Type: TypeNumber, Operands: []ValueID{3, 1}, Operator: "+", Effect: EffectPure, LogicalCapabilityRequirements: []RuntimeCapabilityID{}, Origin: testOrigin(111, 123)}}, Terminator: HIRTerminator{Kind: "branch", Successors: []BlockID{2}, Origin: testOrigin(104, 126)}},
				{ID: 4, Operations: []HIROp{}, Terminator: HIRTerminator{Kind: "return", Value: 3, Origin: testOrigin(132, 145)}},
			},
		}},
	}
}

func validPhase2CoalesceHIR() HIRModule {
	requirements := make([]RuntimeCapabilityID, 0)
	return HIRModule{
		SchemaVersion: HIRSchemaVersion, Provenance: testHIRProvenance(requirements), LogicalCapabilityRequirements: requirements,
		Functions: []HIRFunction{{
			ID: 1, Name: "coalesce", Exported: true, ReturnType: TypeNumber, Origin: testOrigin(0, 120),
			Parameters: []HIRParameter{{Name: "value", Value: 1, Type: TypeNullableNumber, Origin: testOrigin(25, 65)}, {Name: "fallback", Value: 2, Type: TypeNumber, Origin: testOrigin(67, 83)}},
			Blocks: []HIRBlock{
				{ID: 1, Operations: []HIROp{{ID: 3, Kind: "is_nullish", Type: TypeBoolean, Operands: []ValueID{1}, Effect: EffectPure, LogicalCapabilityRequirements: []RuntimeCapabilityID{}, Origin: testOrigin(104, 121)}}, Terminator: HIRTerminator{Kind: "condbranch", Value: 3, Successors: []BlockID{2, 3}, Origin: testOrigin(104, 121)}},
				{ID: 2, Operations: []HIROp{}, Terminator: HIRTerminator{Kind: "branch", Successors: []BlockID{4}, Origin: testOrigin(113, 121)}},
				{ID: 3, Operations: []HIROp{{ID: 4, Kind: "unwrap_nullable", Type: TypeNumber, Operands: []ValueID{1}, Effect: EffectPure, LogicalCapabilityRequirements: []RuntimeCapabilityID{}, Origin: testOrigin(104, 109)}}, Terminator: HIRTerminator{Kind: "branch", Successors: []BlockID{4}, Origin: testOrigin(104, 109)}},
				{ID: 4, Operations: []HIROp{{ID: 5, Kind: "phi", Type: TypeNumber, Operands: []ValueID{2, 4}, IncomingBlocks: []BlockID{2, 3}, Effect: EffectPure, LogicalCapabilityRequirements: []RuntimeCapabilityID{}, Origin: testOrigin(104, 121)}}, Terminator: HIRTerminator{Kind: "return", Value: 5, Origin: testOrigin(97, 122)}},
			},
		}},
	}
}

func validPhase2ClassifyHIR() HIRModule {
	requirements := make([]RuntimeCapabilityID, 0)
	return HIRModule{
		SchemaVersion: HIRSchemaVersion, Provenance: testHIRProvenance(requirements), LogicalCapabilityRequirements: requirements,
		Functions: []HIRFunction{{
			ID: 1, Name: "classify", Exported: true, ReturnType: TypeNumber, Origin: testOrigin(0, 120),
			Parameters: []HIRParameter{{Name: "value", Value: 1, Type: TypeNumber, Origin: testOrigin(25, 30)}},
			Blocks: []HIRBlock{
				{ID: 1, Operations: []HIROp{{ID: 2, Kind: "number.constant", Type: TypeNumber, NumberBits: "0000000000000000", Effect: EffectPure, LogicalCapabilityRequirements: []RuntimeCapabilityID{}, Origin: testOrigin(40, 41)}, {ID: 3, Kind: "compare", Type: TypeBoolean, Operands: []ValueID{1, 2}, Operator: "<", Effect: EffectPure, LogicalCapabilityRequirements: []RuntimeCapabilityID{}, Origin: testOrigin(35, 41)}}, Terminator: HIRTerminator{Kind: "condbranch", Value: 3, Successors: []BlockID{2, 3}, Origin: testOrigin(31, 42)}},
				{ID: 2, Operations: []HIROp{{ID: 4, Kind: "number.constant", Type: TypeNumber, NumberBits: "3ff0000000000000", Effect: EffectPure, LogicalCapabilityRequirements: []RuntimeCapabilityID{}, Origin: testOrigin(50, 51)}, {ID: 5, Kind: "unary", Type: TypeNumber, Operands: []ValueID{4}, Operator: "-", Effect: EffectPure, LogicalCapabilityRequirements: []RuntimeCapabilityID{}, Origin: testOrigin(49, 51)}}, Terminator: HIRTerminator{Kind: "return", Value: 5, Origin: testOrigin(45, 52)}},
				{ID: 3, Operations: []HIROp{{ID: 6, Kind: "number.constant", Type: TypeNumber, NumberBits: "3ff0000000000000", Effect: EffectPure, LogicalCapabilityRequirements: []RuntimeCapabilityID{}, Origin: testOrigin(60, 61)}, {ID: 7, Kind: "compare", Type: TypeBoolean, Operands: []ValueID{1, 6}, Operator: "<", Effect: EffectPure, LogicalCapabilityRequirements: []RuntimeCapabilityID{}, Origin: testOrigin(55, 61)}}, Terminator: HIRTerminator{Kind: "condbranch", Value: 7, Successors: []BlockID{4, 5}, Origin: testOrigin(53, 62)}},
				{ID: 4, Operations: []HIROp{{ID: 8, Kind: "number.constant", Type: TypeNumber, NumberBits: "0000000000000000", Effect: EffectPure, LogicalCapabilityRequirements: []RuntimeCapabilityID{}, Origin: testOrigin(70, 71)}}, Terminator: HIRTerminator{Kind: "return", Value: 8, Origin: testOrigin(65, 72)}},
				{ID: 5, Operations: []HIROp{{ID: 9, Kind: "number.constant", Type: TypeNumber, NumberBits: "3ff0000000000000", Effect: EffectPure, LogicalCapabilityRequirements: []RuntimeCapabilityID{}, Origin: testOrigin(80, 81)}}, Terminator: HIRTerminator{Kind: "return", Value: 9, Origin: testOrigin(75, 82)}},
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

func TestPhase2LoopHIRIsCanonicalAndVerifiesEdgeDominance(t *testing.T) {
	module := validPhase2LoopHIR()
	_, hash, err := CanonicalPhase2HIR(module)
	if err != nil {
		t.Fatal(err)
	}
	module.ContentHash = hash
	if err := VerifyCanonicalPhase2HIR(module); err != nil {
		t.Fatal(err)
	}
	if err := VerifyHIR(module); err == nil {
		t.Fatal("loop HIR was accepted by the frozen Phase 2A verifier")
	}
}

func TestPhase2NullableCoalesceHIRIsCanonicalAndRejectsUnguardedUnwrap(t *testing.T) {
	for _, functionName := range []string{"coalesce", "coalesceAssign"} {
		t.Run(functionName, func(t *testing.T) {
			module := validPhase2CoalesceHIR()
			module.Functions[0].Name = functionName
			_, hash, err := CanonicalPhase2HIR(module)
			if err != nil {
				t.Fatal(err)
			}
			module.ContentHash = hash
			if err := VerifyCanonicalPhase2HIR(module); err != nil {
				t.Fatal(err)
			}
			if err := VerifyHIR(module); err == nil {
				t.Fatal("nullable Phase 2B HIR was accepted by the frozen Phase 2A verifier")
			}
			for _, test := range []struct {
				name   string
				mutate func(*HIRModule)
			}{
				{name: "wrong branch", mutate: func(candidate *HIRModule) { candidate.Functions[0].Blocks[0].Terminator.Successors = []BlockID{3, 2} }},
				{name: "wrong predicate", mutate: func(candidate *HIRModule) { candidate.Functions[0].Blocks[0].Operations[0].Operands[0] = 2 }},
				{name: "wrong unwrap block", mutate: func(candidate *HIRModule) { candidate.Functions[0].Blocks[2].ID = 4 }},
			} {
				t.Run(test.name, func(t *testing.T) {
					candidate := validPhase2CoalesceHIR()
					candidate.Functions[0].Name = functionName
					test.mutate(&candidate)
					if err := VerifyPhase2HIR(candidate); err == nil {
						t.Fatal("malformed nullable unwrap proof was accepted")
					}
				})
			}
		})
	}
}

func TestPhase2ClassifyHIRIsCanonicalAndRejectsLiteralOrCFGTampering(t *testing.T) {
	module := validPhase2ClassifyHIR()
	_, hash, err := CanonicalPhase2HIR(module)
	if err != nil {
		t.Fatal(err)
	}
	module.ContentHash = hash
	if err := VerifyCanonicalPhase2HIR(module); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		mutate func(*HIRModule)
		want   string
	}{
		{name: "malformed number bits", mutate: func(candidate *HIRModule) { candidate.Functions[0].Blocks[0].Operations[0].NumberBits = "3ff" }, want: "outside"},
		{name: "wrong unary operator", mutate: func(candidate *HIRModule) { candidate.Functions[0].Blocks[1].Operations[1].Operator = "+" }, want: "outside"},
		{name: "wrong second successor", mutate: func(candidate *HIRModule) { candidate.Functions[0].Blocks[2].Terminator.Successors[1] = 4 }, want: "distinct successors"},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate := validPhase2ClassifyHIR()
			test.mutate(&candidate)
			if err := VerifyPhase2HIR(candidate); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("classify HIR tamper error = %v, want %q", err, test.want)
			}
		})
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

func TestPhase2LoopHIRRejectsRehashedPhiAndComparisonTampering(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*HIRModule)
		want   string
	}{
		{name: "missing incoming block", mutate: func(module *HIRModule) { module.Functions[0].Blocks[1].Operations[0].IncomingBlocks = []BlockID{1} }, want: "outside"},
		{name: "noncanonical incoming order", mutate: func(module *HIRModule) { module.Functions[0].Blocks[1].Operations[0].IncomingBlocks = []BlockID{3, 1} }, want: "incoming blocks"},
		{name: "phi after compare", mutate: func(module *HIRModule) {
			operations := module.Functions[0].Blocks[1].Operations
			operations[0], operations[1] = operations[1], operations[0]
			operations[0].ID, operations[1].ID = 3, 4
		}, want: "appears after"},
		{name: "backedge does not dominate predecessor", mutate: func(module *HIRModule) { module.Functions[0].Blocks[1].Operations[0].Operands[0] = 5 }, want: "not dominated"},
		{name: "comparison result", mutate: func(module *HIRModule) { module.Functions[0].Blocks[1].Operations[1].Type = TypeNumber }, want: "boolean result"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			module := validPhase2LoopHIR()
			test.mutate(&module)
			if err := VerifyPhase2HIR(module); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("VerifyPhase2HIR error = %v, want %q", err, test.want)
			}
		})
	}
}
