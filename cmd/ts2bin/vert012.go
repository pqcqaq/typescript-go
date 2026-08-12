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

const vert012ReportSchemaVersion uint32 = 1

type vert012ArtifactReport struct {
	SchemaVersion         uint32 `json:"schemaVersion"`
	FrontendSnapshotHash  string `json:"frontendSnapshotHash"`
	BuildPlanHash         string `json:"buildPlanHash"`
	TargetContextHash     string `json:"targetContextHash"`
	CapabilityCatalogHash string `json:"capabilityCatalogHash"`
	HIRHash               string `json:"hirHash"`
	ClosureContractHash   string `json:"closureContractHash"`
	CellLayoutHash        string `json:"cellLayoutHash"`
	EnvironmentLayoutHash string `json:"environmentLayoutHash"`
	MIRHash               string `json:"mirHash"`
	BoundMIRHash          string `json:"boundMirHash"`
	LLVMIRHash            string `json:"llvmIrHash"`
	ObjectHash            string `json:"objectHash"`
	ContentHash           string `json:"contentHash"`
}

func (report vert012ArtifactReport) CanonicalBytes() ([]byte, error) {
	if report.SchemaVersion != vert012ReportSchemaVersion {
		return nil, fmt.Errorf("unsupported VERT-012 report schema %d", report.SchemaVersion)
	}
	for name, value := range map[string]string{
		"frontend snapshot": report.FrontendSnapshotHash, "build plan": report.BuildPlanHash,
		"target context": report.TargetContextHash, "capability catalog": report.CapabilityCatalogHash,
		"closure contract": report.ClosureContractHash, "HIR": report.HIRHash, "cell layout": report.CellLayoutHash,
		"environment layout": report.EnvironmentLayoutHash, "MIR": report.MIRHash,
		"bound MIR": report.BoundMIRHash, "LLVM IR": report.LLVMIRHash, "object": report.ObjectHash,
	} {
		if !vert010Digest(value) {
			return nil, fmt.Errorf("invalid VERT-012 report %s hash", name)
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
		return nil, fmt.Errorf("VERT-012 report content hash mismatch: got %q, want %q", report.ContentHash, want)
	}
	return json.Marshal(report)
}

func decodeVERT012ArtifactReport(data []byte) (*vert012ArtifactReport, error) {
	var report vert012ArtifactReport
	if err := jsonx.Unmarshal(data, &report, jsonx.RejectUnknownMembers(true)); err != nil {
		return nil, fmt.Errorf("decode VERT-012 report: %w", err)
	}
	if _, err := report.CanonicalBytes(); err != nil {
		return nil, err
	}
	return &report, nil
}

func runEmitVERT012(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("emit-vert012", flag.ContinueOnError)
	flags.SetOutput(stderr)
	runtimeManifestPath := flags.String("runtime-manifest", "", "path to the locked runtime manifest")
	outputDirectory := flags.String("output-dir", "", "new directory for verified VERT-012 artifacts")
	if err := flags.Parse(args); err != nil || flags.NArg() != 1 || *runtimeManifestPath == "" || *outputDirectory == "" {
		fmt.Fprintln(stderr, "ts2bin emit-vert012: expected --runtime-manifest FILE --output-dir DIR SNAPSHOT")
		return exitUsage
	}
	snapshotBytes, err := os.ReadFile(flags.Arg(0))
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin emit-vert012: read snapshot: %v\n", err)
		return exitUsage
	}
	frontend, err := frontendwire.DecodeFrontendSnapshot(snapshotBytes)
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin emit-vert012: decode snapshot: %v\n", err)
		return exitDiagnostics
	}
	runtimeManifest, err := os.ReadFile(*runtimeManifestPath)
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin emit-vert012: read runtime manifest: %v\n", err)
		return exitUsage
	}
	identity, err := loadCompilerBuildIdentity()
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin emit-vert012: compiler identity: %v\n", err)
		return exitUsage
	}
	plan, err := buildplan.New(frontend.Program.ContentHash, frontendwire.ProfileStatic, buildplan.BackendRequest{
		Target: llvmbackend.FirstSliceTriple, CPU: llvmbackend.FirstSliceCPU, Features: []string{}, Runtime: "core-es2020",
		GC: frontendwire.GCTracing, Exceptions: frontendwire.ExceptionsNone, Overflow: frontendwire.OverflowJSNumber,
		BoundsCheck: frontendwire.BoundsCheckOn, Emit: []frontendwire.EmitArtifact{frontendwire.EmitHIR, frontendwire.EmitMIR, frontendwire.EmitLLVM, frontendwire.EmitObject},
		LLVMMajor: llvmbackend.LockedLLVMMajor,
	})
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin emit-vert012: build plan: %v\n", err)
		return exitDiagnostics
	}
	machine, err := openStaticCoreTargetMachine()
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin emit-vert012: %v\n", err)
		return exitDiagnostics
	}
	if machine != nil {
		defer machine.Close()
	}
	result, err := executeVERT012Pipeline(frontend.Program, identity, plan, machine, runtimeManifest)
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin emit-vert012: %v\n", err)
		return exitDiagnostics
	}
	artifacts, report, err := canonicalVERT012Outputs(result, plan)
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin emit-vert012: encode artifacts: %v\n", err)
		return exitDiagnostics
	}
	outputPath, err := filepath.Abs(*outputDirectory)
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin emit-vert012: resolve output directory: %v\n", err)
		return exitUsage
	}
	if err := publishVERT012Outputs(outputPath, artifacts); err != nil {
		fmt.Fprintf(stderr, "ts2bin emit-vert012: publish artifacts: %v\n", err)
		return exitUsage
	}
	fmt.Fprintf(stdout, "[ok] emit-vert012 %s %s\n", outputPath, report.ContentHash)
	return exitSuccess
}

