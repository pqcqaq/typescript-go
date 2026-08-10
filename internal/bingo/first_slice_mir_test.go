package bingo

import (
	"bytes"
	"fmt"
	"slices"
	"strings"
	"testing"
)

func TestFirstSliceRepresentationAndMIRAreCanonicalAndTargetAware(t *testing.T) {
	hir, plan := firstSliceMIRFixture(t)
	if _, err := plan.CanonicalBytes(); err != nil {
		t.Fatal(err)
	}

	structural, err := LowerFirstSliceMIR(hir, plan)
	if err != nil {
		t.Fatal(err)
	}
	if structural.BoundCapabilityClosure != nil {
		t.Fatal("structural MIR bound capabilities before the binding pass")
	}
	structuralBytes, err := structural.CanonicalStructuralBytes()
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(structuralBytes, []byte(`"successors"`)) {
		t.Fatalf("Phase 2A add MIR bytes changed after CFG successor support: %s", structuralBytes)
	}
	function := structural.Functions[0]
	if function.ReturnType != RepF64 || function.Blocks[0].Instructions[0].Kind != "fadd" {
		t.Fatalf("target-aware MIR = %#v", structural)
	}

	bound, err := BindFirstSliceCapabilities(structural)
	if err != nil {
		t.Fatal(err)
	}
	if bound.BoundCapabilityClosure == nil || bound.BoundCapabilityClosure.Bindings == nil || len(bound.BoundCapabilityClosure.Bindings) != 0 {
		t.Fatalf("bound capability closure is not explicit and empty: %#v", bound.BoundCapabilityClosure)
	}
	encoded, err := bound.CanonicalBoundBytes()
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeBoundFirstSliceMIR(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.ContentHash != bound.ContentHash {
		t.Fatalf("bound MIR hash = %s, want %s", decoded.ContentHash, bound.ContentHash)
	}
}

func TestPrimitiveRepresentationBindingsSeparateBooleanAndNumber(t *testing.T) {
	boolean, err := PrimitiveRepresentationBinding(TypeBoolean)
	if err != nil {
		t.Fatal(err)
	}
	if boolean != (RepresentationBinding{SourceType: TypeBoolean, RepType: RepI1, BitWidth: 1, ABIAlign: 1}) {
		t.Fatalf("boolean representation binding = %#v", boolean)
	}
	number, err := PrimitiveRepresentationBinding(TypeNumber)
	if err != nil {
		t.Fatal(err)
	}
	if number != (RepresentationBinding{SourceType: TypeNumber, RepType: RepF64, BitWidth: 64, ABIAlign: 8}) {
		t.Fatalf("number representation binding = %#v", number)
	}
	if _, err := PrimitiveRepresentationBinding(TypeString); err == nil || !strings.Contains(err.Error(), "no representation binding") {
		t.Fatalf("unsupported representation error = %v", err)
	}
	nullable, err := PrimitiveRepresentationBinding(TypeNullableNumber)
	if err != nil {
		t.Fatal(err)
	}
	if nullable != (RepresentationBinding{SourceType: TypeNullableNumber, RepType: RepNullableF64, BitWidth: 128, ABIAlign: 8}) {
		t.Fatalf("nullable-number representation binding = %#v", nullable)
	}
}

func TestPhase2ChooseRepresentationAndMIRAreCanonical(t *testing.T) {
	hir := validPhase2ChooseHIR()
	_, hirHash, err := CanonicalPhase2HIR(hir)
	if err != nil {
		t.Fatal(err)
	}
	hir.ContentHash = hirHash
	plan, err := NewRepresentationPlanForHIR(phase2ChooseProvenance(hir), hir)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Bindings) != 2 || plan.Bindings[0].SourceType != TypeBoolean || plan.Bindings[0].RepType != RepI1 || plan.Bindings[1].SourceType != TypeNumber || plan.Bindings[1].RepType != RepF64 {
		t.Fatalf("choose representation plan = %#v", plan.Bindings)
	}
	structural, err := LowerFirstSliceMIR(hir, plan)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyStructuralFirstSliceMIR(structural); err != nil {
		t.Fatal(err)
	}
	function := structural.Functions[0]
	if function.ReturnType != RepF64 || len(function.Parameters) != 3 || function.Parameters[0].Type != RepI1 || function.Parameters[1].Type != RepF64 || function.Parameters[2].Type != RepF64 || len(function.Blocks) != 3 {
		t.Fatalf("choose target MIR = %#v", function)
	}
	if entry := function.Blocks[0].Terminator; entry.Kind != "condbranch" || entry.Value != 1 || !slices.Equal(entry.Successors, []BlockID{2, 3}) {
		t.Fatalf("choose MIR entry = %#v", function.Blocks[0].Terminator)
	}
	if function.Blocks[1].Terminator.Value != 2 || function.Blocks[2].Terminator.Value != 3 {
		t.Fatalf("choose MIR returns = %#v / %#v", function.Blocks[1].Terminator, function.Blocks[2].Terminator)
	}
	bound, err := BindFirstSliceCapabilities(structural)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyBoundFirstSliceMIR(bound); err != nil {
		t.Fatal(err)
	}
}

