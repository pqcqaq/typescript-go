package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"github.com/microsoft/typescript-go/internal/applicationbuild"
	"github.com/microsoft/typescript-go/internal/artifactio"
	"github.com/microsoft/typescript-go/internal/ast2bingo"
	"github.com/microsoft/typescript-go/internal/bingo"
	"github.com/microsoft/typescript-go/internal/bingomir"
	"github.com/microsoft/typescript-go/internal/core"
	"github.com/microsoft/typescript-go/internal/firstslicerunner"
	"github.com/microsoft/typescript-go/internal/irartifact"
	"github.com/microsoft/typescript-go/internal/llvmbackend"
	"github.com/microsoft/typescript-go/internal/tsfrontend"
)

const (
	exitSuccess     = 0
	exitDiagnostics = 1
	exitUsage       = 2
)

const commandReportSchemaVersion = 1

var (
	loadCompilerBuildIdentity   = ast2bingo.InjectedCompilerBuildIdentity
	openStaticCoreTargetMachine = llvmbackend.OpenFirstSliceTargetMachine
	executeStaticCoreCase       = firstslicerunner.RunCase
	executeApplicationBuild     = applicationbuild.Build
	executeVERT010Pipeline      = bingomir.ExecuteVERT010
	executeVERT011Pipeline      = bingomir.ExecuteVERT011
	executeVERT012Pipeline      = bingomir.ExecuteVERT012
	executeVERT013aPipeline     = bingomir.ExecuteVERT013a
	publishApplicationReport    = artifactio.PublishNewFile
)

type processResult struct {
	Output   []byte
	ExitCode int
}

type commandEnvironment struct {
	goos                 string
	getwd                func() (string, error)
	lookPath             func(string) (string, error)
	execute              func(context.Context, string, string, ...string) (processResult, error)
	collectCompatibility func(context.Context, string) (tsfrontend.CompatibilitySnapshot, error)
}

type doctorReport struct {
	SchemaVersion int           `json:"schemaVersion"`
	OK            bool          `json:"ok"`
	ExitCode      int           `json:"exitCode"`
	Checks        []doctorCheck `json:"checks"`
	Messages      []string      `json:"messages,omitempty"`
	Error         string        `json:"error,omitempty"`
}

type doctorCheck struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

type frontendTestReport struct {
	SchemaVersion int      `json:"schemaVersion"`
	Stage         string   `json:"stage"`
	OK            bool     `json:"ok"`
	Runner        string   `json:"runner,omitempty"`
	Output        []string `json:"output,omitempty"`
	Error         string   `json:"error,omitempty"`
}

func main() {
	core.ApplyDebugStackLimit()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	return runWithEnvironment(ctx, args, stdout, stderr, defaultCommandEnvironment())
}

func runWithEnvironment(ctx context.Context, args []string, stdout, stderr io.Writer, environment commandEnvironment) int {
	if len(args) == 0 {
		writeUsage(stderr)
		return exitUsage
	}
	switch args[0] {
	case "version":
		return runVersion(args[1:], stdout, stderr)
	case "check":
		return runCheck(ctx, args[1:], stdout, stderr)
	case "snapshot", "dump-snapshot":
		return runSnapshot(ctx, args[0], args[1:], stdout, stderr)
	case "emit-hir":
		return runEmitHIR(ctx, args[1:], stdout, stderr)
	case "emit-mir":
		return runEmitMIR(ctx, args[1:], stdout, stderr)
	case "emit-vert010":
		return runEmitVERT010(ctx, args[1:], stdout, stderr)
	case "emit-vert011":
		return runEmitVERT011(ctx, args[1:], stdout, stderr)
	case "emit-vert012":
		return runEmitVERT012(ctx, args[1:], stdout, stderr)
	case "emit-vert013a":
		return runEmitVERT013a(ctx, args[1:], stdout, stderr)
	case "emit-vert013b":
		return runEmitVERT013b(ctx, args[1:], stdout, stderr)
	case "emit-classaccess":
		return runEmitClassAccess(ctx, args[1:], stdout, stderr)
	case "emit-checked-cast-replay":
		return runEmitCheckedCastReplay(args[1:], stdout, stderr)
	case "emit-function-thunk-replay":
		return runEmitFunctionThunkReplay(args[1:], stdout, stderr)
	case "emit-object-layout-copy-replay":
		return runEmitObjectLayoutCopyReplay(args[1:], stdout, stderr)
	case "emit-property-access-replay":
		return runEmitPropertyAccessReplay(args[1:], stdout, stderr)
	case "emit-property-access-unbound":
		return runEmitPropertyAccessUnbound(args[1:], stdout, stderr)
	case "diff":
		return runIRDiff(args[1:], stdout, stderr)
	case "doctor":
		return runDoctor(ctx, args[1:], stdout, stderr, environment)
	case "compatibility", "upstream-audit":
		return runCompatibility(ctx, args[0], args[1:], stdout, stderr, environment)
	case "test":
		return runTests(ctx, args[1:], stdout, stderr, environment)
	case "build":
		return runBuild(ctx, args[1:], stdout, stderr, environment)
	case "help", "-h", "--help":
		writeUsage(stdout)
		return exitSuccess
	default:
		fmt.Fprintf(stderr, "ts2bin: unknown command %q\n", args[0])
		writeUsage(stderr)
		return exitUsage
	}
}

