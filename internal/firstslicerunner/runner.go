// Package firstslicerunner owns the REL-001a first-slice case orchestration.
package firstslicerunner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/microsoft/typescript-go/internal/bingo"
	"github.com/microsoft/typescript-go/internal/firstslicelink"
	"github.com/microsoft/typescript-go/internal/firstsliceoracle"
	"github.com/microsoft/typescript-go/internal/irartifact"
	"github.com/microsoft/typescript-go/internal/llvmbackend"
	"github.com/microsoft/typescript-go/internal/targetcontext"
)

const ReportSchemaVersion uint32 = 2

type Options struct {
	RuntimeDirectory   string
	RuntimeArchivePath string
	OutputDirectory    string
	Clang              string
	LLD                string
	Node               string
}

type ArtifactProvenance struct {
	FrontendSnapshotHash string `json:"frontendSnapshotHash"`
	HIRContentHash       string `json:"hirContentHash"`
	BuildPlanHash        string `json:"buildPlanHash"`
	RuntimeManifestHash  string `json:"runtimeManifestHash"`
	MIRContentHash       string `json:"mirContentHash"`
	LLVMIRHash           string `json:"llvmIrHash"`
	ObjectHash           string `json:"objectHash"`
	EmissionContentHash  string `json:"emissionContentHash"`
	ResponseFileHash     string `json:"responseFileHash"`
	LinkMapHash          string `json:"linkMapHash"`
	ExecutableHash       string `json:"executableHash"`
	LinkContentHash      string `json:"linkContentHash"`
}

type ExecutionReport struct {
	Name           string   `json:"name"`
	Arguments      []string `json:"arguments"`
	ExpectedBits   string   `json:"expectedBits"`
	ActualBits     string   `json:"actualBits"`
	OutputHash     string   `json:"outputHash"`
	NodeBits       string   `json:"nodeBits"`
	NodeOutputHash string   `json:"nodeOutputHash"`
	OK             bool     `json:"ok"`
}

type BoundaryRejectionReport struct {
	Name       string   `json:"name"`
	Arguments  []string `json:"arguments"`
	OutputHash string   `json:"outputHash"`
	Rejected   bool     `json:"rejected"`
}

type Report struct {
	SchemaVersion         uint32                      `json:"schemaVersion"`
	Stage                 string                      `json:"stage"`
	CaseName              string                      `json:"caseName"`
	EntryPoint            string                      `json:"entryPoint"`
	TargetTriple          string                      `json:"targetTriple"`
	TimeoutMS             uint32                      `json:"timeoutMs"`
	NodeVersion           string                      `json:"nodeVersion"`
	NodeScriptHash        string                      `json:"nodeScriptHash"`
	NonCanonicalRejected  bool                        `json:"nonCanonicalRejected,omitempty"`
	CompilerBuildIdentity bingo.CompilerBuildIdentity `json:"compilerBuildIdentity"`
	Artifacts             ArtifactProvenance          `json:"artifacts"`
	Executions            []ExecutionReport           `json:"executions"`
	BoundaryRejections    []BoundaryRejectionReport   `json:"boundaryRejections,omitempty"`
	OK                    bool                        `json:"ok"`
	ContentHash           string                      `json:"contentHash"`
}

