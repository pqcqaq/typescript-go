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

const vert013bReportSchemaVersion uint32 = 1

type vert013bArtifactReport struct {
	SchemaVersion         uint32 `json:"schemaVersion"`
	FrontendSnapshotHash  string `json:"frontendSnapshotHash"`
	BuildPlanHash         string `json:"buildPlanHash"`
	TargetContextHash     string `json:"targetContextHash"`
	CapabilityCatalogHash string `json:"capabilityCatalogHash"`
	ClassContractHash     string `json:"classContractHash"`
	HIRHash               string `json:"hirHash"`
	BaseLayoutHash        string `json:"baseLayoutHash"`
	DerivedLayoutHash     string `json:"derivedLayoutHash"`
	MIRHash               string `json:"mirHash"`
	BoundMIRHash          string `json:"boundMirHash"`
	LLVMIRHash            string `json:"llvmIrHash"`
	ObjectHash            string `json:"objectHash"`
	ContentHash           string `json:"contentHash"`
}

func (report vert013bArtifactReport) CanonicalBytes() ([]byte, error) {
	if report.SchemaVersion != vert013bReportSchemaVersion {
		return nil, fmt.Errorf("unsupported VERT-013b report schema %d", report.SchemaVersion)
	}
	without := report
	without.ContentHash = ""
	encoded, err := json.Marshal(without)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(encoded)
	if report.ContentHash != hex.EncodeToString(digest[:]) {
		return nil, fmt.Errorf("VERT-013b report content hash mismatch")
	}
	return json.Marshal(report)
}

func runEmitVERT013b(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("emit-vert013b", flag.ContinueOnError)
	flags.SetOutput(stderr)
	runtimeManifestPath := flags.String("runtime-manifest", "", "path to locked runtime manifest")
	outputDirectory := flags.String("output-dir", "", "new directory for verified VERT-013b artifacts")
	if err := flags.Parse(args); err != nil || flags.NArg() != 1 || *runtimeManifestPath == "" || *outputDirectory == "" {
		fmt.Fprintln(stderr, "ts2bin emit-vert013b: expected --runtime-manifest FILE --output-dir DIR SNAPSHOT")
		return exitUsage
	}
	snapshotBytes, err := os.ReadFile(flags.Arg(0))
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin emit-vert013b: read snapshot: %v\n", err)
		return exitUsage
	}
	frontend, err := frontendwire.DecodeFrontendSnapshot(snapshotBytes)
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin emit-vert013b: decode snapshot: %v\n", err)
		return exitDiagnostics
	}
	runtimeManifest, err := os.ReadFile(*runtimeManifestPath)
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin emit-vert013b: read runtime manifest: %v\n", err)
		return exitUsage
	}
	identity, err := loadCompilerBuildIdentity()
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin emit-vert013b: compiler identity: %v\n", err)
		return exitUsage
	}
	plan, err := buildplan.New(frontend.Program.ContentHash, frontendwire.ProfileStatic, buildplan.BackendRequest{Target: llvmbackend.FirstSliceTriple, CPU: llvmbackend.FirstSliceCPU, Features: []string{}, Runtime: "core-es2020", GC: frontendwire.GCTracing, Exceptions: frontendwire.ExceptionsNone, Overflow: frontendwire.OverflowJSNumber, BoundsCheck: frontendwire.BoundsCheckOn, Emit: []frontendwire.EmitArtifact{frontendwire.EmitHIR, frontendwire.EmitMIR, frontendwire.EmitLLVM, frontendwire.EmitObject}, LLVMMajor: llvmbackend.LockedLLVMMajor})
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin emit-vert013b: build plan: %v\n", err)
		return exitDiagnostics
	}
	machine, err := openStaticCoreTargetMachine()
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin emit-vert013b: %v\n", err)
		return exitDiagnostics
	}
	if machine != nil {
		defer machine.Close()
	}
	result, err := bingomir.ExecuteVERT013b(frontend.Program, identity, plan, machine, runtimeManifest)
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin emit-vert013b: %v\n", err)
		return exitDiagnostics
	}
	artifacts, report, err := canonicalVERT013bOutputs(result, plan)
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin emit-vert013b: encode artifacts: %v\n", err)
		return exitDiagnostics
	}
	outputPath, err := filepath.Abs(*outputDirectory)
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin emit-vert013b: resolve output directory: %v\n", err)
		return exitUsage
	}
	if err := publishVERT013bOutputs(outputPath, artifacts); err != nil {
		fmt.Fprintf(stderr, "ts2bin emit-vert013b: publish artifacts: %v\n", err)
		return exitUsage
	}
	fmt.Fprintf(stdout, "[ok] emit-vert013b %s %s\n", outputPath, report.ContentHash)
	return exitSuccess
}

