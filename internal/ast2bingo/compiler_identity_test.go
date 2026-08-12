package ast2bingo

import (
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/internal/bingo"
)

func testCompilerIdentity(t testing.TB, snapshot ProgramSnapshot) bingo.CompilerBuildIdentity {
	t.Helper()
	identity, err := NewCompilerBuildIdentity(
		snapshot.Provenance.TypeScriptGoCommit,
		snapshot.Provenance.TypeScriptGoCommit,
	)
	if err != nil {
		t.Fatal(err)
	}
	return identity
}

func TestPrimitiveLoweringHashIsStableAndBoundToHIRV8(t *testing.T) {
	first := PrimitiveLoweringHash()
	second := PrimitiveLoweringHash()
	if first != second || len(first) != 64 || PrimitiveLoweringSchema != "bingo-hir-lowering-v8" || bingo.HIRSchemaVersion != 8 {
		t.Fatalf("lowering identity = %q / %q / %q / HIR %d", first, second, PrimitiveLoweringSchema, bingo.HIRSchemaVersion)
	}
}

func TestPrimitiveLoweringHashIncludesCheckedObjectCastReplay(t *testing.T) {
	identity, err := NewCompilerBuildIdentity(strings.Repeat("a", 40), strings.Repeat("b", 40))
	if err != nil {
		t.Fatal(err)
	}
	if identity.LoweringHash != PrimitiveLoweringHash() || len(identity.LoweringHash) != 64 {
		t.Fatalf("invalid lowering hash %q", identity.LoweringHash)
	}
	if _, err := primitiveLoweringSources.ReadFile("checked_object_cast_replay.go"); err != nil {
		t.Fatalf("checked-cast replay is not identity-bound: %v", err)
	}
	if _, err := primitiveLoweringSources.ReadFile("function_thunk_replay.go"); err != nil {
		t.Fatalf("function-thunk replay is not identity-bound: %v", err)
	}
	if _, err := primitiveLoweringSources.ReadFile("property_access_admission_replay.go"); err != nil {
		t.Fatalf("property-access admission replay is not identity-bound: %v", err)
	}
}

func TestCompilerBuildIdentityRejectsMissingForkInjection(t *testing.T) {
	if _, err := NewCompilerBuildIdentity(strings.Repeat("1", 40), ""); err == nil {
		t.Fatal("compiler identity without fork commit was accepted")
	}
}
