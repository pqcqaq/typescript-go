package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/microsoft/typescript-go/internal/artifactio"
	"github.com/microsoft/typescript-go/internal/bingo"
	"github.com/microsoft/typescript-go/internal/bingomir"
	"github.com/microsoft/typescript-go/internal/buildplan"
	"github.com/microsoft/typescript-go/internal/frontendwire"
	jsonx "github.com/microsoft/typescript-go/internal/json"
	"github.com/microsoft/typescript-go/internal/llvmbackend"
)

const classAccessReportSchemaVersion uint32 = 1

type classAccessArtifactReport struct {
	SchemaVersion         uint32 `json:"schemaVersion"`
	FrontendSnapshotHash  string `json:"frontendSnapshotHash"`
	BuildPlanHash         string `json:"buildPlanHash"`
	TargetContextHash     string `json:"targetContextHash"`
	CapabilityCatalogHash string `json:"capabilityCatalogHash"`
	AccessContractHash    string `json:"accessContractHash"`
	ExecutionHash         string `json:"executionHash"`
	ReplayHash            string `json:"replayHash"`
	HIRHash               string `json:"hirHash"`
	MIRHash               string `json:"mirHash"`
	BaseLayoutHash        string `json:"baseLayoutHash"`
	DerivedLayoutHash     string `json:"derivedLayoutHash"`
	LayoutHash            string `json:"layoutHash"`
	BoundMIRHash          string `json:"boundMirHash"`
	BackendPlanHash       string `json:"backendPlanHash"`
	LLVMIRHash            string `json:"llvmIrHash"`
	ObjectHash            string `json:"objectHash"`
	ContentHash           string `json:"contentHash"`
}

func (report classAccessArtifactReport) CanonicalBytes() ([]byte, error) {
	if report.SchemaVersion != classAccessReportSchemaVersion {
		return nil, fmt.Errorf("unsupported classaccess report schema %d", report.SchemaVersion)
	}
	without := report
	without.ContentHash = ""
	encoded, err := json.Marshal(without)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(encoded)
	if report.ContentHash != hex.EncodeToString(digest[:]) {
		return nil, fmt.Errorf("classaccess report content hash mismatch")
	}
	return json.Marshal(report)
}

func runEmitClassAccess(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("emit-classaccess", flag.ContinueOnError)
	flags.SetOutput(stderr)
	runtimeManifestPath := flags.String("runtime-manifest", "", "path to locked runtime manifest")
	outputDirectory := flags.String("output-dir", "", "new directory for verified classaccess artifacts")
	if err := flags.Parse(args); err != nil || flags.NArg() != 1 || *runtimeManifestPath == "" || *outputDirectory == "" {
		fmt.Fprintln(stderr, "ts2bin emit-classaccess: expected --runtime-manifest FILE --output-dir DIR SNAPSHOT")
		return exitUsage
	}
	snapshotBytes, err := os.ReadFile(flags.Arg(0))
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin emit-classaccess: read snapshot: %v\n", err)
		return exitUsage
	}
	frontend, err := frontendwire.DecodeFrontendSnapshot(snapshotBytes)
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin emit-classaccess: decode snapshot: %v\n", err)
		return exitDiagnostics
	}
	runtimeManifest, err := os.ReadFile(*runtimeManifestPath)
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin emit-classaccess: read runtime manifest: %v\n", err)
		return exitUsage
	}
	identity, err := loadCompilerBuildIdentity()
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin emit-classaccess: compiler identity: %v\n", err)
		return exitUsage
	}
	plan, err := buildplan.New(frontend.Program.ContentHash, frontendwire.ProfileStatic, buildplan.BackendRequest{Target: llvmbackend.FirstSliceTriple, CPU: llvmbackend.FirstSliceCPU, Features: []string{}, Runtime: "core-es2020", GC: frontendwire.GCTracing, Exceptions: frontendwire.ExceptionsNone, Overflow: frontendwire.OverflowJSNumber, BoundsCheck: frontendwire.BoundsCheckOn, Emit: []frontendwire.EmitArtifact{frontendwire.EmitHIR, frontendwire.EmitMIR, frontendwire.EmitLLVM, frontendwire.EmitObject}, LLVMMajor: llvmbackend.LockedLLVMMajor})
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin emit-classaccess: build plan: %v\n", err)
		return exitDiagnostics
	}
	machine, err := openStaticCoreTargetMachine()
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin emit-classaccess: %v\n", err)
		return exitDiagnostics
	}
	if machine != nil {
		defer machine.Close()
	}
	result, err := bingomir.ExecuteClassAccess(frontend.Program, identity, plan, machine, runtimeManifest)
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin emit-classaccess: %v\n", err)
		return exitDiagnostics
	}
	artifacts, report, err := canonicalClassAccessOutputs(result, plan)
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin emit-classaccess: encode artifacts: %v\n", err)
		return exitDiagnostics
	}
	outputPath, err := filepath.Abs(*outputDirectory)
	if err != nil {
		return exitUsage
	}
	if err := publishClassAccessOutputs(outputPath, artifacts); err != nil {
		fmt.Fprintf(stderr, "ts2bin emit-classaccess: publish: %v\n", err)
		return exitUsage
	}
	fmt.Fprintf(stdout, "[ok] emit-classaccess %s %s\n", outputPath, report.ContentHash)
	return exitSuccess
}

