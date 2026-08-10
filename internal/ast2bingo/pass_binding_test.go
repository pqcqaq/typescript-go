package ast2bingo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/internal/bingo"
)

func TestPrimitiveHIRProductionBindingExecutesOnlyCanonicalPhase15Prefix(t *testing.T) {
	snapshot := buildReplayAddSnapshot(t)
	identity := testCompilerIdentity(t, *snapshot)
	artifact, execution, err := executePrimitiveHIRPasses(context.Background(), *snapshot, identity)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(execution.Dumps), 2; got != want {
		t.Fatalf("production dump count = %d, want %d", got, want)
	}
	if execution.Dumps[0].Pass != bingo.PassValidateSnapshot || execution.Dumps[1].Pass != bingo.PassTypedHIR {
		t.Fatalf("production pass order = %#v", execution.Dumps)
	}
	if execution.State.Schema != "hir-v4" || !slices.Contains(execution.State.Facts, "typed-hir") {
		t.Fatalf("production terminal state = %#v", execution.State)
	}
	for _, dump := range execution.Dumps {
		if strings.Contains(dump.Schema, "mir") {
			t.Fatalf("Phase 1.5 production binding claimed MIR schema: %#v", dump)
		}
		if len(dump.Effects.Artifact) != 0 || len(dump.Effects.Introduced) != 0 {
			t.Fatalf("primitive prefix claimed effects: %#v", dump.Effects)
		}
	}
	plan, err := buildPrimitiveSourceTypePlan(*snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyPrimitiveTypedHIRArtifact(plan, artifact, identity); err != nil {
		t.Fatalf("verify production artifact: %v", err)
	}
}

func TestPrimitiveSourceTypePlanIsDriverIdentityFreeAndHIRIsNot(t *testing.T) {
	snapshot := buildReplayAddSnapshot(t)
	plan, err := buildPrimitiveSourceTypePlan(*snapshot)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{
		`"compilerBuildIdentity"`,
		`"upstreamCommit"`,
		`"forkCommit"`,
		`"loweringSchema"`,
		`"loweringHash"`,
	} {
		if strings.Contains(string(encoded), field) {
			t.Fatalf("source type plan contains driver identity field %s: %s", field, encoded)
		}
	}

	firstIdentity := testCompilerIdentity(t, *snapshot)
	secondIdentity := firstIdentity
	secondIdentity.LoweringHash = strings.Repeat("b", 64)
	first, err := lowerPrimitiveSourceTypePlan(plan, firstIdentity)
	if err != nil {
		t.Fatal(err)
	}
	second, err := lowerPrimitiveSourceTypePlan(plan, secondIdentity)
	if err != nil {
		t.Fatal(err)
	}
	if first.HIR.Provenance.CompilerBuildIdentity != firstIdentity ||
		second.HIR.Provenance.CompilerBuildIdentity != secondIdentity {
		t.Fatalf("HIR provenance did not retain driver identities: %#v / %#v", first.HIR.Provenance, second.HIR.Provenance)
	}
	if first.HIR.ContentHash == second.HIR.ContentHash {
		t.Fatalf("different compiler identities produced the same HIR hash %q", first.HIR.ContentHash)
	}
}

func TestPrimitiveHIRProductionRegistryRejectsMissingHandler(t *testing.T) {
	snapshot := buildReplayAddSnapshot(t)
	handlers := primitiveHIRPassHandlers(testCompilerIdentity(t, *snapshot))
	delete(handlers, bingo.PassTypedHIR)
	if _, err := bingo.NewPassExecutorThrough(handlers, bingo.PassTypedHIR, 0); err == nil || !strings.Contains(err.Error(), "no registered handler") {
		t.Fatalf("missing production handler error = %v", err)
	}
}

func TestPrimitiveTypedHIRPostVerifierRejectsTampering(t *testing.T) {
	identity, plan, input, valid := primitiveTypedHIRVerifierFixture(t)
	handler := primitiveHIRPassHandlers(identity)[bingo.PassTypedHIR]
	spec := primitivePassSpec(t, bingo.PassTypedHIR)

	tests := []struct {
		name   string
		mutate func(primitiveTypedHIRArtifact) json.RawMessage
		want   string
	}{
		{
			name: "truncated artifact bytes",
			mutate: func(primitiveTypedHIRArtifact) json.RawMessage {
				return json.RawMessage(`{"schemaVersion":2,"events":[`)
			},
			want: "decode primitive typed HIR artifact",
		},
		{
			name: "rehashed compiler identity",
			mutate: func(value primitiveTypedHIRArtifact) json.RawMessage {
				value.CompilerBuildIdentity.ForkCommit = strings.Repeat("b", 40)
				value.HIR.Provenance.CompilerBuildIdentity = value.CompilerBuildIdentity
				rehashPrimitiveHIRTestArtifact(t, &value)
				return marshalPrimitivePassTestArtifact(t, value)
			},
			want: "compiler identity does not match",
		},
		{
			name: "rehashed logical capability",
			mutate: func(value primitiveTypedHIRArtifact) json.RawMessage {
				value.HIR.LogicalCapabilityRequirements = []bingo.RuntimeCapabilityID{"runtime:regexp"}
				value.HIR.Functions[0].Blocks[0].Operations[0].LogicalCapabilityRequirements = []bingo.RuntimeCapabilityID{"runtime:regexp"}
				digest, err := bingo.LogicalCapabilityRequirementsDigest(value.HIR.LogicalCapabilityRequirements)
				if err != nil {
					t.Fatal(err)
				}
				value.HIR.Provenance.LogicalCapabilityRequirementsDigest = digest
				rehashPrimitiveHIRTestArtifact(t, &value)
				return marshalPrimitivePassTestArtifact(t, value)
			},
			want: "does not bind runtime capabilities",
		},
		{
			name: "content hash",
			mutate: func(value primitiveTypedHIRArtifact) json.RawMessage {
				value.HIR.ContentHash = strings.Repeat("0", 64)
				return marshalPrimitivePassTestArtifact(t, value)
			},
			want: "content hash mismatch",
		},
		{
			name: "rehashed operator",
			mutate: func(value primitiveTypedHIRArtifact) json.RawMessage {
				value.HIR.Functions[0].Blocks[0].Operations[0].Operator = "-"
				rehashPrimitiveHIRTestArtifact(t, &value)
				return marshalPrimitivePassTestArtifact(t, value)
			},
			want: "invalid effect/operator",
		},
		{
			name: "rehashed frontend provenance",
			mutate: func(value primitiveTypedHIRArtifact) json.RawMessage {
				value.HIR.Provenance.FrontendSnapshotHash = strings.Repeat("1", 64)
				value.FrontendSnapshotHash = value.HIR.Provenance.FrontendSnapshotHash
				rehashPrimitiveHIRTestArtifact(t, &value)
				return marshalPrimitivePassTestArtifact(t, value)
			},
			want: "provenance does not match its source plan",
		},
		{
			name: "rehashed type",
			mutate: func(value primitiveTypedHIRArtifact) json.RawMessage {
				function := &value.HIR.Functions[0]
				function.ReturnType = bingo.TypeString
				for index := range function.Parameters {
					function.Parameters[index].Type = bingo.TypeString
				}
				function.Blocks[0].Operations[0].Type = bingo.TypeString
				rehashPrimitiveHIRTestArtifact(t, &value)
				return marshalPrimitivePassTestArtifact(t, value)
			},
			want: "invalid return type",
		},
		{
			name: "evaluation order",
			mutate: func(value primitiveTypedHIRArtifact) json.RawMessage {
				value.Events[1], value.Events[2] = value.Events[2], value.Events[1]
				return marshalPrimitivePassTestArtifact(t, value)
			},
			want: "evaluation-order events",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := clonePrimitiveTypedHIRTestArtifact(t, valid)
			output := bingo.PassState{Schema: "hir-v4", Facts: []string{"source-type-plan", "typed-hir"}, Artifact: test.mutate(candidate)}
			if _, err := handler.PostVerify(context.Background(), spec, 1, input, output); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("post-verifier error = %v, want %q", err, test.want)
			}
		})
	}

	if err := verifyPrimitiveTypedHIRArtifact(plan, valid, identity); err != nil {
		t.Fatalf("fixture no longer verifies: %v", err)
	}
}