func canonicalVERT013bOutputs(result bingomir.VERT013bPipelineResult, plan buildplan.Plan) (map[string][]byte, vert013bArtifactReport, error) {
	contract, _, err := bingo.CanonicalVERT013bClassContract(result.Replay.Contract)
	if err != nil {
		return nil, vert013bArtifactReport{}, err
	}
	hir, _, err := bingo.CanonicalVERT013bDerivedHIR(result.Replay.HIR)
	if err != nil {
		return nil, vert013bArtifactReport{}, err
	}
	layout, _, err := bingo.CanonicalVERT013bLayout(result.Layout, result.Replay.Contract)
	if err != nil {
		return nil, vert013bArtifactReport{}, err
	}
	mir, _, err := bingo.CanonicalVERT013bMIR(result.MIR)
	if err != nil {
		return nil, vert013bArtifactReport{}, err
	}
	bound, _, err := bingo.CanonicalVERT013bBoundMIR(result.BoundMIR)
	if err != nil {
		return nil, vert013bArtifactReport{}, err
	}
	report := vert013bArtifactReport{SchemaVersion: 1, FrontendSnapshotHash: result.Replay.FrontendSnapshotHash, BuildPlanHash: plan.ContentHash, TargetContextHash: result.Resolution.Context.ContentHash, CapabilityCatalogHash: result.Resolution.Catalog.ContentHash, ClassContractHash: result.Replay.Contract.ContentHash, HIRHash: result.Replay.HIR.ContentHash, BaseLayoutHash: result.Layout.Base.ContentHash, DerivedLayoutHash: result.Layout.Derived.ContentHash, MIRHash: result.MIR.ContentHash, BoundMIRHash: result.BoundMIR.ContentHash, LLVMIRHash: result.Emission.LLVMIRHash, ObjectHash: result.Emission.ObjectHash}
	without := report
	without.ContentHash = ""
	encoded, err := json.Marshal(without)
	if err != nil {
		return nil, vert013bArtifactReport{}, err
	}
	digest := sha256.Sum256(encoded)
	report.ContentHash = hex.EncodeToString(digest[:])
	reportBytes, err := report.CanonicalBytes()
	if err != nil {
		return nil, vert013bArtifactReport{}, err
	}
	return map[string][]byte{"class-contract-v2.json": append(contract, '\n'), "hir-v13.json": append(hir, '\n'), "layout-v1.json": append(layout, '\n'), "mir-v11.json": append(mir, '\n'), "bound-mir-v1.json": append(bound, '\n'), "module.ll": result.Emission.LLVMIR, "module.o": result.Emission.Object, "report.json": append(reportBytes, '\n')}, report, nil
}

var vert013bArtifactNames = []string{"class-contract-v2.json", "hir-v13.json", "layout-v1.json", "mir-v11.json", "bound-mir-v1.json", "module.ll", "module.o", "report.json"}

func publishVERT013bOutputs(directory string, artifacts map[string][]byte) error {
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
	for _, name := range vert013bArtifactNames {
		path := filepath.Join(directory, name)
		if err := artifactio.PublishNewFile(path, artifacts[name], 0o644); err != nil {
			return rollback(err)
		}
		created = append(created, path)
	}
	return nil
}

func decodeVERT013bArtifactReport(data []byte) (*vert013bArtifactReport, error) {
	var report vert013bArtifactReport
	if err := jsonx.Unmarshal(data, &report, jsonx.RejectUnknownMembers(true)); err != nil {
		return nil, err
	}
	if _, err := report.CanonicalBytes(); err != nil {
		return nil, err
	}
	return &report, nil
}
