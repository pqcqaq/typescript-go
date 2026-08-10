package bingo

import (
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
	if _, err := structural.CanonicalStructuralBytes(); err != nil {
		t.Fatal(err)
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
	unknownPlan := strings.Replace(string(planBytes), `"schemaVersion":1`, `"schemaVersion":1,"unknown":true`, 1)
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
	unknownMIR := strings.Replace(string(moduleBytes), `"schemaVersion":1`, `"schemaVersion":1,"unknown":true`, 1)
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
			result[index].Blocks[blockIndex].Instructions = make([]FirstSliceMIRInstruction, len(block.Instructions))
			for instructionIndex, instruction := range block.Instructions {
				result[index].Blocks[blockIndex].Instructions[instructionIndex] = instruction
				result[index].Blocks[blockIndex].Instructions[instructionIndex].Operands = slices.Clone(instruction.Operands)
				result[index].Blocks[blockIndex].Instructions[instructionIndex].LogicalCapabilityRequirements = slices.Clone(instruction.LogicalCapabilityRequirements)
			}
		}
	}
	return result
}