func runBuild(ctx context.Context, args []string, stdout, stderr io.Writer, environment commandEnvironment) int {
	flags := flag.NewFlagSet("build", flag.ContinueOnError)
	flags.SetOutput(stderr)
	project := flags.String("project", "tsconfig.json", "path to tsconfig.json or its directory")
	flags.StringVar(project, "p", "tsconfig.json", "path to tsconfig.json or its directory")
	output := flags.String("output", "a.out", "write the Linux x86-64 executable to this path")
	flags.StringVar(output, "o", "a.out", "write the Linux x86-64 executable to this path")
	if err := flags.Parse(args); err != nil {
		return exitUsage
	}
	if flags.NArg() > 1 {
		fmt.Fprintln(stderr, "ts2bin build: expected at most one project path")
		return exitUsage
	}
	if flags.NArg() == 1 {
		*project = flags.Arg(0)
	}
	configPath, err := resolveConfigPath(*project)
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin build: %v\n", err)
		return exitUsage
	}
	configPath, err = filepath.Abs(configPath)
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin build: resolve project path: %v\n", err)
		return exitUsage
	}
	outputPath, err := filepath.Abs(*output)
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin build: resolve output path: %v\n", err)
		return exitUsage
	}
	reportPath := outputPath + ".report.json"
	for _, path := range []string{outputPath, reportPath} {
		if _, statErr := os.Stat(path); statErr == nil {
			fmt.Fprintf(stderr, "ts2bin build: output already exists: %s\n", path)
			return exitUsage
		} else if !errors.Is(statErr, os.ErrNotExist) {
			fmt.Fprintf(stderr, "ts2bin build: inspect output %s: %v\n", path, statErr)
			return exitUsage
		}
	}
	repositoryRoot, err := resolveRepositoryRoot(environment)
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin build: %v\n", err)
		return exitUsage
	}
	identity, err := loadCompilerBuildIdentity()
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin build: compiler identity: %v\n", err)
		return exitUsage
	}
	machine, err := openStaticCoreTargetMachine()
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin build: %v\n", err)
		return exitDiagnostics
	}
	if machine != nil {
		defer machine.Close()
	}
	clang, err := firstAvailableTool(environment.lookPath, "clang-20", "clang")
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin build: Clang 20 not found: %v\n", err)
		return exitDiagnostics
	}
	lld, err := firstAvailableTool(environment.lookPath, "ld.lld-20", "ld.lld")
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin build: LLD 20 not found: %v\n", err)
		return exitDiagnostics
	}
	runtimeDirectory := filepath.Join(repositoryRoot, "runtime", "bingo-rt", "target", "first-slice")
	runtimeArchive := filepath.Join(runtimeDirectory, "cargo", llvmbackend.FirstSliceTriple, "release", "libbingo_runtime.a")
	report, err := executeApplicationBuild(ctx, identity, machine, applicationbuild.Request{
		ConfigPath: configPath, OutputPath: outputPath,
		RuntimeDirectory: runtimeDirectory, RuntimeArchivePath: runtimeArchive,
		Clang: clang, LLD: lld,
	})
	if err != nil {
		var diagnostics *applicationbuild.DiagnosticsError
		if errors.As(err, &diagnostics) {
			for _, diagnostic := range diagnostics.Diagnostics {
				writeDiagnostic(stderr, diagnostic)
			}
		} else {
			fmt.Fprintf(stderr, "ts2bin build: %v\n", err)
		}
		return exitDiagnostics
	}
	reportBytes, err := report.CanonicalBytes()
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin build: encode report: %v\n", rollbackApplicationOutput(outputPath, err))
		return exitDiagnostics
	}
	if err := publishApplicationReport(reportPath, append(reportBytes, '\n'), 0o644); err != nil {
		fmt.Fprintf(stderr, "ts2bin build: write report: %v\n", rollbackApplicationOutput(outputPath, err))
		return exitUsage
	}
	fmt.Fprintf(stdout, "[ok] build %s %s\n", outputPath, report.Artifacts.ExecutableHash)
	return exitSuccess
}

func defaultCommandEnvironment() commandEnvironment {
	return commandEnvironment{
		goos:                 runtime.GOOS,
		getwd:                os.Getwd,
		lookPath:             exec.LookPath,
		execute:              executeProcess,
		collectCompatibility: tsfrontend.CollectCurrentCompatibilitySnapshot,
	}
}

func runVersion(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("version", flag.ContinueOnError)
	flags.SetOutput(stderr)
	jsonOutput := flags.Bool("json", false, "print build provenance as JSON")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return exitUsage
	}
	info := tsfrontend.CurrentBuildInfo()
	if *jsonOutput {
		data, err := json.MarshalIndent(info, "", "  ")
		if err != nil {
			fmt.Fprintf(stderr, "ts2bin: encode build info: %v\n", err)
			return exitUsage
		}
		fmt.Fprintln(stdout, string(data))
		return exitSuccess
	}
	fmt.Fprintf(stdout, "ts2bin (TypeScript %s, tsgo %s, Go %s, LLVM %d)\n",
		info.TypeScriptVersion,
		shortCommit(info.TypeScriptGoCommit),
		info.GoVersion,
		info.LLVMMajor,
	)
	return exitSuccess
}