func canonicalVERT012Outputs(result bingomir.VERT012PipelineResult, plan buildplan.Plan) (map[string][]byte, vert012ArtifactReport, error) {
	contract, _, err := bingo.CanonicalClosureContract(result.Replay.Contract)
	if err != nil {
		return nil, vert012ArtifactReport{}, err
	}
	hir, _, err := bingo.CanonicalVERT012ClosureHIR(result.Replay.HIR)
	if err != nil {
		return nil, vert012ArtifactReport{}, err
	}
	cellLayout, _, err := bingo.CanonicalObjectLayoutContract(result.CellLayout)
	if err != nil {
		return nil, vert012ArtifactReport{}, err
	}
	environmentLayout, _, err := bingo.CanonicalObjectLayoutContract(result.EnvironmentLayout)
	if err != nil {
		return nil, vert012ArtifactReport{}, err
	}
	mir, _, err := bingo.CanonicalVERT012MIR(result.MIR)
	if err != nil {
		return nil, vert012ArtifactReport{}, err
	}
	bound, _, err := bingo.CanonicalVERT012BoundMIR(result.BoundMIR)
	if err != nil {
		return nil, vert012ArtifactReport{}, err
	}
	report := vert012ArtifactReport{
		SchemaVersion: vert012ReportSchemaVersion, FrontendSnapshotHash: result.Replay.FrontendSnapshotHash,
		BuildPlanHash: plan.ContentHash, TargetContextHash: result.Resolution.Context.ContentHash,
		CapabilityCatalogHash: result.Resolution.Catalog.ContentHash, HIRHash: result.Replay.HIR.ContentHash,
		ClosureContractHash: result.Replay.Contract.ContentHash, CellLayoutHash: result.CellLayout.ContentHash,
		EnvironmentLayoutHash: result.EnvironmentLayout.ContentHash, MIRHash: result.MIR.ContentHash, BoundMIRHash: result.BoundMIR.ContentHash,
		LLVMIRHash: result.Emission.LLVMIRHash, ObjectHash: result.Emission.ObjectHash,
	}
	withoutHash, err := json.Marshal(report)
	if err != nil {
		return nil, vert012ArtifactReport{}, err
	}
	digest := sha256.Sum256(withoutHash)
	report.ContentHash = hex.EncodeToString(digest[:])
	reportBytes, err := report.CanonicalBytes()
	if err != nil {
		return nil, vert012ArtifactReport{}, err
	}
	return map[string][]byte{
		"closure-contract-v1.json": append(contract, '\n'), "hir-v11.json": append(hir, '\n'),
		"cell-layout-v1.json": append(cellLayout, '\n'), "environment-layout-v1.json": append(environmentLayout, '\n'),
		"mir-v9.json": append(mir, '\n'), "bound-mir-v1.json": append(bound, '\n'),
		"module.ll": result.Emission.LLVMIR, "module.o": result.Emission.Object, "report.json": append(reportBytes, '\n'),
	}, report, nil
}

func publishVERT012Outputs(directory string, artifacts map[string][]byte) error {
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
	for _, name := range []string{"closure-contract-v1.json", "hir-v11.json", "cell-layout-v1.json", "environment-layout-v1.json", "mir-v9.json", "bound-mir-v1.json", "module.ll", "module.o", "report.json"} {
		path := filepath.Join(directory, name)
		if err := artifactio.PublishNewFile(path, artifacts[name], 0o644); err != nil {
			return rollback(err)
		}
		created = append(created, path)
	}
	return nil
}
