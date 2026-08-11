package applicationbuild

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/internal/ast2bingo"
	"github.com/microsoft/typescript-go/internal/bingo"
	"github.com/microsoft/typescript-go/internal/llvmbackend"
	"github.com/microsoft/typescript-go/internal/tsfrontend"
)

func validReportForTest() Report {
	digest := strings.Repeat("a", 64)
	report := Report{
		SchemaVersion: ReportSchemaVersion,
		Stage:         "application-build-preview",
		EntryPoint:    "main",
		TargetTriple:  llvmbackend.FirstSliceTriple,
		CompilerBuildIdentity: bingo.CompilerBuildIdentity{
			UpstreamCommit: strings.Repeat("1", 40), ForkCommit: strings.Repeat("2", 40),
			LoweringSchema: "bingo-hir-lowering-v8", LoweringHash: strings.Repeat("3", 64),
		},
		Artifacts: ArtifactProvenance{
			FrontendSnapshotHash: digest, HIRContentHash: digest, BuildPlanHash: digest,
			RuntimeManifestHash: digest, MIRContentHash: digest, LLVMIRHash: digest,
			ObjectHash: digest, EmissionContentHash: digest, ResponseFileHash: digest,
			LinkMapHash: digest, ExecutableHash: digest, LinkContentHash: digest,
		},
	}
	report.ContentHash, _ = reportContentHash(report)
	return report
}

func TestApplicationBuildReportIsCanonicalAndRejectsTampering(t *testing.T) {
	report := validReportForTest()
	first, err := report.CanonicalBytes()
	if err != nil {
		t.Fatal(err)
	}
	second, err := report.CanonicalBytes()
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatal("canonical application reports differ")
	}

	for _, test := range []struct {
		name   string
		mutate func(*Report)
	}{
		{name: "schema", mutate: func(value *Report) { value.SchemaVersion++ }},
		{name: "entry point", mutate: func(value *Report) { value.EntryPoint = "add" }},
		{name: "artifact digest", mutate: func(value *Report) { value.Artifacts.ExecutableHash = "bad" }},
		{name: "content hash", mutate: func(value *Report) { value.ContentHash = strings.Repeat("f", 64) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate := report
			test.mutate(&candidate)
			if err := VerifyReport(candidate); err == nil {
				t.Fatal("tampered application report was accepted")
			}
		})
	}
}

func TestApplicationBuildRejectsInvalidEntrypointsWithoutPublishingArtifacts(t *testing.T) {
	identity, err := ast2bingo.NewCompilerBuildIdentity(tsfrontend.TypeScriptGoCommit, strings.Repeat("2", 40))
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		source string
	}{
		{name: "missing main", source: `export function compute(value: number): number { return value; }`},
		{name: "duplicate main", source: `export function main(): number { return 0; } export function main(): number { return 1; }`},
		{name: "non-exported main", source: `function main(): number { return 0; }`},
		{name: "parameterized main", source: `export function main(value: number): number { return value; }`},
		{name: "wrong return type", source: `export function main(): boolean { return true; }`},
		{name: "missing return", source: `export function main(): number { }`},
	} {
		t.Run(test.name, func(t *testing.T) {
			project := t.TempDir()
			if err := os.WriteFile(filepath.Join(project, "tsconfig.json"), []byte(`{"compilerOptions":{"strict":true},"files":["main.ts"]}`), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(project, "main.ts"), []byte(test.source), 0o600); err != nil {
				t.Fatal(err)
			}
			output := filepath.Join(project, "application")
			_, err := Build(context.Background(), identity, nil, Request{
				ConfigPath: filepath.Join(project, "tsconfig.json"), OutputPath: output,
				RuntimeDirectory: filepath.Join(project, "runtime"), RuntimeArchivePath: filepath.Join(project, "runtime", "libbingo_runtime.a"),
				Clang: "clang-20", LLD: "ld.lld-20",
			})
			if err == nil {
				t.Fatal("invalid application entrypoint was accepted")
			}
			for _, path := range []string{output, output + ".report.json"} {
				if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
					t.Fatalf("rejected application published %s: %v", path, statErr)
				}
			}
		})
	}
}
