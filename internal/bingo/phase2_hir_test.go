package bingo

import (
	"encoding/json"
	"fmt"
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

func TestDecodePhase2HIRRejectsNonCanonicalContracts(t *testing.T) {
	module := validPhase2ChooseHIR()
	_, hash, err := CanonicalPhase2HIR(module)
	if err != nil {
		t.Fatal(err)
	}
	module.ContentHash = hash
	canonical, _, err := CanonicalPhase2HIR(module)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodePhase2HIR(canonical)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.ContentHash != module.ContentHash {
		t.Fatalf("decoded content hash = %q, want %q", decoded.ContentHash, module.ContentHash)
	}

	unknown := append([]byte(nil), canonical[:len(canonical)-1]...)
	unknown = append(unknown, []byte(`,"unknown":true}`)...)
	oldSchema := []byte(strings.Replace(
		string(canonical),
		fmt.Sprintf(`"schemaVersion":%d`, HIRSchemaVersion),
		fmt.Sprintf(`"schemaVersion":%d`, HIRSchemaVersion-1),
		1,
	))
	tampered := module
	tampered.ContentHash = strings.Repeat("0", 64)
	tamperedBytes, err := json.Marshal(tampered)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		data    []byte
		message string
	}{
		{name: "unknown field", data: unknown, message: "unknown"},
		{name: "old schema", data: oldSchema, message: "unsupported HIR schema"},
		{name: "tampered content hash", data: tamperedBytes, message: "content hash mismatch"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := DecodePhase2HIR(test.data)
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("DecodePhase2HIR error = %v, want message containing %q", err, test.message)
			}
		})
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

func validPhase2StringLengthHIR() HIRModule {
	requirements := make([]RuntimeCapabilityID, 0)
	return HIRModule{
		SchemaVersion: HIRSchemaVersion, Provenance: testHIRProvenance(requirements), LogicalCapabilityRequirements: requirements,
		Functions: []HIRFunction{{
			ID: 1, Name: "stringLength", Exported: true, ReturnType: TypeNumber, Origin: testOrigin(0, 80),
			Parameters: []HIRParameter{{Name: "value", Value: 1, Type: TypeString, Origin: testOrigin(29, 42)}},
			Blocks: []HIRBlock{{
				ID:         1,
				Operations: []HIROp{{ID: 2, Kind: "string.length", Type: TypeNumber, Operands: []ValueID{1}, Effect: EffectPure, LogicalCapabilityRequirements: []RuntimeCapabilityID{}, Origin: testOrigin(62, 74)}},
				Terminator: HIRTerminator{Kind: "return", Value: 2, Origin: testOrigin(55, 75)},
			}},
		}},
	}
}

func validPhase2ApplicationMainHIR() HIRModule {
	requirements := make([]RuntimeCapabilityID, 0)
	return HIRModule{
		SchemaVersion: HIRSchemaVersion, Provenance: testHIRProvenance(requirements), LogicalCapabilityRequirements: requirements,
		Functions: []HIRFunction{{
			ID: 1, Name: "main", Exported: true, ReturnType: TypeNumber, Origin: testOrigin(0, 45),
			Parameters: []HIRParameter{},
			Blocks: []HIRBlock{{
				ID:         1,
				Operations: []HIROp{{ID: 1, Kind: "number.constant", Type: TypeNumber, NumberBits: "406fe00000000000", Effect: EffectPure, LogicalCapabilityRequirements: []RuntimeCapabilityID{}, Origin: testOrigin(40, 43)}},
				Terminator: HIRTerminator{Kind: "return", Value: 1, Origin: testOrigin(33, 44)},
			}},
		}},
	}
}