func runCheck(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("check", flag.ContinueOnError)
	flags.SetOutput(stderr)
	project := flags.String("project", "tsconfig.json", "path to tsconfig.json or its directory")
	flags.StringVar(project, "p", "tsconfig.json", "path to tsconfig.json or its directory")
	profile := flags.String("profile", string(tsfrontend.ProfileStatic), "static, interop, unsafe, or dynamic")
	jsonOutput := flags.Bool("json", false, "print structured diagnostics as JSON")
	if err := flags.Parse(args); err != nil {
		return exitUsage
	}
	if flags.NArg() > 1 {
		fmt.Fprintln(stderr, "ts2bin check: expected at most one project path")
		return exitUsage
	}
	if flags.NArg() == 1 {
		*project = flags.Arg(0)
	}
	profileOverride := explicitProfileOverride(flags, *profile)
	configPath, err := resolveConfigPath(*project)
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin check: %v\n", err)
		return exitUsage
	}
	result, err := tsfrontend.Check(ctx, tsfrontend.BuildRequest{
		ConfigPath:           configPath,
		BingoProfileOverride: profileOverride,
	})
	if err != nil {
		if errors.Is(err, context.Canceled) {
			fmt.Fprintln(stderr, "ts2bin: canceled")
		} else {
			fmt.Fprintf(stderr, "ts2bin: %v\n", err)
		}
		return exitUsage
	}
	if *jsonOutput {
		data, err := tsfrontend.CanonicalDiagnosticsJSON(result.Diagnostics)
		if err != nil {
			fmt.Fprintf(stderr, "ts2bin: encode diagnostics: %v\n", err)
			return exitUsage
		}
		fmt.Fprintln(stdout, string(data))
	} else {
		for _, diagnostic := range result.Diagnostics {
			writeDiagnostic(stdout, diagnostic)
		}
	}
	if tsfrontend.DiagnosticsHaveErrors(result.Diagnostics) {
		return exitDiagnostics
	}
	return exitSuccess
}

func runSnapshot(ctx context.Context, command string, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(stderr)
	project := flags.String("project", "tsconfig.json", "path to tsconfig.json or its directory")
	flags.StringVar(project, "p", "tsconfig.json", "path to tsconfig.json or its directory")
	profile := flags.String("profile", string(tsfrontend.ProfileStatic), "static, interop, unsafe, or dynamic")
	output := flags.String("output", "", "write snapshot JSON to this file instead of stdout")
	flags.StringVar(output, "o", "", "write snapshot JSON to this file instead of stdout")
	flags.Bool("json", false, "emit the snapshot as JSON")
	verifyDeterminism := flags.Bool("verify-determinism", false, "build twice and require byte-identical snapshots")
	if err := flags.Parse(args); err != nil {
		return exitUsage
	}
	if flags.NArg() > 1 {
		fmt.Fprintf(stderr, "ts2bin %s: expected at most one project path\n", command)
		return exitUsage
	}
	if flags.NArg() == 1 {
		*project = flags.Arg(0)
	}
	profileOverride := explicitProfileOverride(flags, *profile)
	configPath, err := resolveConfigPath(*project)
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin %s: %v\n", command, err)
		return exitUsage
	}
	request := tsfrontend.BuildRequest{
		ConfigPath:           configPath,
		BingoProfileOverride: profileOverride,
	}
	first, diagnostics := tsfrontend.NewOSFrontend(tsfrontend.TypeScriptGoCommit).Build(ctx, request)
	if err := ctx.Err(); err != nil {
		fmt.Fprintf(stderr, "ts2bin %s: %v\n", command, err)
		return exitUsage
	}
	if first == nil {
		writeSnapshotDiagnostics(stderr, command, diagnostics)
		return exitCodeForDiagnostics(diagnostics)
	}
	firstFrontend, err := tsfrontend.NewFrontendSnapshot(*first)
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin %s: validate frontend snapshot: %v\n", command, err)
		return exitDiagnostics
	}
	firstBytes, err := firstFrontend.CanonicalBytes()
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin %s: encode snapshot: %v\n", command, err)
		return exitUsage
	}
	if *verifyDeterminism {
		second, secondDiagnostics := tsfrontend.NewOSFrontend(tsfrontend.TypeScriptGoCommit).Build(ctx, request)
		if err := ctx.Err(); err != nil {
			fmt.Fprintf(stderr, "ts2bin %s: %v\n", command, err)
			return exitUsage
		}
		if second == nil {
			fmt.Fprintf(stderr, "ts2bin %s: determinism rebuild did not produce a snapshot\n", command)
			writeSnapshotDiagnostics(stderr, command, secondDiagnostics)
			return exitDiagnostics
		}
		secondFrontend, err := tsfrontend.NewFrontendSnapshot(*second)
		if err != nil {
			fmt.Fprintf(stderr, "ts2bin %s: validate determinism snapshot: %v\n", command, err)
			return exitDiagnostics
		}
		secondBytes, err := secondFrontend.CanonicalBytes()
		if err != nil {
			fmt.Fprintf(stderr, "ts2bin %s: encode determinism snapshot: %v\n", command, err)
			return exitUsage
		}
		if firstFrontend.ContentHash != secondFrontend.ContentHash || !bytes.Equal(firstBytes, secondBytes) {
			fmt.Fprintf(stderr, "ts2bin %s: nondeterministic snapshot: first=%s second=%s bytesEqual=%t\n",
				command, firstFrontend.ContentHash, secondFrontend.ContentHash, bytes.Equal(firstBytes, secondBytes))
			return exitDiagnostics
		}
	}
	if err := writeSnapshotOutput(*output, firstBytes, stdout); err != nil {
		fmt.Fprintf(stderr, "ts2bin %s: write snapshot: %v\n", command, err)
		return exitUsage
	}
	if tsfrontend.DiagnosticsHaveErrors(diagnostics) {
		return exitDiagnostics
	}
	return exitSuccess
}

