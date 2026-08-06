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

	"github.com/microsoft/typescript-go/internal/core"
	"github.com/microsoft/typescript-go/internal/tsfrontend"
)

const (
	exitSuccess     = 0
	exitDiagnostics = 1
	exitUsage       = 2
)

const commandReportSchemaVersion = 1

type processResult struct {
	Output   []byte
	ExitCode int
}

type commandEnvironment struct {
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
	case "doctor":
		return runDoctor(ctx, args[1:], stdout, stderr, environment)
	case "compatibility", "upstream-audit":
		return runCompatibility(ctx, args[0], args[1:], stdout, stderr, environment)
	case "test":
		return runTests(ctx, args[1:], stdout, stderr, environment)
	case "help", "-h", "--help":
		writeUsage(stdout)
		return exitSuccess
	default:
		fmt.Fprintf(stderr, "ts2bin: unknown command %q\n", args[0])
		writeUsage(stderr)
		return exitUsage
	}
}

func defaultCommandEnvironment() commandEnvironment {
	return commandEnvironment{
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
	script := filepath.Join(repositoryRoot, "scripts", "doctor.ps1")
	if info, statErr := os.Stat(script); statErr != nil || info.IsDir() {
		if statErr == nil {
			statErr = fmt.Errorf("%s is not a file", script)
		}
		return writeDoctorFailure(stdout, stderr, *jsonOutput, fmt.Errorf("doctor script unavailable: %w", statErr))
	}
	powerShell, err := resolvePowerShell(environment.lookPath)
	if err != nil {
		return writeDoctorFailure(stdout, stderr, *jsonOutput, err)
	}
	result, err := environment.execute(ctx, repositoryRoot, powerShell, "-NoLogo", "-NoProfile", "-NonInteractive", "-File", script)
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
	stage := flags.String("stage", "", "test stage (currently frontend)")
	jsonOutput := flags.Bool("json", false, "print the test report as JSON")
	runnerFlag := flags.String("runner", "", "explicit frontend manifest runner path")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return exitUsage
	}
	if *stage != "frontend" {
		err := fmt.Errorf("unsupported or missing stage %q; expected frontend", *stage)
		return writeFrontendTestFailure(stdout, stderr, *jsonOutput, "", err, exitUsage)
	}
	repositoryRoot, err := resolveRepositoryRoot(environment)
	if err != nil {
		return writeFrontendTestFailure(stdout, stderr, *jsonOutput, "", err, exitUsage)
	}
	runner, err := resolveFrontendRunner(repositoryRoot, *runnerFlag)
	if err != nil {
		return writeFrontendTestFailure(stdout, stderr, *jsonOutput, "", err, exitUsage)
	}
	name, runnerArgs, err := frontendRunnerCommand(runner, environment.lookPath)
	if err != nil {
		return writeFrontendTestFailure(stdout, stderr, *jsonOutput, runner, err, exitUsage)
	}
	runnerDirectory := repositoryRoot
	if strings.EqualFold(filepath.Ext(runner), ".go") {
		runnerDirectory = filepath.Join(repositoryRoot, "typescript-go")
	}
	result, err := environment.execute(ctx, runnerDirectory, name, runnerArgs...)
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

func resolveFrontendRunner(repositoryRoot, explicit string) (string, error) {
	if strings.TrimSpace(explicit) != "" {
		path, err := filepath.Abs(explicit)
		if err != nil {
			return "", err
		}
		if info, err := os.Stat(path); err != nil || info.IsDir() {
			if err == nil {
				err = fmt.Errorf("path is a directory")
			}
			return "", fmt.Errorf("frontend runner %q is unavailable: %w", path, err)
		}
		return path, nil
	}
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
	case ".go":
		goCommand, err := lookPath("go")
		if err != nil {
			return "", nil, fmt.Errorf("Go executable not found: %w", err)
		}
		return goCommand, []string{"test", "./internal/tsfrontend", "-run", "^TestFrontendConformanceFixtures$", "-count=1"}, nil
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
			script := filepath.Join(directory, "scripts", "doctor.ps1")
			if lockInfo, lockErr := os.Stat(lock); lockErr == nil && !lockInfo.IsDir() {
				if scriptInfo, scriptErr := os.Stat(script); scriptErr == nil && !scriptInfo.IsDir() {
					return directory, nil
				}
			}
			parent := filepath.Dir(directory)
			if parent == directory {
				break
			}
		}
	}
	return "", fmt.Errorf("repository root containing ts2bin.lock.json and scripts/doctor.ps1 was not found")
}

func resolvePowerShell(lookPath func(string) (string, error)) (string, error) {
	for _, name := range []string{"pwsh", "powershell"} {
		if path, err := lookPath(name); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("PowerShell executable not found (tried pwsh and powershell)")
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
	fmt.Fprintln(writer, "commands: check, snapshot, dump-snapshot, compatibility, upstream-audit, doctor, test, version")
}