func TestPhase2LocalAssignmentAndDirectCallMIRAreCanonical(t *testing.T) {
	hir := validPhase2LocalCallHIR()
	_, hirHash, err := CanonicalPhase2HIR(hir)
	if err != nil {
		t.Fatal(err)
	}
	hir.ContentHash = hirHash
	plan, err := NewRepresentationPlanForHIR(phase2ChooseProvenance(hir), hir)
	if err != nil {
		t.Fatal(err)
	}
	structural, err := LowerFirstSliceMIR(hir, plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(structural.Functions) != 2 || structural.Functions[0].Exported || !structural.Functions[1].Exported {
		t.Fatalf("local-call MIR visibility = %#v", structural.Functions)
	}
	call := structural.Functions[1].Blocks[0].Instructions[0]
	if call.Kind != "call" || call.Callee != 1 || call.Effect != EffectCall || !slices.Equal(call.Operands, []ValueID{1, 2}) {
		t.Fatalf("direct call MIR = %#v", call)
	}
	bound, err := BindFirstSliceCapabilities(structural)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyBoundFirstSliceMIR(bound); err != nil {
		t.Fatal(err)
	}
}

func TestPhase2LoopRepresentationAndMIRAreCanonical(t *testing.T) {
	hir := validPhase2LoopHIR()
	_, hirHash, err := CanonicalPhase2HIR(hir)
	if err != nil {
		t.Fatal(err)
	}
	hir.ContentHash = hirHash
	plan, err := NewRepresentationPlanForHIR(phase2ChooseProvenance(hir), hir)
	if err != nil {
		t.Fatal(err)
	}
	structural, err := LowerFirstSliceMIR(hir, plan)
	if err != nil {
		t.Fatal(err)
	}
	function := structural.Functions[0]
	if len(function.Blocks) != 4 || function.Blocks[1].Instructions[0].Kind != "phi" || function.Blocks[1].Instructions[1].Kind != "fcmp.olt" || !slices.Equal(function.Blocks[1].Instructions[0].IncomingBlocks, []BlockID{1, 3}) {
		t.Fatalf("loop MIR = %#v", function)
	}
	bound, err := BindFirstSliceCapabilities(structural)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyBoundFirstSliceMIR(bound); err != nil {
		t.Fatal(err)
	}
}

func TestPhase2NullableCoalesceRepresentationAndMIRAreCanonical(t *testing.T) {
	for _, functionName := range []string{"coalesce", "coalesceAssign"} {
		t.Run(functionName, func(t *testing.T) {
			hir := validPhase2CoalesceHIR()
			hir.Functions[0].Name = functionName
			_, hirHash, err := CanonicalPhase2HIR(hir)
			if err != nil {
				t.Fatal(err)
			}
			hir.ContentHash = hirHash
			plan, err := NewRepresentationPlanForHIR(phase2ChooseProvenance(hir), hir)
			if err != nil {
				t.Fatal(err)
			}
			if len(plan.Bindings) != 3 || plan.Bindings[2].SourceType != TypeNullableNumber || plan.Bindings[2].RepType != RepNullableF64 || plan.Bindings[2].BitWidth != 128 || plan.Bindings[2].ABIAlign != 8 {
				t.Fatalf("%s representation bindings = %#v", functionName, plan.Bindings)
			}
			structural, err := LowerFirstSliceMIR(hir, plan)
			if err != nil {
				t.Fatal(err)
			}
			function := structural.Functions[0]
			if function.Name != functionName || function.Parameters[0].Type != RepNullableF64 || function.Blocks[0].Instructions[0].Kind != "nullable.is-nullish" || function.Blocks[2].Instructions[0].Kind != "nullable.unwrap" || function.Blocks[3].Instructions[0].Kind != "phi" {
				t.Fatalf("%s MIR = %#v", functionName, function)
			}
			for _, test := range []struct {
				name   string
				mutate func(*FirstSliceMIRArtifact)
			}{
				{name: "tag test", mutate: func(candidate *FirstSliceMIRArtifact) {
					candidate.Functions[0].Blocks[0].Instructions[0].Kind = "i1.copy"
				}},
				{name: "unwrap", mutate: func(candidate *FirstSliceMIRArtifact) {
					candidate.Functions[0].Blocks[2].Instructions[0].Operands[0] = 2
				}},
				{name: "phi edge", mutate: func(candidate *FirstSliceMIRArtifact) {
					candidate.Functions[0].Blocks[3].Instructions[0].IncomingBlocks[0] = 3
				}},
			} {
				t.Run(test.name, func(t *testing.T) {
					candidate := structural
					candidate.Functions = cloneFirstSliceFunctions(structural.Functions)
					test.mutate(&candidate)
					candidate.ContentHash, err = firstSliceMIRContentHash(candidate)
					if err != nil {
						t.Fatal(err)
					}
					if err := VerifyStructuralFirstSliceMIR(candidate); err == nil {
						t.Fatal("malformed nullable coalesce MIR was accepted")
					}
				})
			}
		})
	}
}

func TestPhase2LoopMIRRejectsRehashedPhiAndCFGTampering(t *testing.T) {
	hir := validPhase2LoopHIR()
	_, hirHash, err := CanonicalPhase2HIR(hir)
	if err != nil {
		t.Fatal(err)
	}
	hir.ContentHash = hirHash
	plan, err := NewRepresentationPlanForHIR(phase2ChooseProvenance(hir), hir)
	if err != nil {
		t.Fatal(err)
	}
	base, err := LowerFirstSliceMIR(hir, plan)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		mutate func(*FirstSliceMIRArtifact)
		want   string
	}{
		{name: "phi incoming edge", mutate: func(module *FirstSliceMIRArtifact) {
			module.Functions[0].Blocks[1].Instructions[0].IncomingBlocks[1] = 4
		}, want: "phi is invalid"},
		{name: "phi value", mutate: func(module *FirstSliceMIRArtifact) { module.Functions[0].Blocks[1].Instructions[0].Operands[1] = 4 }, want: "phi is invalid"},
		{name: "comparison", mutate: func(module *FirstSliceMIRArtifact) { module.Functions[0].Blocks[1].Instructions[1].Kind = "fcmp.ule" }, want: "comparison is invalid"},
		{name: "back edge", mutate: func(module *FirstSliceMIRArtifact) { module.Functions[0].Blocks[2].Terminator.Successors[0] = 4 }, want: "body is invalid"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := base
			candidate.Functions = cloneFirstSliceFunctions(base.Functions)
			test.mutate(&candidate)
			candidate.ContentHash, err = firstSliceMIRContentHash(candidate)
			if err != nil {
				t.Fatal(err)
			}
			if err := VerifyStructuralFirstSliceMIR(candidate); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("loop MIR tamper error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestPhase2LocalCallMIRRejectsRehashedCallTampering(t *testing.T) {
	hir := validPhase2LocalCallHIR()
	_, hirHash, err := CanonicalPhase2HIR(hir)
	if err != nil {
		t.Fatal(err)
	}
	hir.ContentHash = hirHash
	plan, err := NewRepresentationPlanForHIR(phase2ChooseProvenance(hir), hir)
	if err != nil {
		t.Fatal(err)
	}
	base, err := LowerFirstSliceMIR(hir, plan)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		mutate func(*FirstSliceMIRArtifact)
		want   string
	}{
		{name: "helper exported", mutate: func(module *FirstSliceMIRArtifact) { module.Functions[0].Exported = true }, want: "helper is invalid"},
		{name: "entry internal", mutate: func(module *FirstSliceMIRArtifact) { module.Functions[1].Exported = false }, want: "entry is invalid"},
		{name: "callee", mutate: func(module *FirstSliceMIRArtifact) { module.Functions[1].Blocks[0].Instructions[0].Callee = 2 }, want: "direct call is invalid"},
		{name: "effect", mutate: func(module *FirstSliceMIRArtifact) { module.Functions[1].Blocks[0].Instructions[0].Effect = EffectPure }, want: "direct call is invalid"},
		{name: "assignment value", mutate: func(module *FirstSliceMIRArtifact) { module.Functions[1].Blocks[0].Instructions[1].Operands[0] = 1 }, want: "assignment value is invalid"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := base
			candidate.Functions = cloneFirstSliceFunctions(base.Functions)
			test.mutate(&candidate)
			candidate.ContentHash, err = firstSliceMIRContentHash(candidate)
			if err != nil {
				t.Fatal(err)
			}
			if err := VerifyStructuralFirstSliceMIR(candidate); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("local-call MIR tamper error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestPhase2ChooseMIRVerifierRejectsRehashedCFGTampering(t *testing.T) {
	hir := validPhase2ChooseHIR()
	_, hirHash, err := CanonicalPhase2HIR(hir)
	if err != nil {
		t.Fatal(err)
	}
	hir.ContentHash = hirHash
	plan, err := NewRepresentationPlanForHIR(phase2ChooseProvenance(hir), hir)
	if err != nil {
		t.Fatal(err)
	}
	base, err := LowerFirstSliceMIR(hir, plan)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		mutate func(*FirstSliceMIRArtifact)
		want   string
	}{
		{name: "condition representation", mutate: func(module *FirstSliceMIRArtifact) { module.Functions[0].Parameters[0].Type = RepF64 }, want: "parameter 0 is invalid"},
		{name: "successor", mutate: func(module *FirstSliceMIRArtifact) { module.Functions[0].Blocks[0].Terminator.Successors[1] = 9 }, want: "conditional branch is invalid"},
		{name: "true return", mutate: func(module *FirstSliceMIRArtifact) { module.Functions[0].Blocks[1].Terminator.Value = 3 }, want: "true return is invalid"},
		{name: "missing operations slice", mutate: func(module *FirstSliceMIRArtifact) { module.Functions[0].Blocks[2].Instructions = nil }, want: "block 3 is invalid"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := base
			candidate.Functions = cloneFirstSliceFunctions(base.Functions)
			test.mutate(&candidate)
			candidate.ContentHash, err = firstSliceMIRContentHash(candidate)
			if err != nil {
				t.Fatal(err)
			}
			if err := VerifyStructuralFirstSliceMIR(candidate); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("choose MIR tamper error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestMIRLoweringRejectsRepresentationPlanWithUnusedBinding(t *testing.T) {
	hir, numberOnly := firstSliceMIRFixture(t)
	plan, err := NewPrimitiveRepresentationPlan(numberOnly.Provenance, []TypeKind{TypeBoolean, TypeNumber})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := LowerFirstSliceMIR(hir, plan); err == nil || !strings.Contains(err.Error(), "exact HIR primitive types") {
		t.Fatalf("extra representation binding error = %v", err)
	}
}

func phase2ChooseProvenance(hir HIRModule) TargetProvenance {
	return TargetProvenance{
		HIRHash:                        hir.ContentHash,
		FrontendSnapshotHash:           hir.Provenance.FrontendSnapshotHash,
		BuildPlanHash:                  strings.Repeat("1", 64),
		CompilerBuildIdentity:          hir.Provenance.CompilerBuildIdentity,
		TargetContextHash:              strings.Repeat("2", 64),
		DataLayoutHash:                 strings.Repeat("3", 64),
		AvailableCapabilityCatalogHash: strings.Repeat("4", 64),
		ToolchainManifestHash:          strings.Repeat("5", 64),
		RuntimeManifestHash:            strings.Repeat("6", 64),
	}
}

func TestFirstSliceMIRVerifierRejectsRehashedStructuralAndClosureTampering(t *testing.T) {
	hir, plan := firstSliceMIRFixture(t)
	base, err := LowerFirstSliceMIR(hir, plan)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		mutate func(*FirstSliceMIRArtifact)
		want   string
	}{
		{name: "function ID", mutate: func(module *FirstSliceMIRArtifact) { module.Functions[0].ID = 2 }, want: "function is invalid"},
		{name: "parameter ID", mutate: func(module *FirstSliceMIRArtifact) { module.Functions[0].Parameters[1].Value = 4 }, want: "parameter 1 is invalid"},
		{name: "instruction kind", mutate: func(module *FirstSliceMIRArtifact) { module.Functions[0].Blocks[0].Instructions[0].Kind = "add" }, want: "instruction is invalid"},
		{name: "instruction type", mutate: func(module *FirstSliceMIRArtifact) { module.Functions[0].Blocks[0].Instructions[0].Type = "i64" }, want: "instruction is invalid"},
		{name: "missing requirements", mutate: func(module *FirstSliceMIRArtifact) {
			module.Functions[0].Blocks[0].Instructions[0].LogicalCapabilityRequirements = nil
		}, want: "instruction is invalid"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := base
			candidate.Functions = cloneFirstSliceFunctions(base.Functions)
			test.mutate(&candidate)
			candidate.ContentHash, err = firstSliceMIRContentHash(candidate)
			if err != nil {
				t.Fatal(err)
			}
			if err := VerifyStructuralFirstSliceMIR(candidate); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("tamper error = %v, want %q", err, test.want)
			}
		})
	}

	bound, err := BindFirstSliceCapabilities(base)
	if err != nil {
		t.Fatal(err)
	}
	bound.BoundCapabilityClosure.AvailableCapabilityCatalogHash = strings.Repeat("f", 64)
	bound.ContentHash, err = firstSliceMIRContentHash(bound)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyBoundFirstSliceMIR(bound); err == nil || !strings.Contains(err.Error(), "closure is invalid") {
		t.Fatalf("closure substitution error = %v", err)
	}
}

func TestRepresentationPlanAndMIRStrictDecodersRejectUnknownFields(t *testing.T) {
	_, plan := firstSliceMIRFixture(t)
	planBytes, err := plan.CanonicalBytes()
	if err != nil {
		t.Fatal(err)
	}
	unknownPlan := strings.Replace(string(planBytes), fmt.Sprintf(`"schemaVersion":%d`, RepresentationPlanSchemaVersion), fmt.Sprintf(`"schemaVersion":%d,"unknown":true`, RepresentationPlanSchemaVersion), 1)
	if _, err := DecodeRepresentationPlan([]byte(unknownPlan)); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("unknown representation field error = %v", err)
	}

	hir, _ := firstSliceMIRFixture(t)
	module, err := LowerFirstSliceMIR(hir, plan)
	if err != nil {
		t.Fatal(err)
	}
	moduleBytes, err := module.CanonicalStructuralBytes()
	if err != nil {
		t.Fatal(err)
	}
	unknownMIR := strings.Replace(string(moduleBytes), fmt.Sprintf(`"schemaVersion":%d`, FirstSliceMIRSchemaVersion), fmt.Sprintf(`"schemaVersion":%d,"unknown":true`, FirstSliceMIRSchemaVersion), 1)
	if _, err := DecodeStructuralFirstSliceMIR([]byte(unknownMIR)); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("unknown MIR field error = %v", err)
	}
}

func firstSliceMIRFixture(t *testing.T) (HIRModule, RepresentationPlan) {
	t.Helper()
	hir := validFirstSliceHIR()
	_, hirHash, err := CanonicalHIR(hir)
	if err != nil {
		t.Fatal(err)
	}
	hir.ContentHash = hirHash
	plan, err := NewRepresentationPlan(TargetProvenance{
		HIRHash:                        hir.ContentHash,
		FrontendSnapshotHash:           hir.Provenance.FrontendSnapshotHash,
		BuildPlanHash:                  strings.Repeat("1", 64),
		CompilerBuildIdentity:          hir.Provenance.CompilerBuildIdentity,
		TargetContextHash:              strings.Repeat("2", 64),
		DataLayoutHash:                 strings.Repeat("3", 64),
		AvailableCapabilityCatalogHash: strings.Repeat("4", 64),
		ToolchainManifestHash:          strings.Repeat("5", 64),
		RuntimeManifestHash:            strings.Repeat("6", 64),
	})
	if err != nil {
		t.Fatal(err)
	}
	return hir, plan
}

func cloneFirstSliceFunctions(input []FirstSliceMIRFunction) []FirstSliceMIRFunction {
	result := make([]FirstSliceMIRFunction, len(input))
	for index, function := range input {
		result[index] = function
		result[index].Parameters = slices.Clone(function.Parameters)
		result[index].Blocks = make([]FirstSliceMIRBlock, len(function.Blocks))
		for blockIndex, block := range function.Blocks {
			result[index].Blocks[blockIndex] = block
			result[index].Blocks[blockIndex].Terminator.Successors = slices.Clone(block.Terminator.Successors)
			result[index].Blocks[blockIndex].Instructions = make([]FirstSliceMIRInstruction, len(block.Instructions))
			for instructionIndex, instruction := range block.Instructions {
				result[index].Blocks[blockIndex].Instructions[instructionIndex] = instruction
				result[index].Blocks[blockIndex].Instructions[instructionIndex].Operands = slices.Clone(instruction.Operands)
				result[index].Blocks[blockIndex].Instructions[instructionIndex].IncomingBlocks = slices.Clone(instruction.IncomingBlocks)
				result[index].Blocks[blockIndex].Instructions[instructionIndex].LogicalCapabilityRequirements = slices.Clone(instruction.LogicalCapabilityRequirements)
			}
		}
	}
	return result
}