func explicitProfileOverride(flags *flag.FlagSet, value string) *tsfrontend.Profile {
	var specified bool
	flags.Visit(func(current *flag.Flag) {
		if current.Name == "profile" {
			specified = true
		}
	})
	if !specified {
		return nil
	}
	profile := tsfrontend.Profile(value)
	return &profile
}

func writeSnapshotDiagnostics(writer io.Writer, command string, diagnostics []tsfrontend.Diagnostic) {
	data, err := tsfrontend.CanonicalDiagnosticsJSON(diagnostics)
	if err != nil {
		fmt.Fprintf(writer, "ts2bin %s: encode diagnostics: %v\n", command, err)
		return
	}
	_, _ = writer.Write(data)
	_, _ = io.WriteString(writer, "\n")
}

func exitCodeForDiagnostics(diagnostics []tsfrontend.Diagnostic) int {
	if tsfrontend.DiagnosticsHaveErrors(diagnostics) {
		return exitDiagnostics
	}
	return exitUsage
}

func writeSnapshotOutput(output string, data []byte, stdout io.Writer) error {
	if output == "" || output == "-" {
		if _, err := stdout.Write(data); err != nil {
			return err
		}
		_, err := io.WriteString(stdout, "\n")
		return err
	}
	return os.WriteFile(output, data, 0o644)
}

func runEmitHIR(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("emit-hir", flag.ContinueOnError)
	flags.SetOutput(stderr)
	output := flags.String("output", "", "write HIR to this file instead of stdout")
	flags.StringVar(output, "o", "", "write HIR to this file instead of stdout")
	format := flags.String("format", "json", "output format: json or text")
	verify := flags.Bool("verify", false, "re-decode and verify the emitted HIR artifact")
	if err := flags.Parse(args); err != nil || flags.NArg() != 1 {
		fmt.Fprintln(stderr, "ts2bin emit-hir: expected exactly one case directory")
		return exitUsage
	}
	identity, err := loadCompilerBuildIdentity()
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin emit-hir: compiler identity: %v\n", err)
		return exitUsage
	}
	module, err := irartifact.LoadHIR(flags.Arg(0), identity)
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin emit-hir: %v\n", err)
		return exitDiagnostics
	}
	canonical, err := irartifact.CanonicalHIR(module)
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin emit-hir: encode artifact: %v\n", err)
		return exitDiagnostics
	}
	if *verify {
		if _, err := irartifact.DecodeHIR(canonical); err != nil {
			fmt.Fprintf(stderr, "ts2bin emit-hir: verify emitted artifact: %v\n", err)
			return exitDiagnostics
		}
	}
	data, err := formatHIRArtifact(*format, canonical, module)
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin emit-hir: %v\n", err)
		return exitUsage
	}
	if err := writeIRArtifactOutput(*output, data, stdout); err != nil {
		fmt.Fprintf(stderr, "ts2bin emit-hir: write output: %v\n", err)
		return exitUsage
	}
	return exitSuccess
}

func formatHIRArtifact(format string, canonical []byte, module bingo.HIRModule) ([]byte, error) {
	switch format {
	case "json":
		return canonical, nil
	case "text":
		rendered, err := irartifact.RenderHIRText(module)
		return []byte(rendered), err
	default:
		return nil, fmt.Errorf("unsupported format %q", format)
	}
}

func runEmitMIR(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("emit-mir", flag.ContinueOnError)
	flags.SetOutput(stderr)
	output := flags.String("output", "", "write MIR to this file instead of stdout")
	flags.StringVar(output, "o", "", "write MIR to this file instead of stdout")
	format := flags.String("format", "json", "output format: json or text")
	verify := flags.Bool("verify", false, "re-decode and verify the emitted MIR artifact")
	if err := flags.Parse(args); err != nil || flags.NArg() != 1 {
		fmt.Fprintln(stderr, "ts2bin emit-mir: expected exactly one case directory")
		return exitUsage
	}
	identity, err := loadCompilerBuildIdentity()
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin emit-mir: compiler identity: %v\n", err)
		return exitUsage
	}
	machine, err := llvmbackend.OpenFirstSliceTargetMachine()
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin emit-mir: %v\n", err)
		return exitDiagnostics
	}
	defer machine.Close()
	module, err := irartifact.LoadMIR(ctx, flags.Arg(0), identity, machine)
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin emit-mir: %v\n", err)
		return exitDiagnostics
	}
	canonical, err := irartifact.CanonicalMIR(module)
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin emit-mir: encode artifact: %v\n", err)
		return exitDiagnostics
	}
	if *verify {
		if _, err := irartifact.DecodeMIR(canonical); err != nil {
			fmt.Fprintf(stderr, "ts2bin emit-mir: verify emitted artifact: %v\n", err)
			return exitDiagnostics
		}
	}
	data, err := formatMIRArtifact(*format, canonical, module)
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin emit-mir: %v\n", err)
		return exitUsage
	}
	if err := writeIRArtifactOutput(*output, data, stdout); err != nil {
		fmt.Fprintf(stderr, "ts2bin emit-mir: write output: %v\n", err)
		return exitUsage
	}
	return exitSuccess
}

