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

const vert011ReportSchemaVersion uint32 = 1

type vert011ArtifactReport struct {
	SchemaVersion         uint32 `json:"schemaVersion"`
	FrontendSnapshotHash  string `json:"frontendSnapshotHash"`
	BuildPlanHash         string `json:"buildPlanHash"`
	TargetContextHash     string `json:"targetContextHash"`
	CapabilityCatalogHash string `json:"capabilityCatalogHash"`
	HIRHash               string `json:"hirHash"`
	LayoutHash            string `json:"layoutHash"`
	MIRHash               string `json:"mirHash"`
	BoundMIRHash          string `json:"boundMirHash"`
	LLVMIRHash            string `json:"llvmIrHash"`
	ObjectHash            string `json:"objectHash"`
	ContentHash           string `json:"contentHash"`
}

func (report vert011ArtifactReport) CanonicalBytes() ([]byte, error) {
	if report.SchemaVersion != vert011ReportSchemaVersion {
		return nil, fmt.Errorf("unsupported VERT-011 report schema %d", report.SchemaVersion)
	}
	for name, value := range map[string]string{
		"frontend snapshot": report.FrontendSnapshotHash, "build plan": report.BuildPlanHash,
		"target context": report.TargetContextHash, "capability catalog": report.CapabilityCatalogHash,
		"HIR": report.HIRHash, "layout": report.LayoutHash, "MIR": report.MIRHash,
		"bound MIR": report.BoundMIRHash, "LLVM IR": report.LLVMIRHash, "object": report.ObjectHash,
	} {
		if !vert010Digest(value) {
			return nil, fmt.Errorf("invalid VERT-011 report %s hash", name)
		}
	}
	withoutHash := report
	withoutHash.ContentHash = ""
	encoded, err := json.Marshal(withoutHash)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(encoded)
	want := hex.EncodeToString(digest[:])
	if report.ContentHash != want {
		return nil, fmt.Errorf("VERT-011 report content hash mismatch: got %q, want %q", report.ContentHash, want)
	}
	return json.Marshal(report)
}

func decodeVERT011ArtifactReport(data []byte) (*vert011ArtifactReport, error) {
	var report vert011ArtifactReport
	if err := jsonx.Unmarshal(data, &report, jsonx.RejectUnknownMembers(true)); err != nil {
		return nil, fmt.Errorf("decode VERT-011 report: %w", err)
	}
	if _, err := report.CanonicalBytes(); err != nil {
		return nil, err
	}
	return &report, nil
}

func runEmitVERT011(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("emit-vert011", flag.ContinueOnError)
	flags.SetOutput(stderr)
	runtimeManifestPath := flags.String("runtime-manifest", "", "path to the locked runtime manifest")
	outputDirectory := flags.String("output-dir", "", "new directory for verified VERT-011 artifacts")
	if err := flags.Parse(args); err != nil || flags.NArg() != 1 || *runtimeManifestPath == "" || *outputDirectory == "" {
		fmt.Fprintln(stderr, "ts2bin emit-vert011: expected --runtime-manifest FILE --output-dir DIR SNAPSHOT")
		return exitUsage
	}
	snapshotBytes, err := os.ReadFile(flags.Arg(0))
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin emit-vert011: read snapshot: %v\n", err)
		return exitUsage
	}
	frontend, err := frontendwire.DecodeFrontendSnapshot(snapshotBytes)
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin emit-vert011: decode snapshot: %v\n", err)
		return exitDiagnostics
	}
	runtimeManifest, err := os.ReadFile(*runtimeManifestPath)
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin emit-vert011: read runtime manifest: %v\n", err)
		return exitUsage
	}
	identity, err := loadCompilerBuildIdentity()
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin emit-vert011: compiler identity: %v\n", err)
		return exitUsage
	}
	plan, err := buildplan.New(frontend.Program.ContentHash, frontendwire.ProfileStatic, buildplan.BackendRequest{
		Target: llvmbackend.FirstSliceTriple, CPU: llvmbackend.FirstSliceCPU, Features: []string{}, Runtime: "core-es2020",
		GC: frontendwire.GCTracing, Exceptions: frontendwire.ExceptionsNone, Overflow: frontendwire.OverflowJSNumber,
		BoundsCheck: frontendwire.BoundsCheckOn, Emit: []frontendwire.EmitArtifact{frontendwire.EmitHIR, frontendwire.EmitMIR, frontendwire.EmitLLVM, frontendwire.EmitObject},
		LLVMMajor: llvmbackend.LockedLLVMMajor,
	})
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin emit-vert011: build plan: %v\n", err)
		return exitDiagnostics
	}
	machine, err := openStaticCoreTargetMachine()
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin emit-vert011: %v\n", err)
		return exitDiagnostics
	}
	if machine != nil {
		defer machine.Close()
	}
	result, err := executeVERT011Pipeline(frontend.Program, identity, plan, machine, runtimeManifest)
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin emit-vert011: %v\n", err)
		return exitDiagnostics
	}
	artifacts, report, err := canonicalVERT011Outputs(result, plan)
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin emit-vert011: encode artifacts: %v\n", err)
		return exitDiagnostics
	}
	outputPath, err := filepath.Abs(*outputDirectory)
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin emit-vert011: resolve output directory: %v\n", err)
		return exitUsage
	}
	if err := publishVERT011Outputs(outputPath, artifacts); err != nil {
		fmt.Fprintf(stderr, "ts2bin emit-vert011: publish artifacts: %v\n", err)
		return exitUsage
	}
	fmt.Fprintf(stdout, "[ok] emit-vert011 %s %s\n", outputPath, report.ContentHash)
	return exitSuccess
}

