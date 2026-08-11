package bingo

import (
	"slices"
	"strings"
	"testing"
)

func testOrigin(start, end int) Origin {
	return Origin{File: "main.ts", Start: start, End: end}
}

func testCompilerBuildIdentity() CompilerBuildIdentity {
	return CompilerBuildIdentity{
		UpstreamCommit: strings.Repeat("1", 40),
		ForkCommit:     strings.Repeat("2", 40),
		LoweringSchema: "bingo-hir-lowering-v7",
		LoweringHash:   strings.Repeat("4", 64),
	}
}

func testHIRProvenance(requirements []RuntimeCapabilityID) HIRProvenance {
	digest, err := LogicalCapabilityRequirementsDigest(requirements)
	if err != nil {
		panic(err)
	}
	return HIRProvenance{
		FrontendSnapshotSchemaVersion:       HIRFrontendSnapshotSchemaVersion,
		FrontendSnapshotHash:                strings.Repeat("5", 64),
		SourceContentHash:                   strings.Repeat("6", 64),
		CompilerBuildIdentity:               testCompilerBuildIdentity(),
		StandardLibraryHash:                 strings.Repeat("7", 64),
		KindManifestHash:                    strings.Repeat("8", 64),
		LogicalCapabilityRequirementsDigest: digest,
	}
}

func validFirstSliceHIR() HIRModule {
	requirements := make([]RuntimeCapabilityID, 0)
	return HIRModule{
		SchemaVersion:                 HIRSchemaVersion,
		Provenance:                    testHIRProvenance(requirements),
		LogicalCapabilityRequirements: requirements,
		Functions: []HIRFunction{{
			ID: 1, Name: "add", ReturnType: TypeNumber, Origin: testOrigin(0, 32),
			Parameters: []HIRParameter{
				{Name: "a", Value: 1, Type: TypeNumber, Origin: testOrigin(10, 11)},
				{Name: "b", Value: 2, Type: TypeNumber, Origin: testOrigin(13, 14)},
			},
			Blocks: []HIRBlock{{
				ID: 1,
				Operations: []HIROp{{
					ID: 3, Kind: "binary", Type: TypeNumber, Operands: []ValueID{1, 2}, Operator: "+", Effect: EffectPure,
					LogicalCapabilityRequirements: make([]RuntimeCapabilityID, 0), Origin: testOrigin(25, 30),
				}},
				Terminator: HIRTerminator{Kind: "return", Value: 3, Origin: testOrigin(31, 32)},
			}},
		}},
	}
}

func TestVerifyHIRV2AndCanonicalHash(t *testing.T) {
	module := validFirstSliceHIR()
	encoded, hash, err := CanonicalHIR(module)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) == 0 || len(hash) != 64 {
		t.Fatalf("canonical artifact = %d bytes, hash %q", len(encoded), hash)
	}
	module.ContentHash = hash
	if err := VerifyCanonicalHIR(module); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(module.LogicalCapabilityRequirements, []RuntimeCapabilityID{}) ||
		module.Functions[0].Blocks[0].Operations[0].LogicalCapabilityRequirements == nil {
		t.Fatalf("canonical empty capability requirements are not explicit: %#v", module)
	}
}