func formatMIRArtifact(format string, canonical []byte, module bingo.FirstSliceMIRArtifact) ([]byte, error) {
	switch format {
	case "json":
		return canonical, nil
	case "text":
		rendered, err := irartifact.RenderMIRText(module)
		return []byte(rendered), err
	default:
		return nil, fmt.Errorf("unsupported format %q", format)
	}
}

func runIRDiff(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("diff", flag.ContinueOnError)
	flags.SetOutput(stderr)
	kind := flags.String("kind", "", "IR kind: hir or mir")
	output := flags.String("output", "", "write diff report to this file instead of stdout")
	flags.StringVar(output, "o", "", "write diff report to this file instead of stdout")
	format := flags.String("format", "json", "output format: json or text")
	if err := flags.Parse(args); err != nil || flags.NArg() != 2 {
		fmt.Fprintln(stderr, "ts2bin diff: expected --kind hir|mir and two artifact paths")
		return exitUsage
	}
	if *kind != string(irartifact.KindHIR) && *kind != string(irartifact.KindMIR) {
		fmt.Fprintf(stderr, "ts2bin diff: unsupported IR kind %q\n", *kind)
		return exitUsage
	}
	left, err := os.ReadFile(flags.Arg(0))
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin diff: read left artifact: %v\n", err)
		return exitUsage
	}
	right, err := os.ReadFile(flags.Arg(1))
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin diff: read right artifact: %v\n", err)
		return exitUsage
	}
	report, err := irartifact.Diff(irartifact.Kind(*kind), left, right)
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin diff: %v\n", err)
		return exitDiagnostics
	}
	data, err := formatDiffReport(*format, report)
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin diff: %v\n", err)
		return exitUsage
	}
	if err := writeIRArtifactOutput(*output, data, stdout); err != nil {
		fmt.Fprintf(stderr, "ts2bin diff: write output: %v\n", err)
		return exitUsage
	}
	if !report.Equal {
		return exitDiagnostics
	}
	return exitSuccess
}

func formatDiffReport(format string, report irartifact.DiffReport) ([]byte, error) {
	switch format {
	case "json":
		return report.CanonicalBytes()
	case "text":
		rendered, err := irartifact.RenderDiffText(report)
		return []byte(rendered), err
	default:
		return nil, fmt.Errorf("unsupported format %q", format)
	}
}

func writeIRArtifactOutput(output string, data []byte, stdout io.Writer) error {
	if output == "" || output == "-" {
		if _, err := stdout.Write(data); err != nil {
			return err
		}
		if len(data) == 0 || data[len(data)-1] != '\n' {
			_, err := io.WriteString(stdout, "\n")
			return err
		}
		return nil
	}
	return os.WriteFile(output, data, 0o644)
}

func runDoctor(ctx context.Context, args []string, stdout, stderr io.Writer, environment commandEnvironment) int {
	flags := flag.NewFlagSet("doctor", flag.ContinueOnError)
	flags.SetOutput(stderr)
	jsonOutput := flags.Bool("json", false, "print the doctor report as JSON")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return exitUsage
	}
	repositoryRoot, err := resolveRepositoryRoot(environment)
	if err != nil {
		return writeDoctorFailure(stdout, stderr, *jsonOutput, err)
	}
	goos := environment.goos
	if goos == "" {
		goos = runtime.GOOS
	}
	scriptName := "doctor.sh"
	if goos == "windows" {
		scriptName = "doctor.ps1"
	}
	script := filepath.Join(repositoryRoot, "scripts", scriptName)
	if info, statErr := os.Stat(script); statErr != nil || info.IsDir() {
		if statErr == nil {
			statErr = fmt.Errorf("%s is not a file", script)
		}
		return writeDoctorFailure(stdout, stderr, *jsonOutput, fmt.Errorf("doctor script unavailable: %w", statErr))
	}
	name, commandArgs, err := doctorCommand(goos, script, environment.lookPath)
	if err != nil {
		return writeDoctorFailure(stdout, stderr, *jsonOutput, err)
	}
	result, err := environment.execute(ctx, repositoryRoot, name, commandArgs...)
	if err != nil {
		return writeDoctorFailure(stdout, stderr, *jsonOutput, err)
	}
	checks, messages := parseDoctorOutput(result.Output)
	if *jsonOutput {
		report := doctorReport{SchemaVersion: commandReportSchemaVersion, OK: result.ExitCode == 0, ExitCode: result.ExitCode, Checks: checks, Messages: messages}
		if err := writeStableJSON(stdout, report); err != nil {
			fmt.Fprintf(stderr, "ts2bin doctor: encode report: %v\n", err)
			return exitUsage
		}
	} else {
		writeProcessOutput(stdout, result.Output)
	}
	if result.ExitCode != 0 {
		return exitDiagnostics
	}
	return exitSuccess
}

