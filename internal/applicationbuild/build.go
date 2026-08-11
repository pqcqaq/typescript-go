// Package applicationbuild owns the Phase 2B real-source application build preview.
package applicationbuild

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/microsoft/typescript-go/internal/ast2bingo"
	"github.com/microsoft/typescript-go/internal/bingo"
	"github.com/microsoft/typescript-go/internal/bingomir"
	"github.com/microsoft/typescript-go/internal/firstslicelink"
	"github.com/microsoft/typescript-go/internal/llvmbackend"
	"github.com/microsoft/typescript-go/internal/targetcontext"
	"github.com/microsoft/typescript-go/internal/tsfrontend"
)

// ReportSchemaVersion identifies the deterministic application build report.
const ReportSchemaVersion uint32 = 1

// Request names the real project and locked link inputs used by one build.
type Request struct {
	ConfigPath         string
	OutputPath         string
	RuntimeDirectory   string
	RuntimeArchivePath string
	Clang              string
	LLD                string
}

// ArtifactProvenance records every authenticated compiler and linker output.
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

// Report is the canonical provenance result for a successful application build.
type Report struct {
	SchemaVersion         uint32                      `json:"schemaVersion"`
	Stage                 string                      `json:"stage"`
	EntryPoint            string                      `json:"entryPoint"`
	TargetTriple          string                      `json:"targetTriple"`
	CompilerBuildIdentity bingo.CompilerBuildIdentity `json:"compilerBuildIdentity"`
	Artifacts             ArtifactProvenance          `json:"artifacts"`
	ContentHash           string                      `json:"contentHash"`
}

// DiagnosticsError preserves source diagnostics without flattening them into
// a backend or environment failure.
type DiagnosticsError struct {
	Diagnostics []tsfrontend.Diagnostic
}

func (err *DiagnosticsError) Error() string {
	return fmt.Sprintf("frontend rejected application source with %d diagnostic(s)", len(err.Diagnostics))
}