func TestVerifyHIRV2RejectsOldSchemaAndNonFirstSliceShapes(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*HIRModule)
		want   string
	}{
		{name: "old schema", mutate: func(module *HIRModule) { module.SchemaVersion = 1 }, want: "unsupported HIR schema"},
		{name: "zero functions", mutate: func(module *HIRModule) { module.Functions = nil }, want: "exactly one function"},
		{name: "multiple functions", mutate: func(module *HIRModule) { module.Functions = append(module.Functions, module.Functions[0]) }, want: "exactly one function"},
		{name: "sparse function ID", mutate: func(module *HIRModule) { module.Functions[0].ID = 2 }, want: "canonical dense ID"},
		{name: "boolean parameter", mutate: func(module *HIRModule) { module.Functions[0].Parameters[0].Type = TypeBoolean }, want: "invalid parameter"},
		{name: "string return", mutate: func(module *HIRModule) { module.Functions[0].ReturnType = TypeString }, want: "invalid return type"},
		{name: "void production return", mutate: func(module *HIRModule) { module.Functions[0].ReturnType = TypeVoid }, want: "first-slice function return type"},
		{name: "one parameter", mutate: func(module *HIRModule) { module.Functions[0].Parameters = module.Functions[0].Parameters[:1] }, want: "requires two parameters"},
		{name: "sparse parameter value", mutate: func(module *HIRModule) { module.Functions[0].Parameters[1].Value = 4 }, want: "canonical dense ID"},
		{name: "multiple blocks", mutate: func(module *HIRModule) {
			module.Functions[0].Blocks = append(module.Functions[0].Blocks, module.Functions[0].Blocks[0])
		}, want: "requires one block"},
		{name: "sparse block ID", mutate: func(module *HIRModule) { module.Functions[0].Blocks[0].ID = 2 }, want: "canonical dense ID"},
		{name: "zero operations", mutate: func(module *HIRModule) { module.Functions[0].Blocks[0].Operations = nil }, want: "requires one operation"},
		{name: "multiple operations", mutate: func(module *HIRModule) {
			module.Functions[0].Blocks[0].Operations = append(module.Functions[0].Blocks[0].Operations, module.Functions[0].Blocks[0].Operations[0])
		}, want: "requires one operation"},
		{name: "sparse operation value", mutate: func(module *HIRModule) { module.Functions[0].Blocks[0].Operations[0].ID = 4 }, want: "canonical dense ID"},
		{name: "undefined operand", mutate: func(module *HIRModule) { module.Functions[0].Blocks[0].Operations[0].Operands[0] = 99 }, want: "undefined value"},
		{name: "invalid operation origin", mutate: func(module *HIRModule) { module.Functions[0].Blocks[0].Operations[0].Origin.File = "" }, want: "invalid operation"},
		{name: "invalid operation effect", mutate: func(module *HIRModule) { module.Functions[0].Blocks[0].Operations[0].Effect = EffectRead }, want: "invalid effect/operator"},
		{name: "literal", mutate: func(module *HIRModule) {
			operation := &module.Functions[0].Blocks[0].Operations[0]
			operation.Kind = "literal"
			operation.Operands = nil
			operation.Operator = ""
		}, want: "outside first-slice HIR"},
		{name: "phi", mutate: func(module *HIRModule) { module.Functions[0].Blocks[0].Operations[0].Kind = "phi" }, want: "outside first-slice HIR"},
		{name: "call", mutate: func(module *HIRModule) {
			operation := &module.Functions[0].Blocks[0].Operations[0]
			operation.Kind = "call"
			operation.Operator = ""
			operation.Effect = EffectCall
		}, want: "outside first-slice HIR"},
		{name: "load", mutate: func(module *HIRModule) {
			operation := &module.Functions[0].Blocks[0].Operations[0]
			operation.Kind = "load"
			operation.Operator = ""
			operation.Effect = EffectRead
		}, want: "outside first-slice HIR"},
		{name: "store", mutate: func(module *HIRModule) {
			operation := &module.Functions[0].Blocks[0].Operations[0]
			operation.Kind = "store"
			operation.Operator = ""
			operation.Effect = EffectWrite
		}, want: "outside first-slice HIR"},
		{name: "subtract", mutate: func(module *HIRModule) { module.Functions[0].Blocks[0].Operations[0].Operator = "-" }, want: "invalid effect/operator"},
		{name: "invalid terminator origin", mutate: func(module *HIRModule) { module.Functions[0].Blocks[0].Terminator.Origin.End = -1 }, want: "invalid terminator"},
		{name: "branch", mutate: func(module *HIRModule) {
			module.Functions[0].Blocks[0].Terminator = HIRTerminator{Kind: "branch", Successors: []BlockID{1}, Origin: testOrigin(31, 32)}
		}, want: "outside first-slice HIR"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			module := validFirstSliceHIR()
			test.mutate(&module)
			if err := VerifyHIR(module); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("VerifyHIR error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestVerifyHIRV2RejectsCompilerIdentityTampering(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*CompilerBuildIdentity)
	}{
		{name: "upstream", mutate: func(identity *CompilerBuildIdentity) { identity.UpstreamCommit = "ABC" }},
		{name: "fork", mutate: func(identity *CompilerBuildIdentity) { identity.ForkCommit = "" }},
		{name: "lowering schema", mutate: func(identity *CompilerBuildIdentity) { identity.LoweringSchema = "Bingo HIR" }},
		{name: "lowering hash", mutate: func(identity *CompilerBuildIdentity) { identity.LoweringHash = strings.Repeat("0", 63) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			module := validFirstSliceHIR()
			test.mutate(&module.Provenance.CompilerBuildIdentity)
			if err := VerifyHIR(module); err == nil {
				t.Fatal("tampered compiler build identity was accepted")
			}
		})
	}
}

func TestLogicalCapabilityRequirementsAreExplicitCanonicalAndHashed(t *testing.T) {
	empty := make([]RuntimeCapabilityID, 0)
	first, err := LogicalCapabilityRequirementsDigest(empty)
	if err != nil {
		t.Fatal(err)
	}
	second, err := LogicalCapabilityRequirementsDigest(empty)
	if err != nil || first != second || len(first) != 64 {
		t.Fatalf("empty requirements digest = %q / %q / %v", first, second, err)
	}
	for _, requirements := range [][]RuntimeCapabilityID{
		nil,
		{"runtime:regexp", "runtime:bigint"},
		{"runtime:regexp", "runtime:regexp"},
		{"Runtime:regexp"},
	} {
		if _, err := LogicalCapabilityRequirementsDigest(requirements); err == nil {
			t.Fatalf("invalid requirements were accepted: %#v", requirements)
		}
	}

	module := validFirstSliceHIR()
	module.Provenance.LogicalCapabilityRequirementsDigest = strings.Repeat("0", 64)
	if err := VerifyHIR(module); err == nil || !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("tampered requirements digest error = %v", err)
	}

	module = validFirstSliceHIR()
	module.Functions[0].Blocks[0].Operations[0].LogicalCapabilityRequirements = nil
	if err := VerifyHIR(module); err == nil || !strings.Contains(err.Error(), "requirements are missing") {
		t.Fatalf("missing operation requirements error = %v", err)
	}

	module = validFirstSliceHIR()
	module.LogicalCapabilityRequirements = []RuntimeCapabilityID{"runtime:regexp"}
	module.Functions[0].Blocks[0].Operations[0].LogicalCapabilityRequirements = []RuntimeCapabilityID{"runtime:regexp"}
	digest, err := LogicalCapabilityRequirementsDigest(module.LogicalCapabilityRequirements)
	if err != nil {
		t.Fatal(err)
	}
	module.Provenance.LogicalCapabilityRequirementsDigest = digest
	if err := VerifyHIR(module); err == nil || !strings.Contains(err.Error(), "does not bind runtime capabilities") {
		t.Fatalf("first-slice logical capability error = %v", err)
	}
}

