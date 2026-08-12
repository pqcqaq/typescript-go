package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/microsoft/typescript-go/internal/artifactio"
	"github.com/microsoft/typescript-go/internal/ast2bingo"
	"github.com/microsoft/typescript-go/internal/bingo"
	"github.com/microsoft/typescript-go/internal/bingomir"
	"github.com/microsoft/typescript-go/internal/buildplan"
	"github.com/microsoft/typescript-go/internal/frontendwire"
	jsonx "github.com/microsoft/typescript-go/internal/json"
	"github.com/microsoft/typescript-go/internal/llvmbackend"
)

const propertyAccessUnboundReportSchemaVersion uint32 = 1

type propertyAccessUnboundReport struct {
	SchemaVersion        uint32 `json:"schemaVersion"`
	Stage                string `json:"stage"`
	FrontendSnapshotHash string `json:"frontendSnapshotHash"`
	BuildPlanHash        string `json:"buildPlanHash"`
	ReplayHash           string `json:"replayHash"`
	HIRHash              string `json:"hirHash"`
	MIRHash              string `json:"mirHash"`
	TargetTriple         string `json:"targetTriple"`
	DataLayoutHash       string `json:"dataLayoutHash"`
	ContentHash          string `json:"contentHash"`
}

func (report propertyAccessUnboundReport) CanonicalBytes() ([]byte, error) {
	if report.SchemaVersion != propertyAccessUnboundReportSchemaVersion || report.Stage != "property-access-unbound" {
		return nil, fmt.Errorf("unsupported property-access unbound report identity")
	}
	for name, value := range map[string]string{"frontend": report.FrontendSnapshotHash, "build plan": report.BuildPlanHash, "replay": report.ReplayHash, "HIR": report.HIRHash, "MIR": report.MIRHash, "data layout": report.DataLayoutHash} {
		decoded, err := hex.DecodeString(value)
		if err != nil || len(decoded) != sha256.Size || value != strings.ToLower(value) {
			return nil, fmt.Errorf("invalid property-access unbound report %s hash", name)
		}
	}
	if report.TargetTriple != llvmbackend.FirstSliceTriple {
		return nil, fmt.Errorf("unsupported property-access unbound report target")
	}
	without := report
	without.ContentHash = ""
	encoded, err := json.Marshal(without)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(encoded)
	if report.ContentHash != hex.EncodeToString(digest[:]) {
		return nil, fmt.Errorf("property-access unbound report content hash mismatch")
	}
	return json.Marshal(report)
}

func runEmitPropertyAccessReplay(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("emit-property-access-replay", flag.ContinueOnError)
	flags.SetOutput(stderr)
	output := flags.String("output", "", "new file for the canonical property-access replay")
	flags.StringVar(output, "o", "", "new file for the canonical property-access replay")
	if err := flags.Parse(args); err != nil || flags.NArg() != 1 || *output == "" {
		fmt.Fprintln(stderr, "ts2bin emit-property-access-replay: expected --output FILE SNAPSHOT")
		return exitUsage
	}
	snapshotBytes, err := os.ReadFile(flags.Arg(0))
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin emit-property-access-replay: read snapshot: %v\n", err)
		return exitUsage
	}
	frontend, err := frontendwire.DecodeFrontendSnapshot(snapshotBytes)
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin emit-property-access-replay: decode snapshot: %v\n", err)
		return exitDiagnostics
	}
	identity, err := loadCompilerBuildIdentity()
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin emit-property-access-replay: compiler identity: %v\n", err)
		return exitUsage
	}
	replay, err := ast2bingo.ReplayPropertyAccessAdmissionSnapshot(frontend.Program, identity)
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin emit-property-access-replay: %v\n", err)
		return exitDiagnostics
	}
	encoded, err := replay.CanonicalBytes()
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin emit-property-access-replay: encode replay: %v\n", err)
		return exitDiagnostics
	}
	outputPath, err := filepath.Abs(*output)
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin emit-property-access-replay: resolve output: %v\n", err)
		return exitUsage
	}
	if err := artifactio.PublishNewFile(outputPath, append(encoded, '\n'), 0o644); err != nil {
		fmt.Fprintf(stderr, "ts2bin emit-property-access-replay: publish: %v\n", err)
		return exitUsage
	}
	fmt.Fprintf(stdout, "[ok] emit-property-access-replay %s %s\n", outputPath, replay.ContentHash)
	return exitSuccess
}