func writeDoctorFailure(stdout, stderr io.Writer, jsonOutput bool, err error) int {
	if jsonOutput {
		report := doctorReport{SchemaVersion: commandReportSchemaVersion, OK: false, ExitCode: -1, Checks: []doctorCheck{}, Error: err.Error()}
		if encodeErr := writeStableJSON(stdout, report); encodeErr != nil {
			fmt.Fprintf(stderr, "ts2bin doctor: encode report: %v\n", encodeErr)
		}
	} else {
		fmt.Fprintf(stderr, "ts2bin doctor: %v\n", err)
	}
	return exitUsage
}

func runTests(ctx context.Context, args []string, stdout, stderr io.Writer, environment commandEnvironment) int {
	flags := flag.NewFlagSet("test", flag.ContinueOnError)
	flags.SetOutput(stderr)
	stage := flags.String("stage", "", "test stage: frontend or static-core")
	casePath := flags.String("case", "", "static-core case directory (defaults to lowering)")
	jsonOutput := flags.Bool("json", false, "print the test report as JSON")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return exitUsage
	}
	if *stage == "static-core" {
		return runStaticCoreTests(ctx, stdout, stderr, *jsonOutput, *casePath, environment)
	}
	if *stage != "frontend" {
		err := fmt.Errorf("unsupported or missing stage %q; expected frontend or static-core", *stage)
		return writeFrontendTestFailure(stdout, stderr, *jsonOutput, "", err, exitUsage)
	}
	repositoryRoot, err := resolveRepositoryRoot(environment)
	if err != nil {
		return writeFrontendTestFailure(stdout, stderr, *jsonOutput, "", err, exitUsage)
	}
	runner, err := resolveFrontendRunner(repositoryRoot)
	if err != nil {
		return writeFrontendTestFailure(stdout, stderr, *jsonOutput, "", err, exitUsage)
	}
	name, runnerArgs, err := frontendRunnerCommand(runner, environment.lookPath)
	if err != nil {
		return writeFrontendTestFailure(stdout, stderr, *jsonOutput, runner, err, exitUsage)
	}
	result, err := environment.execute(ctx, repositoryRoot, name, runnerArgs...)
	if err != nil {
		return writeFrontendTestFailure(stdout, stderr, *jsonOutput, runner, err, exitUsage)
	}
	lines := normalizedOutputLines(result.Output)
	if *jsonOutput {
		report := frontendTestReport{SchemaVersion: commandReportSchemaVersion, Stage: "frontend", OK: result.ExitCode == 0, Runner: logicalRunnerPath(repositoryRoot, runner), Output: lines}
		if err := writeStableJSON(stdout, report); err != nil {
			fmt.Fprintf(stderr, "ts2bin test: encode report: %v\n", err)
			return exitUsage
		}
	} else {
		writeProcessOutput(stdout, result.Output)
	}
	if result.ExitCode != 0 {
		return exitDiagnostics
	}
	return exitSuccess
}

