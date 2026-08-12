package firstslicerunner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/microsoft/typescript-go/internal/bingo"
	"github.com/microsoft/typescript-go/internal/bingomir"
	"github.com/microsoft/typescript-go/internal/firstslicelink"
	"github.com/microsoft/typescript-go/internal/firstsliceoracle"
	"github.com/microsoft/typescript-go/internal/irartifact"
	"github.com/microsoft/typescript-go/internal/llvmbackend"
	"github.com/microsoft/typescript-go/internal/targetcontext"
)

func runVERT013bCase(ctx context.Context, caseData irartifact.Case, identity bingo.CompilerBuildIdentity, machine *llvmbackend.TargetMachine, options Options) (Report, error) {
	result, err := bingomir.ExecuteVERT013b(caseData.Frontend.Program, identity, *caseData.BuildPlan, machine, caseData.RuntimeManifest)
	if err != nil {
		return Report{}, fmt.Errorf("case %s VERT-013b pipeline: %w", caseData.Manifest.Name, err)
	}
	runtimeManifest, err := targetcontext.DecodeRuntimeManifest(caseData.RuntimeManifest)
	if err != nil {
		return Report{}, fmt.Errorf("case %s runtime manifest: %w", caseData.Manifest.Name, err)
	}
	workspace, err := os.MkdirTemp(options.OutputDirectory, "ts2bin-vert013b-")
	if err != nil {
		return Report{}, fmt.Errorf("create VERT-013b workspace: %w", err)
	}
	defer os.RemoveAll(workspace)
	executable := filepath.Join(workspace, "derived-counter-harness")
	linkArtifact, err := firstslicelink.LinkFirstSlice(ctx, firstslicelink.LinkRequest{Emission: result.Emission, Runtime: *runtimeManifest, EntryPoint: "derivedCounter", RuntimeDirectory: options.RuntimeDirectory, RuntimeArchivePath: options.RuntimeArchivePath, OutputPath: executable, Clang: options.Clang, LLD: options.LLD})
	if err != nil {
		return Report{}, fmt.Errorf("case %s link: %w", caseData.Manifest.Name, err)
	}
	nodeOracle, err := firstsliceoracle.OpenNode(ctx, options.Node)
	if err != nil {
		return Report{}, fmt.Errorf("case %s Node oracle: %w", caseData.Manifest.Name, err)
	}
	executions := slices.Clone(caseData.Manifest.Executions)
	slices.SortFunc(executions, func(left, right irartifact.CaseExecution) int { return strings.Compare(left.Name, right.Name) })
	reports := make([]ExecutionReport, 0, len(executions))
	allOK := true
	for _, execution := range executions {
		native, err := firstslicelink.RunDerivedCounter(ctx, executable, execution.LeftBits, execution.RightBits)
		if err != nil {
			return Report{}, fmt.Errorf("case %s execution %s: %w", caseData.Manifest.Name, execution.Name, err)
		}
		node, err := nodeOracle.DerivedCounter(ctx, execution.LeftBits, execution.RightBits)
		if err != nil {
			return Report{}, fmt.Errorf("case %s execution %s Node oracle: %w", caseData.Manifest.Name, execution.Name, err)
		}
		actual, nodeBits := strings.TrimSuffix(string(native.Output), "\n"), strings.TrimSuffix(string(node.Output), "\n")
		ok := actual == execution.ExpectedBits && nodeBits == execution.ExpectedBits && actual == nodeBits
		allOK = allOK && ok
		reports = append(reports, ExecutionReport{Name: execution.Name, Arguments: slices.Clone(native.Arguments), ExpectedBits: execution.ExpectedBits, ActualBits: actual, OutputHash: native.OutputHash, NodeBits: nodeBits, NodeOutputHash: node.OutputHash, OK: ok})
	}
	report := Report{SchemaVersion: ReportSchemaVersion, Stage: "static-core", CaseName: caseData.Manifest.Name, EntryPoint: "derivedCounter", OracleProgram: "derivedcounter", TargetTriple: runtimeManifest.Target.Triple, TimeoutMS: caseData.Manifest.TimeoutMS, NodeVersion: nodeOracle.Version(), NodeScriptHash: firstsliceoracle.DerivedCounterScriptHash(), CompilerBuildIdentity: identity, Artifacts: ArtifactProvenance{FrontendSnapshotHash: caseData.Frontend.ContentHash, HIRContentHash: result.Replay.HIR.ContentHash, BuildPlanHash: caseData.BuildPlan.ContentHash, RuntimeManifestHash: runtimeManifest.ContentHash, MIRContentHash: result.BoundMIR.ContentHash, LLVMIRHash: result.Emission.LLVMIRHash, ObjectHash: result.Emission.ObjectHash, EmissionContentHash: result.Emission.ContentHash, ResponseFileHash: linkArtifact.ResponseFileHash, LinkMapHash: linkArtifact.LinkMapHash, ExecutableHash: linkArtifact.ExecutableHash, LinkContentHash: linkArtifact.ContentHash}, Executions: reports, OK: allOK}
	if err := finalizeReport(&report); err != nil {
		return Report{}, err
	}
	if !report.OK {
		return report, fmt.Errorf("case %s observable output mismatch", report.CaseName)
	}
	return report, nil
}