func runEmitPropertyAccessUnbound(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("emit-property-access-unbound", flag.ContinueOnError)
	flags.SetOutput(stderr)
	outputDirectory := flags.String("output-dir", "", "new directory for verified unbound property-access artifacts")
	if err := flags.Parse(args); err != nil || flags.NArg() != 1 || *outputDirectory == "" {
		fmt.Fprintln(stderr, "ts2bin emit-property-access-unbound: expected --output-dir DIR SNAPSHOT")
		return exitUsage
	}
	snapshotBytes, err := os.ReadFile(flags.Arg(0))
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin emit-property-access-unbound: read snapshot: %v\n", err)
		return exitUsage
	}
	frontend, err := frontendwire.DecodeFrontendSnapshot(snapshotBytes)
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin emit-property-access-unbound: decode snapshot: %v\n", err)
		return exitDiagnostics
	}
	identity, err := loadCompilerBuildIdentity()
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin emit-property-access-unbound: compiler identity: %v\n", err)
		return exitUsage
	}
	plan, err := buildplan.New(frontend.Program.ContentHash, frontendwire.ProfileInterop, buildplan.BackendRequest{Target: llvmbackend.FirstSliceTriple, CPU: llvmbackend.FirstSliceCPU, Features: []string{}, Runtime: "core-es2020", GC: frontendwire.GCTracing, Exceptions: frontendwire.ExceptionsNone, Overflow: frontendwire.OverflowJSNumber, BoundsCheck: frontendwire.BoundsCheckOn, Emit: []frontendwire.EmitArtifact{frontendwire.EmitHIR, frontendwire.EmitMIR}, LLVMMajor: llvmbackend.LockedLLVMMajor})
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin emit-property-access-unbound: build plan: %v\n", err)
		return exitDiagnostics
	}
	machine, err := openStaticCoreTargetMachine()
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin emit-property-access-unbound: %v\n", err)
		return exitDiagnostics
	}
	if machine != nil {
		defer machine.Close()
	}
	result, err := bingomir.LowerPropertyAccess(frontend.Program, identity, plan, machine)
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin emit-property-access-unbound: %v\n", err)
		return exitDiagnostics
	}
	artifacts, report, err := canonicalPropertyAccessUnboundOutputs(result, plan)
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin emit-property-access-unbound: encode artifacts: %v\n", err)
		return exitDiagnostics
	}
	outputPath, err := filepath.Abs(*outputDirectory)
	if err != nil {
		return exitUsage
	}
	if err := publishPropertyAccessUnboundOutputs(outputPath, artifacts); err != nil {
		fmt.Fprintf(stderr, "ts2bin emit-property-access-unbound: publish: %v\n", err)
		return exitUsage
	}
	fmt.Fprintf(stdout, "[ok] emit-property-access-unbound %s %s\n", outputPath, report.ContentHash)
	return exitSuccess
}

func canonicalPropertyAccessUnboundOutputs(result bingomir.PropertyAccessLoweringResult, plan buildplan.Plan) (map[string][]byte, propertyAccessUnboundReport, error) {
	if err := buildplan.Validate(plan); err != nil {
		return nil, propertyAccessUnboundReport{}, fmt.Errorf("property-access unbound BuildPlan: %w", err)
	}
	if plan.Profile != frontendwire.ProfileInterop || plan.FrontendHash != result.Replay.FrontendSnapshotHash {
		return nil, propertyAccessUnboundReport{}, fmt.Errorf("property-access unbound BuildPlan does not bind interop replay")
	}
	if result.HIR.FrontendSnapshotHash != result.Replay.FrontendSnapshotHash || result.HIR.ReplayHash != result.Replay.ContentHash || result.MIR.HIRHash != result.HIR.ContentHash || result.MIR.HIR.ContentHash != result.HIR.ContentHash {
		return nil, propertyAccessUnboundReport{}, fmt.Errorf("property-access unbound artifact provenance is inconsistent")
	}
	replay, err := result.Replay.CanonicalBytes()
	if err != nil {
		return nil, propertyAccessUnboundReport{}, err
	}
	hir, _, err := bingo.CanonicalPropertyAccessHIRArtifact(result.HIR)
	if err != nil {
		return nil, propertyAccessUnboundReport{}, err
	}
	mir, _, err := bingo.CanonicalPropertyAccessMIR(result.MIR)
	if err != nil {
		return nil, propertyAccessUnboundReport{}, err
	}
	report := propertyAccessUnboundReport{SchemaVersion: propertyAccessUnboundReportSchemaVersion, Stage: "property-access-unbound", FrontendSnapshotHash: result.Replay.FrontendSnapshotHash, BuildPlanHash: plan.ContentHash, ReplayHash: result.Replay.ContentHash, HIRHash: result.HIR.ContentHash, MIRHash: result.MIR.ContentHash, TargetTriple: result.MIR.TargetTriple, DataLayoutHash: result.MIR.DataLayoutHash}
	without := report
	encoded, _ := json.Marshal(without)
	digest := sha256.Sum256(encoded)
	report.ContentHash = hex.EncodeToString(digest[:])
	reportBytes, err := report.CanonicalBytes()
	if err != nil {
		return nil, propertyAccessUnboundReport{}, err
	}
	return map[string][]byte{"replay-v1.json": append(replay, '\n'), "hir-v1.json": append(hir, '\n'), "unbound-mir-v1.json": append(mir, '\n'), "report.json": append(reportBytes, '\n')}, report, nil
}

var propertyAccessUnboundArtifactNames = []string{"replay-v1.json", "hir-v1.json", "unbound-mir-v1.json", "report.json"}

func publishPropertyAccessUnboundOutputs(directory string, artifacts map[string][]byte) error {
	if len(artifacts) != len(propertyAccessUnboundArtifactNames) {
		return fmt.Errorf("property-access unbound artifact set is incomplete")
	}
	for _, name := range propertyAccessUnboundArtifactNames {
		if len(artifacts[name]) == 0 {
			return fmt.Errorf("property-access unbound artifact %s is missing", name)
		}
	}
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
	for _, name := range propertyAccessUnboundArtifactNames {
		path := filepath.Join(directory, name)
		if err := artifactio.PublishNewFile(path, artifacts[name], 0o644); err != nil {
			return rollback(err)
		}
		created = append(created, path)
	}
	return nil
}

func decodePropertyAccessUnboundReport(data []byte) (*propertyAccessUnboundReport, error) {
	var report propertyAccessUnboundReport
	if err := jsonx.Unmarshal(data, &report, jsonx.RejectUnknownMembers(true)); err != nil {
		return nil, err
	}
	if _, err := report.CanonicalBytes(); err != nil {
		return nil, err
	}
	return &report, nil
}