func canonicalVERT011Outputs(result bingomir.VERT011PipelineResult, plan buildplan.Plan) (map[string][]byte, vert011ArtifactReport, error) {
	hir, _, err := bingo.CanonicalVERT011PlaceHIR(result.Replay.HIR)
	if err != nil {
		return nil, vert011ArtifactReport{}, err
	}
	layout, _, err := bingo.CanonicalObjectLayoutContract(result.Layout)
	if err != nil {
		return nil, vert011ArtifactReport{}, err
	}
	mir, _, err := bingo.CanonicalVERT011MIR(result.MIR)
	if err != nil {
		return nil, vert011ArtifactReport{}, err
	}
	bound, _, err := bingo.CanonicalVERT011BoundMIR(result.BoundMIR)
	if err != nil {
		return nil, vert011ArtifactReport{}, err
	}
	report := vert011ArtifactReport{
		SchemaVersion: vert011ReportSchemaVersion, FrontendSnapshotHash: result.Replay.FrontendSnapshotHash,
		BuildPlanHash: plan.ContentHash, TargetContextHash: result.Resolution.Context.ContentHash,
		CapabilityCatalogHash: result.Resolution.Catalog.ContentHash, HIRHash: result.Replay.HIR.ContentHash,
		LayoutHash: result.Layout.ContentHash, MIRHash: result.MIR.ContentHash, BoundMIRHash: result.BoundMIR.ContentHash,
		LLVMIRHash: result.Emission.LLVMIRHash, ObjectHash: result.Emission.ObjectHash,
	}
	withoutHash, err := json.Marshal(report)
	if err != nil {
		return nil, vert011ArtifactReport{}, err
	}
	digest := sha256.Sum256(withoutHash)
	report.ContentHash = hex.EncodeToString(digest[:])
	reportBytes, err := report.CanonicalBytes()
	if err != nil {
		return nil, vert011ArtifactReport{}, err
	}
	return map[string][]byte{
		"hir-v10.json": append(hir, '\n'), "object-layout-v1.json": append(layout, '\n'),
		"mir-v8.json": append(mir, '\n'), "bound-mir-v1.json": append(bound, '\n'),
		"module.ll": result.Emission.LLVMIR, "module.o": result.Emission.Object, "report.json": append(reportBytes, '\n'),
	}, report, nil
}

func publishVERT011Outputs(directory string, artifacts map[string][]byte) error {
	if info, err := os.Stat(directory); err == nil {
		return fmt.Errorf("output directory already exists: %s", directory)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect output directory: %w", err)
	} else if info != nil {
		return fmt.Errorf("output directory already exists: %s", directory)
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
	for _, name := range []string{"hir-v10.json", "object-layout-v1.json", "mir-v8.json", "bound-mir-v1.json", "module.ll", "module.o", "report.json"} {
		path := filepath.Join(directory, name)
		if err := artifactio.PublishNewFile(path, artifacts[name], 0o644); err != nil {
			return rollback(err)
		}
		created = append(created, path)
	}
	return nil
}
