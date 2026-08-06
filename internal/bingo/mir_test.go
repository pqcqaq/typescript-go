package bingo

import "testing"

func TestVerifyMIRV1RemainsIndependentFromHIRV2FirstSlice(t *testing.T) {
	origin := func(start, end int) Origin {
		return Origin{File: "legacy-mir.ts", Start: start, End: end}
	}
	module := MIRModule{
		SchemaVersion: MIRSchemaVersion,
		Functions: []MIRFunction{{
			ID:         9,
			Name:       "choose",
			ReturnType: TypeNumber,
			Origin:     origin(0, 80),
			Parameters: []MIRParameter{
				{Name: "condition", Value: 10, Type: TypeBoolean, Origin: origin(16, 25)},
				{Name: "value", Value: 20, Type: TypeNumber, Origin: origin(27, 32)},
			},
			Blocks: []MIRBlock{
				{
					ID:         10,
					Terminator: MIRTerminator{Kind: "condbranch", Value: 10, Successors: []BlockID{20, 30}, Origin: origin(40, 49)},
				},
				{
					ID:         20,
					Terminator: MIRTerminator{Kind: "branch", Successors: []BlockID{40}, Origin: origin(50, 55)},
				},
				{
					ID:         30,
					Terminator: MIRTerminator{Kind: "branch", Successors: []BlockID{40}, Origin: origin(56, 61)},
				},
				{
					ID:         40,
					Terminator: MIRTerminator{Kind: "return", Value: 20, Origin: origin(62, 74)},
				},
			},
		}},
	}

	if err := VerifyMIR(module); err != nil {
		t.Fatalf("MIR v1 general CFG was coupled to the HIR v2 first-slice verifier: %v", err)
	}
}
