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

func runVERT011Case(ctx context.Context, caseData irartifact.Case, identity bingo.CompilerBuildIdentity, machine *llvmbackend.TargetMachine, options Options) (Report, error) {
	result, err := bingomir.ExecuteVERT011(caseData.Frontend.Program, identity, *caseData.BuildPlan, machine, caseData.RuntimeManifest)
	if err != nil {
		return Report{}, fmt.Errorf("case %s VERT-011 pipeline: %w", caseData.Manifest.Name, err)
	}
	runtimeManifest, err := targetcontext.DecodeRuntimeManifest(caseData.RuntimeManifest)
	if err != nil {
		return Report{}, fmt.Errorf("case %s runtime manifest: %w", caseData.Manifest.Name, err)
	}
	workspace, err := os.MkdirTemp(options.OutputDirectory, "ts2bin-vert011-")
	if err != nil {
		return Report{}, fmt.Errorf("create VERT-011 workspace: %w", err)
	}
	defer os.RemoveAll(workspace)
	executable := filepath.Join(workspace, "property-nullish-assign-harness")
	linkArtifact, err := firstslicelink.LinkFirstSlice(ctx, firstslicelink.LinkRequest{
		Emission: result.Emission, Runtime: *runtimeManifest, EntryPoint: "propertyNullishAssign",
		RuntimeDirectory: options.RuntimeDirectory, RuntimeArchivePath: options.RuntimeArchivePath,
		OutputPath: executable, Clang: options.Clang, LLD: options.LLD,
	})
	if err != nil {
		return Report{}, fmt.Errorf("case %s link: %w", caseData.Manifest.Name, err)
	}
	nodeOracle, err := firstsliceoracle.OpenNode(ctx, options.Node)
	if err != nil {
		return Report{}, fmt.Errorf("case %s Node oracle: %w", caseData.Manifest.Name, err)
	}
	executions := slices.Clone(caseData.Manifest.Executions)
	slices.SortFunc(executions, func(left, right irartifact.CaseExecution) int { return strings.Compare(left.Name, right.Name) })
	executionReports := make([]ExecutionReport, 0, len(executions))
	allOK := true
	for _, execution := range executions {
		native, err := firstslicelink.RunPropertyNullishAssign(ctx, executable, execution.NullableTag, execution.LeftBits)
		if err != nil {
			return Report{}, fmt.Errorf("case %s execution %s: %w", caseData.Manifest.Name, execution.Name, err)
		}
		node, err := nodeOracle.PropertyNullishAssign(ctx, execution.NullableTag, execution.LeftBits)
		if err != nil {
			return Report{}, fmt.Errorf("case %s execution %s Node oracle: %w", caseData.Manifest.Name, execution.Name, err)
		}
		actual := strings.TrimSuffix(string(native.Output), "\n")
		nodeBits := strings.TrimSuffix(string(node.Output), "\n")
		ok := actual == execution.ExpectedBits && nodeBits == execution.ExpectedBits && actual == nodeBits
		allOK = allOK && ok
		executionReports = append(executionReports, ExecutionReport{
			Name: execution.Name, Arguments: slices.Clone(native.Arguments), ExpectedBits: execution.ExpectedBits,
			ActualBits: actual, OutputHash: native.OutputHash, NodeBits: nodeBits, NodeOutputHash: node.OutputHash, OK: ok,
		})
	}
	rejection, err := firstslicelink.RejectNonCanonicalPropertyNullishAssign(ctx, executable, "0000000000000000")
	if err != nil {
		return Report{}, fmt.Errorf("case %s noncanonical nullable rejection: %w", caseData.Manifest.Name, err)
	}
	report := Report{
		SchemaVersion: ReportSchemaVersion, Stage: "static-core", CaseName: caseData.Manifest.Name,
		EntryPoint: "propertyNullishAssign", OracleProgram: "propertynullishassign",
		TargetTriple: runtimeManifest.Target.Triple, TimeoutMS: caseData.Manifest.TimeoutMS,
		NodeVersion: nodeOracle.Version(), NodeScriptHash: firstsliceoracle.PropertyNullishAssignScriptHash(),
		NonCanonicalRejected: true, CompilerBuildIdentity: identity,
		Artifacts: ArtifactProvenance{
			FrontendSnapshotHash: caseData.Frontend.ContentHash, HIRContentHash: result.Replay.HIR.ContentHash,
			BuildPlanHash: caseData.BuildPlan.ContentHash, RuntimeManifestHash: runtimeManifest.ContentHash,
			MIRContentHash: result.BoundMIR.ContentHash, LLVMIRHash: result.Emission.LLVMIRHash,
			ObjectHash: result.Emission.ObjectHash, EmissionContentHash: result.Emission.ContentHash,
			ResponseFileHash: linkArtifact.ResponseFileHash, LinkMapHash: linkArtifact.LinkMapHash,
			ExecutableHash: linkArtifact.ExecutableHash, LinkContentHash: linkArtifact.ContentHash,
		},
		Executions:         executionReports,
		BoundaryRejections: []BoundaryRejectionReport{{Name: "reject-noncanonical-nullable-tag", Arguments: slices.Clone(rejection.Arguments), OutputHash: rejection.OutputHash, Rejected: true}},
		OK:                 allOK,
	}
	if err := finalizeReport(&report); err != nil {
		return Report{}, err
	}
	if !report.OK {
		return report, fmt.Errorf("case %s observable output mismatch", report.CaseName)
	}
	return report, nil
}
