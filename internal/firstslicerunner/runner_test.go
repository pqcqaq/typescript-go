package firstslicerunner

import (
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/internal/ast2bingo"
	"github.com/microsoft/typescript-go/internal/bingo"
	"github.com/microsoft/typescript-go/internal/firstsliceoracle"
	"github.com/microsoft/typescript-go/internal/llvmbackend"
)

func TestRunnerReportBindsArtifactsAndObservableOutput(t *testing.T) {
	report := validReport(t)
	if _, err := report.CanonicalBytes(); err != nil {
		t.Fatal(err)
	}
	report.Executions[0].ActualBits = "0000000000000000"
	if err := VerifyReport(report); err == nil || !strings.Contains(err.Error(), "output identity") {
		t.Fatalf("tampered output error = %v", err)
	}
}

func TestRunnerReportRequiresCanonicalExecutionOrder(t *testing.T) {
	report := validReport(t)
	report.Executions = []ExecutionReport{
		{Name: "z", Arguments: []string{"0000000000000000", "0000000000000000"}, ExpectedBits: "0000000000000000", ActualBits: "0000000000000000", OutputHash: hashBytes([]byte("0000000000000000\n")), NodeBits: "0000000000000000", NodeOutputHash: hashBytes([]byte("0000000000000000\n")), OK: true},
		{Name: "a", Arguments: []string{"0000000000000000", "0000000000000000"}, ExpectedBits: "0000000000000000", ActualBits: "0000000000000000", OutputHash: hashBytes([]byte("0000000000000000\n")), NodeBits: "0000000000000000", NodeOutputHash: hashBytes([]byte("0000000000000000\n")), OK: true},
	}
	report.ContentHash, _ = reportContentHash(report)
	if err := VerifyReport(report); err == nil || !strings.Contains(err.Error(), "canonical name order") {
		t.Fatalf("unsorted execution error = %v", err)
	}
}

func TestRunnerReportAcceptsComputeOracleIdentity(t *testing.T) {
	report := validReport(t)
	report.CaseName = "local-assignment-direct-call"
	report.EntryPoint = "compute"
	report.NodeScriptHash = firstsliceoracle.ComputeScriptHash()
	if err := finalizeReport(&report); err != nil {
		t.Fatal(err)
	}
	if _, err := report.CanonicalBytes(); err != nil {
		t.Fatal(err)
	}
}

func validReport(t *testing.T) Report {
	t.Helper()
	identity, err := ast2bingo.NewCompilerBuildIdentity(strings.Repeat("1", 40), strings.Repeat("2", 40))
	if err != nil {
		t.Fatal(err)
	}
	report := Report{
		SchemaVersion:         ReportSchemaVersion,
		Stage:                 "static-core",
		CaseName:              "add-number-number",
		TargetTriple:          llvmbackend.FirstSliceTriple,
		TimeoutMS:             2000,
		NodeVersion:           firstsliceoracle.LockedNodeVersion,
		NodeScriptHash:        firstsliceoracle.ScriptHash(),
		CompilerBuildIdentity: identity,
		Artifacts: ArtifactProvenance{
			FrontendSnapshotHash: strings.Repeat("3", 64), HIRContentHash: strings.Repeat("4", 64),
			BuildPlanHash: strings.Repeat("5", 64), RuntimeManifestHash: strings.Repeat("6", 64),
			MIRContentHash: strings.Repeat("7", 64), LLVMIRHash: strings.Repeat("8", 64), ObjectHash: strings.Repeat("9", 64),
			EmissionContentHash: strings.Repeat("a", 64), ResponseFileHash: strings.Repeat("b", 64),
			LinkMapHash: strings.Repeat("c", 64), ExecutableHash: strings.Repeat("d", 64), LinkContentHash: strings.Repeat("e", 64),
		},
		Executions: []ExecutionReport{{
			Name: "one-plus-two", Arguments: []string{"3ff0000000000000", "4000000000000000"},
			ExpectedBits: "4008000000000000", ActualBits: "4008000000000000", OutputHash: hashBytes([]byte("4008000000000000\n")),
			NodeBits: "4008000000000000", NodeOutputHash: hashBytes([]byte("4008000000000000\n")), OK: true,
		}},
		OK: true,
	}
	if err := bingo.ValidateCompilerBuildIdentity(report.CompilerBuildIdentity); err != nil {
		t.Fatal(err)
	}
	if err := finalizeReport(&report); err != nil {
		t.Fatal(err)
	}
	return report
}