func runStaticCoreTests(ctx context.Context, stdout, stderr io.Writer, jsonOutput bool, casePath string, environment commandEnvironment) int {
	repositoryRoot, err := resolveRepositoryRoot(environment)
	if err != nil {
		return writeStaticCoreFailure(stdout, stderr, jsonOutput, firstslicerunner.Report{}, err, exitUsage)
	}
	identity, err := loadCompilerBuildIdentity()
	if err != nil {
		return writeStaticCoreFailure(stdout, stderr, jsonOutput, firstslicerunner.Report{}, fmt.Errorf("compiler identity: %w", err), exitUsage)
	}
	machine, err := openStaticCoreTargetMachine()
	if err != nil {
		return writeStaticCoreFailure(stdout, stderr, jsonOutput, firstslicerunner.Report{}, err, exitDiagnostics)
	}
	defer machine.Close()
	clang, err := firstAvailableTool(environment.lookPath, "clang-20", "clang")
	if err != nil {
		return writeStaticCoreFailure(stdout, stderr, jsonOutput, firstslicerunner.Report{}, fmt.Errorf("Clang 20 not found: %w", err), exitDiagnostics)
	}
	lld, err := firstAvailableTool(environment.lookPath, "ld.lld-20", "ld.lld")
	if err != nil {
		return writeStaticCoreFailure(stdout, stderr, jsonOutput, firstslicerunner.Report{}, fmt.Errorf("LLD 20 not found: %w", err), exitDiagnostics)
	}
	node, err := firstAvailableTool(environment.lookPath, "node")
	if err != nil {
		return writeStaticCoreFailure(stdout, stderr, jsonOutput, firstslicerunner.Report{}, fmt.Errorf("Node 22.22.0 not found: %w", err), exitDiagnostics)
	}
	outputDirectory, err := os.MkdirTemp("", "ts2bin-static-core-stage-")
	if err != nil {
		return writeStaticCoreFailure(stdout, stderr, jsonOutput, firstslicerunner.Report{}, err, exitUsage)
	}
	defer os.RemoveAll(outputDirectory)
	runtimeDirectory := filepath.Join(repositoryRoot, "runtime", "bingo-rt", "target", "first-slice")
	runtimeArchive := filepath.Join(runtimeDirectory, "cargo", llvmbackend.FirstSliceTriple, "release", "libbingo_runtime.a")
	caseDirectory := filepath.Join(repositoryRoot, "typescript-go", "testdata", "ts2bin", "lowering")
	if strings.TrimSpace(casePath) != "" {
		if filepath.IsAbs(casePath) {
			caseDirectory = filepath.Clean(casePath)
		} else {
			caseDirectory = filepath.Join(repositoryRoot, "typescript-go", filepath.Clean(casePath))
		}
	}
	report, err := executeStaticCoreCase(ctx, caseDirectory, identity, machine, firstslicerunner.Options{
		RuntimeDirectory: runtimeDirectory, RuntimeArchivePath: runtimeArchive,
		OutputDirectory: outputDirectory, Clang: clang, LLD: lld, Node: node,
	})
	if err != nil {
		return writeStaticCoreFailure(stdout, stderr, jsonOutput, report, err, exitDiagnostics)
	}
	if jsonOutput {
		data, err := report.CanonicalBytes()
		if err != nil {
			fmt.Fprintf(stderr, "ts2bin test: encode static-core report: %v\n", err)
			return exitUsage
		}
		_, _ = stdout.Write(data)
		_, _ = io.WriteString(stdout, "\n")
	} else {
		fmt.Fprintf(stdout, "[ok] static-core %s %s\n", report.CaseName, report.Artifacts.ExecutableHash)
		for _, execution := range report.Executions {
			fmt.Fprintf(stdout, "[ok] %s %s\n", execution.Name, execution.ActualBits)
		}
	}
	return exitSuccess
}

func writeStaticCoreFailure(stdout, stderr io.Writer, jsonOutput bool, report firstslicerunner.Report, err error, exitCode int) int {
	if jsonOutput && report.SchemaVersion != 0 {
		if data, encodeErr := report.CanonicalBytes(); encodeErr == nil {
			_, _ = stdout.Write(data)
			_, _ = io.WriteString(stdout, "\n")
		} else {
			fmt.Fprintf(stderr, "ts2bin test: encode failed static-core report: %v\n", encodeErr)
		}
	}
	fmt.Fprintf(stderr, "ts2bin test: static-core: %v\n", err)
	return exitCode
}

func firstAvailableTool(lookPath func(string) (string, error), names ...string) (string, error) {
	var last error
	for _, name := range names {
		if path, err := lookPath(name); err == nil {
			return path, nil
		} else {
			last = err
		}
	}
	return "", last
}

func writeFrontendTestFailure(stdout, stderr io.Writer, jsonOutput bool, runner string, err error, exitCode int) int {
	if jsonOutput {
		report := frontendTestReport{SchemaVersion: commandReportSchemaVersion, Stage: "frontend", OK: false, Runner: filepath.ToSlash(runner), Error: err.Error()}
		if encodeErr := writeStableJSON(stdout, report); encodeErr != nil {
			fmt.Fprintf(stderr, "ts2bin test: encode report: %v\n", encodeErr)
		}
	} else {
		fmt.Fprintf(stderr, "ts2bin test: %v\n", err)
	}
	return exitCode
}

func resolveFrontendRunner(repositoryRoot string) (string, error) {
	candidates := []string{
		filepath.Join(repositoryRoot, "scripts", "test-frontend.ps1"),
		filepath.Join(repositoryRoot, "scripts", "test-frontend.cmd"),
		filepath.Join(repositoryRoot, "scripts", "test-frontend.exe"),
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("frontend stage runner is not implemented (expected %s)", filepath.ToSlash(candidates[0]))
}

func frontendRunnerCommand(runner string, lookPath func(string) (string, error)) (string, []string, error) {
	switch strings.ToLower(filepath.Ext(runner)) {
	case ".ps1":
		powerShell, err := resolvePowerShell(lookPath)
		if err != nil {
			return "", nil, err
		}
		return powerShell, []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-File", runner}, nil
	case ".cmd", ".bat":
		commandInterpreter, err := lookPath("cmd")
		if err != nil {
			return "", nil, fmt.Errorf("Windows command interpreter not found: %w", err)
		}
		return commandInterpreter, []string{"/d", "/s", "/c", runner}, nil
	}
	return runner, nil, nil
}

func logicalRunnerPath(repositoryRoot, runner string) string {
	relative, err := filepath.Rel(repositoryRoot, runner)
	if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return filepath.ToSlash(relative)
	}
	return filepath.ToSlash(runner)
}