func TestPrimitiveChooseTypedHIRPostVerifierRejectsTampering(t *testing.T) {
	snapshot := buildReplayChooseSnapshot(t)
	identity := testCompilerIdentity(t, *snapshot)
	plan, err := buildPrimitiveSourceTypePlan(*snapshot)
	if err != nil {
		t.Fatal(err)
	}
	planJSON, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	valid, err := lowerPrimitiveSourceTypePlan(plan, identity)
	if err != nil {
		t.Fatal(err)
	}
	handler := primitiveHIRPassHandlers(identity)[bingo.PassTypedHIR]
	spec := primitivePassSpec(t, bingo.PassTypedHIR)
	input := bingo.PassState{Schema: "source-type-plan-v2", Facts: []string{"source-type-plan"}, Artifact: planJSON}

	tests := []struct {
		name   string
		mutate func(*primitiveTypedHIRArtifact)
		want   string
	}{
		{name: "boolean parameter type", mutate: func(value *primitiveTypedHIRArtifact) {
			value.HIR.Functions[0].Parameters[0].Type = bingo.TypeNumber
		}, want: "conditional branch value"},
		{name: "number condition", mutate: func(value *primitiveTypedHIRArtifact) {
			value.HIR.Functions[0].Blocks[0].Terminator.Value = 2
		}, want: "conditional branch value"},
		{name: "missing successor", mutate: func(value *primitiveTypedHIRArtifact) {
			value.HIR.Functions[0].Blocks[0].Terminator.Successors[1] = 9
		}, want: "targets missing block"},
		{name: "swapped returns", mutate: func(value *primitiveTypedHIRArtifact) {
			blocks := value.HIR.Functions[0].Blocks
			blocks[1].Terminator.Value, blocks[2].Terminator.Value = blocks[2].Terminator.Value, blocks[1].Terminator.Value
		}, want: "true return does not match source plan"},
		{name: "event order", mutate: func(value *primitiveTypedHIRArtifact) {
			value.Events[4], value.Events[5] = value.Events[5], value.Events[4]
		}, want: "evaluation-order events"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := clonePrimitiveTypedHIRTestArtifact(t, valid)
			test.mutate(&candidate)
			rehashPrimitiveHIRTestArtifact(t, &candidate)
			output := bingo.PassState{
				Schema:   "hir-v4",
				Facts:    []string{"source-type-plan", "typed-hir"},
				Artifact: marshalPrimitivePassTestArtifact(t, candidate),
			}
			if _, err := handler.PostVerify(context.Background(), spec, 1, input, output); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("post-verifier error = %v, want %q", err, test.want)
			}
		})
	}

	if err := verifyPrimitiveTypedHIRArtifact(plan, valid, identity); err != nil {
		t.Fatalf("choose fixture no longer verifies: %v", err)
	}
}