func TestPhase2ApplicationMainHIRIsCanonicalAndRejectsTampering(t *testing.T) {
	base := validPhase2ApplicationMainHIR()
	_, hash, err := CanonicalPhase2HIR(base)
	if err != nil {
		t.Fatal(err)
	}
	base.ContentHash = hash
	if err := VerifyCanonicalPhase2HIR(base); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		mutate func(*HIRModule)
		want   string
	}{
		{name: "parameter", mutate: func(module *HIRModule) {
			module.Functions[0].Parameters = []HIRParameter{{Name: "arg", Value: 1, Type: TypeNumber, Origin: testOrigin(20, 23)}}
			module.Functions[0].Blocks[0].Operations[0].ID = 2
			module.Functions[0].Blocks[0].Terminator.Value = 2
		}, want: "application main HIR function is invalid"},
		{name: "fractional status", mutate: func(module *HIRModule) { module.Functions[0].Blocks[0].Operations[0].NumberBits = "3fe0000000000000" }, want: "application main HIR exit status is invalid"},
		{name: "status 256", mutate: func(module *HIRModule) { module.Functions[0].Blocks[0].Operations[0].NumberBits = "4070000000000000" }, want: "application main HIR exit status is invalid"},
		{name: "wrong return", mutate: func(module *HIRModule) { module.Functions[0].Blocks[0].Terminator.Value = 0 }, want: "return terminator requires one value"},
		{name: "helper function", mutate: func(module *HIRModule) {
			helper := module.Functions[0]
			helper.ID = 1
			helper.Name = "helper"
			helper.Exported = false
			module.Functions[0].ID = 2
			module.Functions = append([]HIRFunction{helper}, module.Functions[0])
		}, want: "application main HIR must be the sole function"},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate := base
			candidate.Functions = append([]HIRFunction(nil), base.Functions...)
			candidate.Functions[0].Parameters = append([]HIRParameter(nil), base.Functions[0].Parameters...)
			candidate.Functions[0].Blocks = append([]HIRBlock(nil), base.Functions[0].Blocks...)
			candidate.Functions[0].Blocks[0].Operations = append([]HIROp(nil), base.Functions[0].Blocks[0].Operations...)
			test.mutate(&candidate)
			candidate.ContentHash = ""
			_, candidateHash, hashErr := CanonicalPhase2HIR(candidate)
			if hashErr == nil {
				candidate.ContentHash = candidateHash
				hashErr = VerifyCanonicalPhase2HIR(candidate)
			}
			if hashErr == nil || !strings.Contains(hashErr.Error(), test.want) {
				t.Fatalf("application HIR tamper error = %v, want %q", hashErr, test.want)
			}
		})
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

func TestPhase2UTF16StringLengthHIRIsCanonical(t *testing.T) {
	module := validPhase2StringLengthHIR()
	_, hash, err := CanonicalPhase2HIR(module)
	if err != nil {
		t.Fatal(err)
	}
	module.ContentHash = hash
	if err := VerifyCanonicalPhase2HIR(module); err != nil {
		t.Fatal(err)
	}
	oldMajor := module
	oldMajor.SchemaVersion = 7
	oldMajor.ContentHash = ""
	if _, _, err := CanonicalPhase2HIR(oldMajor); err == nil || !strings.Contains(err.Error(), "unsupported HIR schema 7") {
		t.Fatalf("old UTF-16 HIR major error = %v", err)
	}
	for _, mutate := range []func(*HIRModule){
		func(value *HIRModule) { value.Functions[0].Parameters[0].Type = TypeNumber },
		func(value *HIRModule) { value.Functions[0].Blocks[0].Operations[0].Operands[0] = 2 },
		func(value *HIRModule) { value.Functions[0].Blocks[0].Operations[0].Kind = "binary" },
	} {
		candidate := module
		candidate.Functions = append([]HIRFunction(nil), module.Functions...)
		candidate.Functions[0].Parameters = append([]HIRParameter(nil), module.Functions[0].Parameters...)
		candidate.Functions[0].Blocks = append([]HIRBlock(nil), module.Functions[0].Blocks...)
		candidate.Functions[0].Blocks[0].Operations = append([]HIROp(nil), module.Functions[0].Blocks[0].Operations...)
		candidate.Functions[0].Blocks[0].Operations[0].Operands = append([]ValueID(nil), module.Functions[0].Blocks[0].Operations[0].Operands...)
		mutate(&candidate)
		candidate.ContentHash = ""
		if _, _, err := CanonicalPhase2HIR(candidate); err == nil {
			t.Fatal("tampered UTF-16 string length HIR was accepted")
		}
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