func resolveRepositoryRoot(environment commandEnvironment) (string, error) {
	workingDirectory, err := environment.getwd()
	if err != nil {
		return "", fmt.Errorf("get working directory: %w", err)
	}
	starts := []string{workingDirectory}
	if _, file, _, ok := runtime.Caller(0); ok {
		starts = append(starts, filepath.Dir(file))
	}
	for _, start := range starts {
		for directory := filepath.Clean(start); ; directory = filepath.Dir(directory) {
			lock := filepath.Join(directory, "ts2bin.lock.json")
			scripts := filepath.Join(directory, "scripts")
			if lockInfo, lockErr := os.Stat(lock); lockErr == nil && !lockInfo.IsDir() {
				if scriptsInfo, scriptsErr := os.Stat(scripts); scriptsErr == nil && scriptsInfo.IsDir() {
					return directory, nil
				}
			}
			parent := filepath.Dir(directory)
			if parent == directory {
				break
			}
		}
	}
	return "", fmt.Errorf("repository root containing ts2bin.lock.json and scripts/ was not found")
}

func resolvePowerShell(lookPath func(string) (string, error)) (string, error) {
	for _, name := range []string{"pwsh", "powershell"} {
		if path, err := lookPath(name); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("PowerShell executable not found (tried pwsh and powershell)")
}

func doctorCommand(goos, script string, lookPath func(string) (string, error)) (string, []string, error) {
	if goos == "windows" {
		powerShell, err := resolvePowerShell(lookPath)
		if err != nil {
			return "", nil, err
		}
		return powerShell, []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-File", script}, nil
	}
	bash, err := lookPath("bash")
	if err != nil {
		return "", nil, fmt.Errorf("Bash executable not found: %w", err)
	}
	return bash, []string{"--noprofile", "--norc", script}, nil
}

func executeProcess(ctx context.Context, directory, name string, args ...string) (processResult, error) {
	command := exec.CommandContext(ctx, name, args...)
	command.Dir = directory
	output, err := command.CombinedOutput()
	if err == nil {
		return processResult{Output: output, ExitCode: 0}, nil
	}
	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		return processResult{Output: output, ExitCode: exitError.ExitCode()}, nil
	}
	return processResult{Output: output, ExitCode: -1}, fmt.Errorf("execute %s: %w", name, err)
}

func normalizedOutputLines(output []byte) []string {
	text := strings.ReplaceAll(string(output), "\r\n", "\n")
	text = strings.TrimSuffix(text, "\n")
	if text == "" {
		return []string{}
	}
	return strings.Split(text, "\n")
}

func parseDoctorOutput(output []byte) ([]doctorCheck, []string) {
	checks := []doctorCheck{}
	messages := []string{}
	for _, line := range normalizedOutputLines(output) {
		trimmed := strings.TrimSpace(line)
		ok := false
		body := ""
		switch {
		case strings.HasPrefix(trimmed, "[ok]"):
			ok = true
			body = strings.TrimSpace(strings.TrimPrefix(trimmed, "[ok]"))
		case strings.HasPrefix(trimmed, "[FAIL]"):
			body = strings.TrimSpace(strings.TrimPrefix(trimmed, "[FAIL]"))
		default:
			messages = append(messages, line)
			continue
		}
		name, detail, found := strings.Cut(body, ":")
		if !found {
			name, detail = body, ""
		}
		checks = append(checks, doctorCheck{Name: strings.TrimSpace(name), OK: ok, Detail: strings.TrimSpace(detail)})
	}
	return checks, messages
}

func writeProcessOutput(writer io.Writer, output []byte) {
	if len(output) == 0 {
		return
	}
	_, _ = writer.Write(output)
	if output[len(output)-1] != '\n' {
		_, _ = io.WriteString(writer, "\n")
	}
}

func writeStableJSON(writer io.Writer, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	if _, err := writer.Write(data); err != nil {
		return err
	}
	_, err = io.WriteString(writer, "\n")
	return err
}

func resolveConfigPath(project string) (string, error) {
	project = strings.TrimSpace(project)
	if project == "" {
		return "", errors.New("project path is empty")
	}
	info, err := os.Stat(project)
	if err == nil && info.IsDir() {
		return filepath.Join(project, "tsconfig.json"), nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	return project, nil
}

func writeDiagnostic(writer io.Writer, diagnostic tsfrontend.Diagnostic) {
	location := diagnostic.PrimarySpan.File
	if location == "" {
		location = "<global>"
	}
	fmt.Fprintf(writer, "%s(%d,%d): %s %s",
		location,
		diagnostic.PrimarySpan.Start,
		diagnostic.PrimarySpan.End,
		diagnostic.Code,
		diagnostic.MessageKey,
	)
	if len(diagnostic.Arguments) != 0 {
		fmt.Fprintf(writer, " [%s]", strings.Join(diagnostic.Arguments, ", "))
	}
	fmt.Fprintln(writer)
}

func shortCommit(commit string) string {
	if len(commit) <= 12 {
		return commit
	}
	return commit[:12]
}

func writeUsage(writer io.Writer) {
	fmt.Fprintln(writer, "usage: ts2bin <command> [options]")
	fmt.Fprintln(writer, "commands: build, check, snapshot, dump-snapshot, emit-hir, emit-mir, emit-vert010, emit-vert011, emit-vert012, emit-vert013a, emit-vert013b, emit-classaccess, emit-checked-cast-replay, emit-function-thunk-replay, emit-property-access-replay, emit-property-access-unbound, diff, compatibility, upstream-audit, doctor, test, version")
}