func TestPassContractRequiresResolvedTargetContext(t *testing.T) {
	specs := PassSpecs()
	resolveIndex := slices.IndexFunc(specs, func(spec PassSpec) bool { return spec.ID == PassResolveTarget })
	representationIndex := slices.IndexFunc(specs, func(spec PassSpec) bool { return spec.ID == PassRepresentationPlan })
	capabilityIndex := slices.IndexFunc(specs, func(spec PassSpec) bool { return spec.ID == PassCapabilityBinding })
	if resolveIndex < 0 || representationIndex != resolveIndex+1 || capabilityIndex < representationIndex {
		t.Fatalf("target/capability pass order = resolve %d, representation %d, capability %d", resolveIndex, representationIndex, capabilityIndex)
	}
	resolve := specs[resolveIndex]
	if resolve.InputSchema != "hir-v7" || slices.Contains(resolve.ReadsFacts, "conversion-plan") ||
		slices.ContainsFunc(resolve.ReadsArtifacts, func(requirement PassArtifactRequirement) bool {
			return requirement.Name == PassArtifactTypedHIR
		}) {
		t.Fatalf("target resolution contract = %#v", resolve)
	}
	for _, fact := range []string{"target-context", "data-layout", "available-capability-catalog"} {
		if !slices.Contains(resolve.WritesFacts, fact) {
			t.Errorf("resolver does not produce %q: %#v", fact, resolve)
		}
	}
	representation := specs[representationIndex]
	for _, fact := range []string{"typed-hir", "canonical-build-plan", "target-context", "data-layout", "available-capability-catalog"} {
		if !slices.Contains(representation.ReadsFacts, fact) {
			t.Errorf("representation plan does not require %q: %#v", fact, representation)
		}
	}
	capability := specs[capabilityIndex]
	if !slices.Contains(capability.ReadsFacts, "available-capability-catalog") ||
		!slices.Contains(capability.WritesFacts, "bound-capability-closure") {
		t.Fatalf("capability catalog/closure boundary = %#v", capability)
	}
}

func TestValidatePassSequenceRejectsMutatedCompatibilitySlice(t *testing.T) {
	sequence := append([]PassID(nil), PassDAG...)
	sequence[0] = PassFinalVerifier
	if err := ValidatePassSequence(sequence); err == nil {
		t.Fatal("mutated pass sequence was accepted")
	}
}
