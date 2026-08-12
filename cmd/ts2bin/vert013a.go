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

const vert013aReportSchemaVersion uint32 = 1

type vert013aArtifactReport struct {
	SchemaVersion         uint32 `json:"schemaVersion"`
	FrontendSnapshotHash  string `json:"frontendSnapshotHash"`
	BuildPlanHash         string `json:"buildPlanHash"`
	TargetContextHash     string `json:"targetContextHash"`
	CapabilityCatalogHash string `json:"capabilityCatalogHash"`
	ClassContractHash     string `json:"classContractHash"`
	HIRHash               string `json:"hirHash"`
	InstanceLayoutHash    string `json:"instanceLayoutHash"`
	MIRHash               string `json:"mirHash"`
	BoundMIRHash          string `json:"boundMirHash"`
	LLVMIRHash            string `json:"llvmIrHash"`
	ObjectHash            string `json:"objectHash"`
	ContentHash           string `json:"contentHash"`
}

func (report vert013aArtifactReport) CanonicalBytes() ([]byte, error) {
	if report.SchemaVersion != vert013aReportSchemaVersion {
		return nil, fmt.Errorf("unsupported VERT-013a report schema %d", report.SchemaVersion)
	}
	for name, value := range map[string]string{
		"frontend snapshot": report.FrontendSnapshotHash, "build plan": report.BuildPlanHash,
		"target context": report.TargetContextHash, "capability catalog": report.CapabilityCatalogHash,
		"class contract": report.ClassContractHash, "HIR": report.HIRHash,
		"instance layout": report.InstanceLayoutHash, "MIR": report.MIRHash,
		"bound MIR": report.BoundMIRHash, "LLVM IR": report.LLVMIRHash, "object": report.ObjectHash,
	} {
		if !vert010Digest(value) {
			return nil, fmt.Errorf("invalid VERT-013a report %s hash", name)
		}
	}
	withoutHash := report
	withoutHash.ContentHash = ""
	encoded, err := json.Marshal(withoutHash)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(encoded)
	if report.ContentHash != hex.EncodeToString(digest[:]) {
		return nil, fmt.Errorf("VERT-013a report content hash mismatch")
	}
	return json.Marshal(report)
}

func decodeVERT013aArtifactReport(data []byte) (*vert013aArtifactReport, error) {
	var report vert013aArtifactReport
	if err := jsonx.Unmarshal(data, &report, jsonx.RejectUnknownMembers(true)); err != nil {
		return nil, fmt.Errorf("decode VERT-013a report: %w", err)
	}
	if _, err := report.CanonicalBytes(); err != nil {
		return nil, err
	}
	return &report, nil
}

func runEmitVERT013a(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("emit-vert013a", flag.ContinueOnError)
	flags.SetOutput(stderr)
	runtimeManifestPath := flags.String("runtime-manifest", "", "path to the locked runtime manifest")
	outputDirectory := flags.String("output-dir", "", "new directory for verified VERT-013a artifacts")
	if err := flags.Parse(args); err != nil || flags.NArg() != 1 || *runtimeManifestPath == "" || *outputDirectory == "" {
		fmt.Fprintln(stderr, "ts2bin emit-vert013a: expected --runtime-manifest FILE --output-dir DIR SNAPSHOT")
		return exitUsage
	}
	snapshotBytes, err := os.ReadFile(flags.Arg(0))
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin emit-vert013a: read snapshot: %v\n", err)
		return exitUsage
	}
	frontend, err := frontendwire.DecodeFrontendSnapshot(snapshotBytes)
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin emit-vert013a: decode snapshot: %v\n", err)
		return exitDiagnostics
	}
	runtimeManifest, err := os.ReadFile(*runtimeManifestPath)
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin emit-vert013a: read runtime manifest: %v\n", err)
		return exitUsage
	}
	identity, err := loadCompilerBuildIdentity()
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin emit-vert013a: compiler identity: %v\n", err)
		return exitUsage
	}
	plan, err := buildplan.New(frontend.Program.ContentHash, frontendwire.ProfileStatic, buildplan.BackendRequest{
		Target: llvmbackend.FirstSliceTriple, CPU: llvmbackend.FirstSliceCPU, Features: []string{}, Runtime: "core-es2020",
		GC: frontendwire.GCTracing, Exceptions: frontendwire.ExceptionsNone, Overflow: frontendwire.OverflowJSNumber,
		BoundsCheck: frontendwire.BoundsCheckOn, Emit: []frontendwire.EmitArtifact{frontendwire.EmitHIR, frontendwire.EmitMIR, frontendwire.EmitLLVM, frontendwire.EmitObject},
		LLVMMajor: llvmbackend.LockedLLVMMajor,
	})
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin emit-vert013a: build plan: %v\n", err)
		return exitDiagnostics
	}
	machine, err := openStaticCoreTargetMachine()
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin emit-vert013a: %v\n", err)
		return exitDiagnostics
	}
	if machine != nil {
		defer machine.Close()
	}
	result, err := executeVERT013aPipeline(frontend.Program, identity, plan, machine, runtimeManifest)
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin emit-vert013a: %v\n", err)
		return exitDiagnostics
	}
	artifacts, report, err := canonicalVERT013aOutputs(result, plan)
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin emit-vert013a: encode artifacts: %v\n", err)
		return exitDiagnostics
	}
	outputPath, err := filepath.Abs(*outputDirectory)
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin emit-vert013a: resolve output directory: %v\n", err)
		return exitUsage
	}
	if err := publishVERT013aOutputs(outputPath, artifacts); err != nil {
		fmt.Fprintf(stderr, "ts2bin emit-vert013a: publish artifacts: %v\n", err)
		return exitUsage
	}
	fmt.Fprintf(stdout, "[ok] emit-vert013a %s %s\n", outputPath, report.ContentHash)
	return exitSuccess
}