func primitiveTypedHIRVerifierFixture(t *testing.T) (bingo.CompilerBuildIdentity, primitiveSourceTypePlan, bingo.PassState, primitiveTypedHIRArtifact) {
	t.Helper()
	snapshot := buildReplayAddSnapshot(t)
	identity := testCompilerIdentity(t, *snapshot)
	plan, err := buildPrimitiveSourceTypePlan(*snapshot)
	if err != nil {
		t.Fatal(err)
	}
	planJSON, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := lowerPrimitiveSourceTypePlan(plan, identity)
	if err != nil {
		t.Fatal(err)
	}
	input := bingo.PassState{Schema: "source-type-plan-v2", Facts: []string{"source-type-plan"}, Artifact: planJSON}
	return identity, plan, input, artifact
}

func primitivePassSpec(t *testing.T, id bingo.PassID) bingo.PassSpec {
	t.Helper()
	for _, spec := range bingo.PassSpecs() {
		if spec.ID == id {
			return spec
		}
	}
	t.Fatalf("missing pass spec %q", id)
	return bingo.PassSpec{}
}

func marshalPrimitivePassTestArtifact(t *testing.T, value primitiveTypedHIRArtifact) json.RawMessage {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func clonePrimitiveTypedHIRTestArtifact(t *testing.T, value primitiveTypedHIRArtifact) primitiveTypedHIRArtifact {
	t.Helper()
	encoded := marshalPrimitivePassTestArtifact(t, value)
	clone, err := decodePrimitiveTypedHIRArtifact(encoded)
	if err != nil {
		t.Fatal(err)
	}
	return clone
}

func rehashPrimitiveHIRTestArtifact(t *testing.T, value *primitiveTypedHIRArtifact) {
	t.Helper()
	module := value.HIR
	module.ContentHash = ""
	encoded, err := json.Marshal(module)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(encoded)
	value.HIR.ContentHash = hex.EncodeToString(digest[:])
}