// Build executes the real source-to-ELF preview and publishes OutputPath only
// after every frontend, HIR, MIR, LLVM, runtime and link verifier succeeds.
func Build(ctx context.Context, identity bingo.CompilerBuildIdentity, machine *llvmbackend.TargetMachine, request Request) (Report, error) {
	if ctx == nil {
		return Report{}, fmt.Errorf("application build context is nil")
	}
	if err := validateRequest(request); err != nil {
		return Report{}, err
	}
	snapshot, diagnostics := tsfrontend.NewOSFrontend(tsfrontend.TypeScriptGoCommit).Build(ctx, tsfrontend.BuildRequest{
		ConfigPath: request.ConfigPath,
	})
	if tsfrontend.DiagnosticsHaveErrors(diagnostics) || snapshot == nil {
		return Report{}, &DiagnosticsError{Diagnostics: diagnostics}
	}
	frontend, err := tsfrontend.NewFrontendSnapshot(*snapshot)
	if err != nil {
		return Report{}, fmt.Errorf("validate frontend snapshot: %w", err)
	}
	options := tsfrontend.DefaultBingoOptions()
	options.Profile = frontend.Program.Config.Bingo.Profile
	options.TargetTriple = llvmbackend.FirstSliceTriple
	plan, err := tsfrontend.ResolveBuildPlan(frontend, options)
	if err != nil {
		return Report{}, fmt.Errorf("resolve application build plan: %w", err)
	}

	replay, err := ast2bingo.ReplaySnapshot(frontend.Program, identity)
	if err != nil {
		return Report{}, fmt.Errorf("lower application HIR: %w", err)
	}
	if len(replay.HIR.Functions) != 1 || replay.HIR.Functions[0].Name != "main" || !replay.HIR.Functions[0].Exported {
		return Report{}, fmt.Errorf("application build requires exactly one exported main entrypoint")
	}

	runtimeManifestPath := filepath.Join(request.RuntimeDirectory, "runtime-manifest.json")
	runtimeBytes, err := os.ReadFile(runtimeManifestPath)
	if err != nil {
		return Report{}, fmt.Errorf("read locked runtime manifest: %w", err)
	}
	runtimeManifest, err := targetcontext.DecodeRuntimeManifest(runtimeBytes)
	if err != nil {
		return Report{}, fmt.Errorf("decode locked runtime manifest: %w", err)
	}
	mir, _, err := bingomir.ExecuteFirstSliceMIR(ctx, frontend.Program, identity, plan, machine, runtimeBytes)
	if err != nil {
		return Report{}, fmt.Errorf("lower application MIR: %w", err)
	}
	if len(mir.Functions) != 1 || mir.Functions[0].Name != "main" || !mir.Functions[0].Exported {
		return Report{}, fmt.Errorf("verified MIR does not contain the application entrypoint")
	}
	emission, err := machine.EmitFirstSliceObject(mir)
	if err != nil {
		return Report{}, fmt.Errorf("emit application object: %w", err)
	}
	link, err := firstslicelink.LinkFirstSlice(ctx, firstslicelink.LinkRequest{
		Emission: emission, Runtime: *runtimeManifest, EntryPoint: "main",
		RuntimeDirectory: request.RuntimeDirectory, RuntimeArchivePath: request.RuntimeArchivePath,
		OutputPath: request.OutputPath, Clang: request.Clang, LLD: request.LLD,
	})
	if err != nil {
		return Report{}, fmt.Errorf("link application executable: %w", err)
	}
	report := Report{
		SchemaVersion:         ReportSchemaVersion,
		Stage:                 "application-build-preview",
		EntryPoint:            "main",
		TargetTriple:          llvmbackend.FirstSliceTriple,
		CompilerBuildIdentity: identity,
		Artifacts: ArtifactProvenance{
			FrontendSnapshotHash: frontend.ContentHash,
			HIRContentHash:       replay.HIR.ContentHash,
			BuildPlanHash:        plan.ContentHash,
			RuntimeManifestHash:  runtimeManifest.ContentHash,
			MIRContentHash:       mir.ContentHash,
			LLVMIRHash:           emission.LLVMIRHash,
			ObjectHash:           emission.ObjectHash,
			EmissionContentHash:  emission.ContentHash,
			ResponseFileHash:     link.ResponseFileHash,
			LinkMapHash:          link.LinkMapHash,
			ExecutableHash:       link.ExecutableHash,
			LinkContentHash:      link.ContentHash,
		},
	}
	report.ContentHash, err = reportContentHash(report)
	if err != nil {
		return Report{}, err
	}
	if err := VerifyReport(report); err != nil {
		return Report{}, err
	}
	return report, nil
}

// CanonicalBytes verifies and serializes a successful build report.
func (report Report) CanonicalBytes() ([]byte, error) {
	if err := VerifyReport(report); err != nil {
		return nil, err
	}
	return json.MarshalIndent(report, "", "  ")
}

// VerifyReport recomputes the build report identity and validates all digests.
func VerifyReport(report Report) error {
	if report.SchemaVersion != ReportSchemaVersion || report.Stage != "application-build-preview" ||
		report.EntryPoint != "main" || report.TargetTriple != llvmbackend.FirstSliceTriple {
		return fmt.Errorf("unsupported application build report identity")
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
	} {
		if !isDigest(digest) {
			return fmt.Errorf("invalid %s digest %q", name, digest)
		}
	}
	want, err := reportContentHash(report)
	if err != nil {
		return err
	}
	if report.ContentHash != want {
		return fmt.Errorf("application build report content hash mismatch: got %s want %s", report.ContentHash, want)
	}
	return nil
}

func validateRequest(request Request) error {
	for name, path := range map[string]string{
		"config":            request.ConfigPath,
		"output":            request.OutputPath,
		"runtime directory": request.RuntimeDirectory,
		"runtime archive":   request.RuntimeArchivePath,
	} {
		if strings.TrimSpace(path) == "" || !filepath.IsAbs(path) {
			return fmt.Errorf("application build %s path must be absolute", name)
		}
	}
	if strings.TrimSpace(request.Clang) == "" || strings.TrimSpace(request.LLD) == "" {
		return firstslicelink.ErrLinkUnavailable
	}
	return nil
}

func reportContentHash(report Report) (string, error) {
	report.ContentHash = ""
	encoded, err := json.Marshal(report)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func isDigest(value string) bool {
	if len(value) != 64 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size && value == strings.ToLower(value)
}
