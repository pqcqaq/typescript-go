// Package irartifact contains checker-free first-slice artifact tooling.
//
// The package consumes only serialized frontend/build/runtime artifacts. It
// deliberately does not import the live TypeScript AST, binder, or checker.
package irartifact

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/microsoft/typescript-go/internal/ast2bingo"
	"github.com/microsoft/typescript-go/internal/bingo"
	"github.com/microsoft/typescript-go/internal/bingomir"
	"github.com/microsoft/typescript-go/internal/buildplan"
	"github.com/microsoft/typescript-go/internal/frontendwire"
	jsonx "github.com/microsoft/typescript-go/internal/json"
	"github.com/microsoft/typescript-go/internal/llvmbackend"
)

const CaseManifestSchemaVersion uint32 = 1

// CaseManifest identifies the immutable artifacts used by a first-slice
// command. Paths are relative to the directory containing case.json.
type CaseManifest struct {
	SchemaVersion    uint32          `json:"schemaVersion"`
	Name             string          `json:"name"`
	FrontendSnapshot string          `json:"frontendSnapshot"`
	BuildPlan        string          `json:"buildPlan,omitempty"`
	RuntimeManifest  string          `json:"runtimeManifest,omitempty"`
	TimeoutMS        uint32          `json:"timeoutMs,omitempty"`
	Oracle           string          `json:"oracle,omitempty"`
	Executions       []CaseExecution `json:"executions,omitempty"`
}

// CaseExecution is one observable first-slice C ABI invocation.
type CaseExecution struct {
	Name         string `json:"name"`
	LeftBits     string `json:"leftBits"`
	RightBits    string `json:"rightBits"`
	ExpectedBits string `json:"expectedBits"`
}

type Case struct {
	Directory       string
	Manifest        CaseManifest
	Frontend        frontendwire.FrontendSnapshot
	BuildPlan       *buildplan.Plan
	RuntimeManifest []byte
}

func LoadCase(directory string, requireBackend bool) (Case, error) {
	info, err := os.Stat(directory)
	if err != nil {
		return Case{}, fmt.Errorf("stat case directory: %w", err)
	}
	if !info.IsDir() {
		return Case{}, fmt.Errorf("case path %q is not a directory", directory)
	}
	directory, err = filepath.Abs(directory)
	if err != nil {
		return Case{}, fmt.Errorf("resolve case directory: %w", err)
	}
	manifestBytes, err := os.ReadFile(filepath.Join(directory, "case.json"))
	if err != nil {
		return Case{}, fmt.Errorf("read case manifest: %w", err)
	}
	var manifest CaseManifest
	if err := jsonx.Unmarshal(manifestBytes, &manifest, jsonx.RejectUnknownMembers(true)); err != nil {
		return Case{}, fmt.Errorf("decode case manifest: %w", err)
	}
	if err := validateManifest(manifest, requireBackend); err != nil {
		return Case{}, err
	}
	frontendBytes, err := readCaseFile(directory, manifest.FrontendSnapshot, "frontend snapshot")
	if err != nil {
		return Case{}, err
	}
	frontend, err := frontendwire.DecodeFrontendSnapshot(frontendBytes)
	if err != nil {
		return Case{}, fmt.Errorf("decode frontend snapshot: %w", err)
	}
	result := Case{Directory: directory, Manifest: manifest, Frontend: *frontend}
	if requireBackend {
		planBytes, err := readCaseFile(directory, manifest.BuildPlan, "build plan")
		if err != nil {
			return Case{}, err
		}
		plan, err := buildplan.Decode(planBytes)
		if err != nil {
			return Case{}, err
		}
		if plan.FrontendHash != frontend.ContentHash {
			return Case{}, fmt.Errorf("build plan frontend hash %q does not match snapshot %q", plan.FrontendHash, frontend.ContentHash)
		}
		result.BuildPlan = plan
	}
	if requireBackend {
		runtime, err := readCaseFile(directory, manifest.RuntimeManifest, "runtime manifest")
		if err != nil {
			return Case{}, err
		}
		result.RuntimeManifest = runtime
	}
	if requireBackend && (result.BuildPlan == nil || len(result.RuntimeManifest) == 0) {
		return Case{}, fmt.Errorf("backend case requires buildPlan and runtimeManifest")
	}
	return result, nil
}

func validateManifest(manifest CaseManifest, requireBackend bool) error {
	if manifest.SchemaVersion != CaseManifestSchemaVersion {
		return fmt.Errorf("unsupported case manifest schema %d", manifest.SchemaVersion)
	}
	if strings.TrimSpace(manifest.Name) == "" {
		return fmt.Errorf("case manifest name is empty")
	}
	if strings.TrimSpace(manifest.FrontendSnapshot) == "" {
		return fmt.Errorf("case manifest frontendSnapshot is empty")
	}
	if requireBackend && (strings.TrimSpace(manifest.BuildPlan) == "" || strings.TrimSpace(manifest.RuntimeManifest) == "") {
		return fmt.Errorf("case manifest requires buildPlan and runtimeManifest for backend commands")
	}
	if manifest.TimeoutMS != 0 || len(manifest.Executions) != 0 {
		if err := ValidateRunnableManifest(manifest); err != nil {
			return err
		}
	}
	return nil
}