func canonicalClassAccessOutputs(result bingomir.ClassAccessPipelineResult, plan buildplan.Plan) (map[string][]byte, classAccessArtifactReport, error) {
	access, _, err := bingo.CanonicalClassAccessContract(result.Replay.Contract)
	if err != nil {
		return nil, classAccessArtifactReport{}, err
	}
	execution, _, err := bingo.CanonicalClassAccessExecution(result.Replay.Execution)
	if err != nil {
		return nil, classAccessArtifactReport{}, err
	}
	replay, err := result.Replay.CanonicalBytes()
	if err != nil {
		return nil, classAccessArtifactReport{}, err
	}
	hir, _, err := bingo.CanonicalClassAccessHIR(result.Replay.HIR)
	if err != nil {
		return nil, classAccessArtifactReport{}, err
	}
	mir, _, err := bingo.CanonicalClassAccessMIR(result.MIR)
	if err != nil {
		return nil, classAccessArtifactReport{}, err
	}
	layout, _, err := bingo.CanonicalClassAccessLayout(result.Layout)
	if err != nil {
		return nil, classAccessArtifactReport{}, err
	}
	bound, _, err := bingo.CanonicalClassAccessBoundMIR(result.BoundMIR)
	if err != nil {
		return nil, classAccessArtifactReport{}, err
	}
	backend, _, err := llvmbackend.CanonicalClassAccessBackendPlan(result.BackendPlan)
	if err != nil {
		return nil, classAccessArtifactReport{}, err
	}
	report := classAccessArtifactReport{SchemaVersion: 1, FrontendSnapshotHash: result.Replay.FrontendSnapshotHash, BuildPlanHash: plan.ContentHash, TargetContextHash: result.Resolution.Context.ContentHash, CapabilityCatalogHash: result.Resolution.Catalog.ContentHash, AccessContractHash: result.Replay.Contract.ContentHash, ExecutionHash: result.Replay.Execution.ContentHash, ReplayHash: result.Replay.ContentHash, HIRHash: result.Replay.HIR.ContentHash, MIRHash: result.MIR.ContentHash, BaseLayoutHash: result.Layout.Base.ContentHash, DerivedLayoutHash: result.Layout.Derived.ContentHash, LayoutHash: result.Layout.ContentHash, BoundMIRHash: result.BoundMIR.ContentHash, BackendPlanHash: result.BackendPlan.ContentHash, LLVMIRHash: result.Emission.LLVMIRHash, ObjectHash: result.Emission.ObjectHash}
	without := report
	without.ContentHash = ""
	encoded, _ := json.Marshal(without)
	digest := sha256.Sum256(encoded)
	report.ContentHash = hex.EncodeToString(digest[:])
	reportBytes, err := report.CanonicalBytes()
	if err != nil {
		return nil, classAccessArtifactReport{}, err
	}
	return map[string][]byte{"access-contract-v1.json": append(access, '\n'), "execution-v1.json": append(execution, '\n'), "replay-v2.json": append(replay, '\n'), "hir-v15.json": append(hir, '\n'), "mir-v13.json": append(mir, '\n'), "layout-v1.json": append(layout, '\n'), "bound-mir-v1.json": append(bound, '\n'), "backend-plan-v1.json": append(backend, '\n'), "module.ll": result.Emission.LLVMIR, "module.o": result.Emission.Object, "report.json": append(reportBytes, '\n')}, report, nil
}

var classAccessArtifactNames = []string{"access-contract-v1.json", "execution-v1.json", "replay-v2.json", "hir-v15.json", "mir-v13.json", "layout-v1.json", "bound-mir-v1.json", "backend-plan-v1.json", "module.ll", "module.o", "report.json"}

func publishClassAccessOutputs(directory string, artifacts map[string][]byte) error {
	if _, err := os.Stat(directory); err == nil {
		return fmt.Errorf("output directory already exists: %s", directory)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Mkdir(directory, 0o755); err != nil {
		return err
	}
	created := []string{}
	rollback := func(cause error) error {
		for _, path := range created {
			_ = os.Remove(path)
		}
		_ = os.Remove(directory)
		return cause
	}
	for _, name := range classAccessArtifactNames {
		path := filepath.Join(directory, name)
		if err := artifactio.PublishNewFile(path, artifacts[name], 0o644); err != nil {
			return rollback(err)
		}
		created = append(created, path)
	}
	return nil
}

func decodeClassAccessArtifactReport(data []byte) (*classAccessArtifactReport, error) {
	var report classAccessArtifactReport
	if err := jsonx.Unmarshal(data, &report, jsonx.RejectUnknownMembers(true)); err != nil {
		return nil, err
	}
	if _, err := report.CanonicalBytes(); err != nil {
		return nil, err
	}
	return &report, nil
}