func canonicalVERT013aOutputs(result bingomir.VERT013aPipelineResult, plan buildplan.Plan) (map[string][]byte, vert013aArtifactReport, error) {
	contract, _, err := bingo.CanonicalClassContract(result.Replay.Contract)
	if err != nil {
		return nil, vert013aArtifactReport{}, err
	}
	hir, _, err := bingo.CanonicalVERT013aClassHIR(result.Replay.HIR)
	if err != nil {
		return nil, vert013aArtifactReport{}, err
	}
	layout, _, err := bingo.CanonicalObjectLayoutContract(result.Layout)
	if err != nil {
		return nil, vert013aArtifactReport{}, err
	}
	mir, _, err := bingo.CanonicalVERT013aMIR(result.MIR)
	if err != nil {
		return nil, vert013aArtifactReport{}, err
	}
	bound, _, err := bingo.CanonicalVERT013aBoundMIR(result.BoundMIR)
	if err != nil {
		return nil, vert013aArtifactReport{}, err
	}
	report := vert013aArtifactReport{
		SchemaVersion: vert013aReportSchemaVersion, FrontendSnapshotHash: result.Replay.FrontendSnapshotHash,
		BuildPlanHash: plan.ContentHash, TargetContextHash: result.Resolution.Context.ContentHash,
		CapabilityCatalogHash: result.Resolution.Catalog.ContentHash, ClassContractHash: result.Replay.Contract.ContentHash,
		HIRHash: result.Replay.HIR.ContentHash, InstanceLayoutHash: result.Layout.ContentHash,
		MIRHash: result.MIR.ContentHash, BoundMIRHash: result.BoundMIR.ContentHash,
		LLVMIRHash: result.Emission.LLVMIRHash, ObjectHash: result.Emission.ObjectHash,
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		return nil, vert013aArtifactReport{}, err
	}
	digest := sha256.Sum256(encoded)
	report.ContentHash = hex.EncodeToString(digest[:])
	reportBytes, err := report.CanonicalBytes()
	if err != nil {
		return nil, vert013aArtifactReport{}, err
	}
	return map[string][]byte{
		"class-contract-v1.json": append(contract, '\n'), "hir-v12.json": append(hir, '\n'),
		"instance-layout-v1.json": append(layout, '\n'), "mir-v10.json": append(mir, '\n'),
		"bound-mir-v1.json": append(bound, '\n'), "module.ll": result.Emission.LLVMIR,
		"module.o": result.Emission.Object, "report.json": append(reportBytes, '\n'),
	}, report, nil
}

var vert013aArtifactNames = []string{"class-contract-v1.json", "hir-v12.json", "instance-layout-v1.json", "mir-v10.json", "bound-mir-v1.json", "module.ll", "module.o", "report.json"}

func publishVERT013aOutputs(directory string, artifacts map[string][]byte) error {
	if _, err := os.Stat(directory); err == nil {
		return fmt.Errorf("output directory already exists: %s", directory)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect output directory: %w", err)
	}
	if err := os.Mkdir(directory, 0o755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}
	created := make([]string, 0, len(artifacts))
	rollback := func(cause error) error {
		var rollbackErrors []error
		for _, path := range created {
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				rollbackErrors = append(rollbackErrors, err)
			}
		}
		if err := os.Remove(directory); err != nil && !errors.Is(err, os.ErrNotExist) {
			rollbackErrors = append(rollbackErrors, err)
		}
		return errors.Join(append([]error{cause}, rollbackErrors...)...)
	}
	for _, name := range vert013aArtifactNames {
		path := filepath.Join(directory, name)
		if err := artifactio.PublishNewFile(path, artifacts[name], 0o644); err != nil {
			return rollback(err)
		}
		created = append(created, path)
	}
	return nil
}