// RunCase executes one self-contained first-slice manifest under a single
// timeout and records every compiler/link/process artifact identity.
func RunCase(ctx context.Context, directory string, identity bingo.CompilerBuildIdentity, machine *llvmbackend.TargetMachine, options Options) (Report, error) {
	if ctx == nil {
		return Report{}, fmt.Errorf("runner context is nil")
	}
	if machine == nil {
		return Report{}, fmt.Errorf("LLVM TargetMachine is required")
	}
	if err := bingo.ValidateCompilerBuildIdentity(identity); err != nil {
		return Report{}, fmt.Errorf("compiler identity: %w", err)
	}
	caseData, err := irartifact.LoadCase(directory, true)
	if err != nil {
		return Report{}, err
	}
	if err := irartifact.ValidateRunnableManifest(caseData.Manifest); err != nil {
		return Report{}, err
	}
	if err := validateOptions(options); err != nil {
		return Report{}, err
	}
	caseContext, cancel := context.WithTimeout(ctx, time.Duration(caseData.Manifest.TimeoutMS)*time.Millisecond)
	defer cancel()

	hir, err := irartifact.LoadHIR(directory, identity)
	if err != nil {
		return Report{}, fmt.Errorf("case %s HIR: %w", caseData.Manifest.Name, err)
	}
	mir, err := irartifact.LoadMIR(caseContext, directory, identity, machine)
	if err != nil {
		return Report{}, fmt.Errorf("case %s MIR: %w", caseData.Manifest.Name, err)
	}
	emission, err := machine.EmitFirstSliceObject(mir)
	if err != nil {
		return Report{}, fmt.Errorf("case %s LLVM emission: %w", caseData.Manifest.Name, err)
	}
	runtimeManifest, err := targetcontext.DecodeRuntimeManifest(caseData.RuntimeManifest)
	if err != nil {
		return Report{}, fmt.Errorf("case %s runtime manifest: %w", caseData.Manifest.Name, err)
	}
	workspace, err := os.MkdirTemp(options.OutputDirectory, "ts2bin-static-core-")
	if err != nil {
		return Report{}, fmt.Errorf("create case workspace: %w", err)
	}
	defer os.RemoveAll(workspace)
	entryPoint := caseData.Manifest.EntryPoint
	if entryPoint == "" {
		entryPoint = "add"
	}
	executable := filepath.Join(workspace, entryPoint+"-harness")
	linkArtifact, err := firstslicelink.LinkFirstSlice(caseContext, firstslicelink.LinkRequest{
		Emission:           emission,
		Runtime:            *runtimeManifest,
		EntryPoint:         entryPoint,
		RuntimeDirectory:   options.RuntimeDirectory,
		RuntimeArchivePath: options.RuntimeArchivePath,
		OutputPath:         executable,
		Clang:              options.Clang,
		LLD:                options.LLD,
	})
	if err != nil {
		return Report{}, fmt.Errorf("case %s link: %w", caseData.Manifest.Name, err)
	}
	nodeOracle, err := firstsliceoracle.OpenNode(caseContext, options.Node)
	if err != nil {
		return Report{}, fmt.Errorf("case %s Node oracle: %w", caseData.Manifest.Name, err)
	}

	executions := slices.Clone(caseData.Manifest.Executions)
	slices.SortFunc(executions, func(left, right irartifact.CaseExecution) int { return strings.Compare(left.Name, right.Name) })
	executionReports := make([]ExecutionReport, 0, len(executions))
	allOK := true
	for _, execution := range executions {
		var result firstslicelink.RunResult
		if entryPoint == "choose" {
			result, err = firstslicelink.RunChoose(caseContext, executable, *execution.Flag, execution.LeftBits, execution.RightBits)
		} else {
			result, err = firstslicelink.RunFirstSlice(caseContext, executable, execution.LeftBits, execution.RightBits)
		}
		if err != nil {
			return Report{}, fmt.Errorf("case %s execution %s: %w", caseData.Manifest.Name, execution.Name, err)
		}
		actual := strings.TrimSuffix(string(result.Output), "\n")
		var nodeResult firstsliceoracle.Result
		if entryPoint == "choose" {
			nodeResult, err = nodeOracle.Choose(caseContext, *execution.Flag, execution.LeftBits, execution.RightBits)
		} else {
			nodeResult, err = nodeOracle.Add(caseContext, execution.LeftBits, execution.RightBits)
		}
		if err != nil {
			return Report{}, fmt.Errorf("case %s execution %s Node oracle: %w", caseData.Manifest.Name, execution.Name, err)
		}
		nodeBits := strings.TrimSuffix(string(nodeResult.Output), "\n")
		ok := actual == execution.ExpectedBits && nodeBits == execution.ExpectedBits && actual == nodeBits
		allOK = allOK && ok
		executionReports = append(executionReports, ExecutionReport{
			Name: execution.Name, Arguments: slices.Clone(result.Arguments), ExpectedBits: execution.ExpectedBits,
			ActualBits: actual, OutputHash: result.OutputHash, NodeBits: nodeBits, NodeOutputHash: nodeResult.OutputHash, OK: ok,
		})
	}
	nonCanonicalRejected := false
	var boundaryRejections []BoundaryRejectionReport
	if entryPoint == "choose" {
		result, err := firstslicelink.RejectNonCanonicalChoose(caseContext, executable, executions[0].LeftBits, executions[0].RightBits)
		if err != nil {
			return Report{}, fmt.Errorf("case %s noncanonical boolean rejection: %w", caseData.Manifest.Name, err)
		}
		nonCanonicalRejected = true
		boundaryRejections = []BoundaryRejectionReport{{
			Name: "reject-noncanonical-boolean-byte", Arguments: slices.Clone(result.Arguments),
			OutputHash: result.OutputHash, Rejected: true,
		}}
	}
	nodeScriptHash := nodeOracle.ScriptHash()
	if entryPoint == "choose" {
		nodeScriptHash = firstsliceoracle.ChooseScriptHash()
	}
	report := Report{
		SchemaVersion:         ReportSchemaVersion,
		Stage:                 "static-core",
		CaseName:              caseData.Manifest.Name,
		EntryPoint:            entryPoint,
		TargetTriple:          runtimeManifest.Target.Triple,
		TimeoutMS:             caseData.Manifest.TimeoutMS,
		NodeVersion:           nodeOracle.Version(),
		NodeScriptHash:        nodeScriptHash,
		NonCanonicalRejected:  nonCanonicalRejected,
		CompilerBuildIdentity: identity,
		Artifacts: ArtifactProvenance{
			FrontendSnapshotHash: caseData.Frontend.ContentHash,
			HIRContentHash:       hir.ContentHash,
			BuildPlanHash:        caseData.BuildPlan.ContentHash,
			RuntimeManifestHash:  runtimeManifest.ContentHash,
			MIRContentHash:       mir.ContentHash,
			LLVMIRHash:           emission.LLVMIRHash,
			ObjectHash:           emission.ObjectHash,
			EmissionContentHash:  emission.ContentHash,
			ResponseFileHash:     linkArtifact.ResponseFileHash,
			LinkMapHash:          linkArtifact.LinkMapHash,
			ExecutableHash:       linkArtifact.ExecutableHash,
			LinkContentHash:      linkArtifact.ContentHash,
		},
		Executions:         executionReports,
		BoundaryRejections: boundaryRejections,
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

func (report Report) CanonicalBytes() ([]byte, error) {
	if err := VerifyReport(report); err != nil {
		return nil, err
	}
	return json.Marshal(report)
}

func VerifyReport(report Report) error {
	entryPoint := report.EntryPoint
	if entryPoint == "" {
		entryPoint = "add"
	}
	if report.SchemaVersion != ReportSchemaVersion || report.Stage != "static-core" || strings.TrimSpace(report.CaseName) == "" || (entryPoint != "add" && entryPoint != "choose") {
		return fmt.Errorf("unsupported first-slice runner report identity")
	}
	wantScriptHash := firstsliceoracle.ScriptHash()
	if entryPoint == "choose" {
		wantScriptHash = firstsliceoracle.ChooseScriptHash()
	}
	if report.TargetTriple != llvmbackend.FirstSliceTriple || report.TimeoutMS == 0 || report.TimeoutMS > 60_000 ||
		report.NodeVersion != firstsliceoracle.LockedNodeVersion || report.NodeScriptHash != wantScriptHash {
		return fmt.Errorf("invalid first-slice runner target or timeout")
	}
	if report.NonCanonicalRejected != (entryPoint == "choose") {
		return fmt.Errorf("noncanonical boolean rejection does not match entry point")
	}
	if entryPoint == "add" && len(report.BoundaryRejections) != 0 {
		return fmt.Errorf("add report contains boolean boundary rejections")
	}
	if entryPoint == "choose" {
		if len(report.BoundaryRejections) != 1 {
			return fmt.Errorf("choose report must contain one boolean boundary rejection")
		}
		rejection := report.BoundaryRejections[0]
		if rejection.Name != "reject-noncanonical-boolean-byte" || len(rejection.Arguments) != 3 || rejection.Arguments[0] != "02" || !isBits(rejection.Arguments[1]) || !isBits(rejection.Arguments[2]) || rejection.OutputHash != hashBytes(nil) || !rejection.Rejected {
			return fmt.Errorf("invalid noncanonical boolean rejection report")
		}
	}
	if err := bingo.ValidateCompilerBuildIdentity(report.CompilerBuildIdentity); err != nil {
		return err
	}
	for name, digest := range map[string]string{
		"frontend snapshot": report.Artifacts.FrontendSnapshotHash,
		"HIR":               report.Artifacts.HIRContentHash,
		"build plan":        report.Artifacts.BuildPlanHash,
		"runtime manifest":  report.Artifacts.RuntimeManifestHash,
		"MIR":               report.Artifacts.MIRContentHash,
		"LLVM IR":           report.Artifacts.LLVMIRHash,
		"object":            report.Artifacts.ObjectHash,
		"emission":          report.Artifacts.EmissionContentHash,
		"response file":     report.Artifacts.ResponseFileHash,
		"link map":          report.Artifacts.LinkMapHash,
		"executable":        report.Artifacts.ExecutableHash,
		"link":              report.Artifacts.LinkContentHash,
		"content":           report.ContentHash,
	} {
		if !isDigest(digest) {
			return fmt.Errorf("invalid %s digest %q", name, digest)
		}
	}
	if len(report.Executions) == 0 {
		return fmt.Errorf("runner report has no executions")
	}
	allOK := true
	for index, execution := range report.Executions {
		wantArguments := 2
		if entryPoint == "choose" {
			wantArguments = 3
		}
		if strings.TrimSpace(execution.Name) == "" || len(execution.Arguments) != wantArguments || !isBits(execution.ExpectedBits) || !isBits(execution.ActualBits) || !isBits(execution.NodeBits) {
			return fmt.Errorf("invalid execution report at index %d", index)
		}
		if index > 0 && report.Executions[index-1].Name >= execution.Name {
			return fmt.Errorf("execution reports are not in canonical name order")
		}
		if execution.Arguments[0] == "" || execution.Arguments[1] == "" || hashBytes([]byte(execution.ActualBits+"\n")) != execution.OutputHash {
			return fmt.Errorf("execution %q output identity is invalid", execution.Name)
		}
		if hashBytes([]byte(execution.NodeBits+"\n")) != execution.NodeOutputHash {
			return fmt.Errorf("execution %q Node output identity is invalid", execution.Name)
		}
		if execution.OK != (execution.ExpectedBits == execution.ActualBits && execution.ExpectedBits == execution.NodeBits) {
			return fmt.Errorf("execution %q result flag is invalid", execution.Name)
		}
		allOK = allOK && execution.OK
	}
	if report.OK != allOK {
		return fmt.Errorf("runner report OK flag does not match executions")
	}
	want, err := reportContentHash(report)
	if err != nil {
		return err
	}
	if report.ContentHash != want {
		return fmt.Errorf("runner report content hash mismatch: got %s want %s", report.ContentHash, want)
	}
	return nil
}

func validateOptions(options Options) error {
	for name, path := range map[string]string{
		"runtime directory": options.RuntimeDirectory,
		"runtime archive":   options.RuntimeArchivePath,
		"output directory":  options.OutputDirectory,
	} {
		if strings.TrimSpace(path) == "" || !filepath.IsAbs(path) {
			return fmt.Errorf("%s must be an absolute path", name)
		}
	}
	if strings.TrimSpace(options.Clang) == "" || strings.TrimSpace(options.LLD) == "" || strings.TrimSpace(options.Node) == "" {
		return firstslicelink.ErrLinkUnavailable
	}
	return nil
}

func finalizeReport(report *Report) error {
	var err error
	report.ContentHash, err = reportContentHash(*report)
	if err != nil {
		return err
	}
	return VerifyReport(*report)
}

func reportContentHash(report Report) (string, error) {
	data, err := json.Marshal(struct {
		SchemaVersion         uint32                      `json:"schemaVersion"`
		Stage                 string                      `json:"stage"`
		CaseName              string                      `json:"caseName"`
		EntryPoint            string                      `json:"entryPoint"`
		TargetTriple          string                      `json:"targetTriple"`
		TimeoutMS             uint32                      `json:"timeoutMs"`
		NodeVersion           string                      `json:"nodeVersion"`
		NodeScriptHash        string                      `json:"nodeScriptHash"`
		NonCanonicalRejected  bool                        `json:"nonCanonicalRejected,omitempty"`
		CompilerBuildIdentity bingo.CompilerBuildIdentity `json:"compilerBuildIdentity"`
		Artifacts             ArtifactProvenance          `json:"artifacts"`
		Executions            []ExecutionReport           `json:"executions"`
		BoundaryRejections    []BoundaryRejectionReport   `json:"boundaryRejections,omitempty"`
		OK                    bool                        `json:"ok"`
	}{report.SchemaVersion, report.Stage, report.CaseName, report.EntryPoint, report.TargetTriple, report.TimeoutMS, report.NodeVersion, report.NodeScriptHash, report.NonCanonicalRejected, report.CompilerBuildIdentity, report.Artifacts, report.Executions, report.BoundaryRejections, report.OK})
	if err != nil {
		return "", err
	}
	return hashBytes(data), nil
}

func hashBytes(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func isDigest(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	for _, char := range value {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}

func isBits(value string) bool {
	if len(value) != 16 {
		return false
	}
	for _, char := range value {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}