// ValidateRunnableManifest checks the isolation and observable-input contract
// required by the REL-001a runner.
func ValidateRunnableManifest(manifest CaseManifest) error {
	if manifest.TimeoutMS == 0 || manifest.TimeoutMS > 60_000 {
		return fmt.Errorf("case timeoutMs must be between 1 and 60000")
	}
	if manifest.Oracle != "node" {
		return fmt.Errorf("runnable case oracle is %q, want node", manifest.Oracle)
	}
	if len(manifest.Executions) == 0 {
		return fmt.Errorf("runnable case has no executions")
	}
	names := make(map[string]struct{}, len(manifest.Executions))
	for _, execution := range manifest.Executions {
		if strings.TrimSpace(execution.Name) == "" {
			return fmt.Errorf("case execution name is empty")
		}
		if _, exists := names[execution.Name]; exists {
			return fmt.Errorf("duplicate case execution name %q", execution.Name)
		}
		names[execution.Name] = struct{}{}
		for label, value := range map[string]string{
			"leftBits": execution.LeftBits, "rightBits": execution.RightBits, "expectedBits": execution.ExpectedBits,
		} {
			if !isCanonicalBits(value) {
				return fmt.Errorf("execution %q has invalid %s %q", execution.Name, label, value)
			}
		}
	}
	return nil
}

func isCanonicalBits(value string) bool {
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

func readCaseFile(directory, relative, label string) ([]byte, error) {
	if filepath.IsAbs(relative) {
		return nil, fmt.Errorf("%s path must be relative to case directory", label)
	}
	candidate := filepath.Join(directory, relative)
	rel, err := filepath.Rel(directory, candidate)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("%s path escapes case directory", label)
	}
	info, err := os.Stat(candidate)
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", label, err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("%s path %q is a directory", label, relative)
	}
	data, err := os.ReadFile(candidate)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", label, err)
	}
	return data, nil
}

func LoadHIR(directory string, identity bingo.CompilerBuildIdentity) (bingo.HIRModule, error) {
	caseData, err := LoadCase(directory, false)
	if err != nil {
		return bingo.HIRModule{}, err
	}
	frontendBytes, err := caseData.Frontend.CanonicalBytes()
	if err != nil {
		return bingo.HIRModule{}, fmt.Errorf("encode frontend snapshot: %w", err)
	}
	result, err := ast2bingo.ReplayFrontendSnapshot(frontendBytes, identity)
	if err != nil {
		return bingo.HIRModule{}, fmt.Errorf("replay frontend snapshot: %w", err)
	}
	if _, err := result.CanonicalBytes(); err != nil {
		return bingo.HIRModule{}, fmt.Errorf("verify replay artifact: %w", err)
	}
	if err := bingo.VerifyCanonicalHIR(result.HIR); err != nil {
		return bingo.HIRModule{}, fmt.Errorf("verify HIR artifact: %w", err)
	}
	return result.HIR, nil
}

func LoadMIR(ctx context.Context, directory string, identity bingo.CompilerBuildIdentity, machine *llvmbackend.TargetMachine) (bingo.FirstSliceMIRArtifact, error) {
	if machine == nil {
		return bingo.FirstSliceMIRArtifact{}, fmt.Errorf("LLVM TargetMachine is required")
	}
	caseData, err := LoadCase(directory, true)
	if err != nil {
		return bingo.FirstSliceMIRArtifact{}, err
	}
	if ctx == nil {
		return bingo.FirstSliceMIRArtifact{}, fmt.Errorf("context is nil")
	}
	result, _, err := bingomir.ExecuteFirstSliceMIR(ctx, caseData.Frontend.Program, identity, *caseData.BuildPlan, machine, caseData.RuntimeManifest)
	if err != nil {
		return bingo.FirstSliceMIRArtifact{}, fmt.Errorf("execute first-slice MIR pipeline: %w", err)
	}
	if _, err := result.CanonicalBoundBytes(); err != nil {
		return bingo.FirstSliceMIRArtifact{}, fmt.Errorf("verify final MIR artifact: %w", err)
	}
	return result, nil
}

func CanonicalHIR(module bingo.HIRModule) ([]byte, error) {
	if err := bingo.VerifyCanonicalHIR(module); err != nil {
		return nil, err
	}
	data, _, err := bingo.CanonicalHIR(module)
	if err != nil {
		return nil, err
	}
	return data, nil
}

func DecodeHIR(data []byte) (bingo.HIRModule, error) {
	var module bingo.HIRModule
	if err := jsonx.Unmarshal(data, &module, jsonx.RejectUnknownMembers(true)); err != nil {
		return bingo.HIRModule{}, fmt.Errorf("decode HIR artifact: %w", err)
	}
	if err := bingo.VerifyCanonicalHIR(module); err != nil {
		return bingo.HIRModule{}, err
	}
	return module, nil
}

func CanonicalMIR(module bingo.FirstSliceMIRArtifact) ([]byte, error) {
	return module.CanonicalBoundBytes()
}

func DecodeMIR(data []byte) (bingo.FirstSliceMIRArtifact, error) {
	var module bingo.FirstSliceMIRArtifact
	if err := jsonx.Unmarshal(data, &module, jsonx.RejectUnknownMembers(true)); err != nil {
		return bingo.FirstSliceMIRArtifact{}, fmt.Errorf("decode MIR artifact: %w", err)
	}
	if module.BoundCapabilityClosure == nil {
		if err := bingo.VerifyStructuralFirstSliceMIR(module); err != nil {
			return bingo.FirstSliceMIRArtifact{}, err
		}
	} else if err := bingo.VerifyBoundFirstSliceMIR(module); err != nil {
		return bingo.FirstSliceMIRArtifact{}, err
	}
	return module, nil
}
