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

func TestPrimitiveLoweringHashIsStableAndBoundToHIRV5(t *testing.T) {
	first := PrimitiveLoweringHash()
	second := PrimitiveLoweringHash()
	if first != second || len(first) != 64 || PrimitiveLoweringSchema != "bingo-hir-lowering-v5" || bingo.HIRSchemaVersion != 5 {
		t.Fatalf("lowering identity = %q / %q / %q / HIR %d", first, second, PrimitiveLoweringSchema, bingo.HIRSchemaVersion)
	}
}

func TestCompilerBuildIdentityRejectsMissingForkInjection(t *testing.T) {
	if _, err := NewCompilerBuildIdentity(strings.Repeat("1", 40), ""); err == nil {
		t.Fatal("compiler identity without fork commit was accepted")
	}
}
