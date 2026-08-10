package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVersionJSONContainsLockedProvenance(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), []string{"version", "--json"}, &stdout, &stderr); code != exitSuccess {
		t.Fatalf("exit = %d, stderr = %s", code, &stderr)
	}
	for _, value := range []string{
		"86cc4767d4ebadb9b7845d0ab8eb2b05785c3fee",
		"b82676463eea69ca2b7e4a6db078098999eae73b0e426cca8b8d1a7ebfc08967",
		`"llvmMajor": 20`,
		`"llvmVersion": "20.1.8"`,
		`"lldVersion": "20.1.8"`,
	} {
		if !strings.Contains(stdout.String(), value) {
			t.Fatalf("version output missing %q:\n%s", value, &stdout)
		}
	}
}

func TestCheckExitCodesAndStableJSON(t *testing.T) {
	t.Parallel()
	valid := writeProject(t, `export const value: number = 1;`)
	invalid := writeProject(t, `export const value: string = 1;`)

	for _, test := range []struct {
		name    string
		project string
		want    int
	}{
		{name: "valid", project: valid, want: exitSuccess},
		{name: "invalid", project: invalid, want: exitDiagnostics},
	} {
		t.Run(test.name, func(t *testing.T) {
			var first, second, stderr bytes.Buffer
			args := []string{"check", "--json", test.project}
			if code := run(context.Background(), args, &first, &stderr); code != test.want {
				t.Fatalf("exit = %d, want %d, stderr = %s, stdout = %s", code, test.want, &stderr, &first)
			}
			stderr.Reset()
			if code := run(context.Background(), args, &second, &stderr); code != test.want {
				t.Fatalf("second exit = %d, want %d", code, test.want)
			}
			if first.String() != second.String() {
				t.Fatalf("diagnostics changed between runs:\n%s\n%s", &first, &second)
			}
		})
	}
}

func TestSnapshotVerifiesDeterminismAndWritesJSON(t *testing.T) {
	project := writeProject(t, `export const value: number = 1;`)
	output := filepath.Join(t.TempDir(), "snapshot.json")
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"snapshot", "--verify-determinism", "--output", output, project}, &stdout, &stderr)
	if code != exitSuccess {
		t.Fatalf("exit = %d, stderr = %s, stdout = %s", code, &stderr, &stdout)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout should be empty with --output, got %s", &stdout)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot struct {
		SchemaVersion uint32 `json:"schemaVersion"`
		ContentHash   string `json:"contentHash"`
	}
	if err := json.Unmarshal(data, &snapshot); err != nil {
		t.Fatalf("invalid snapshot JSON: %v\n%s", err, data)
	}
	if snapshot.SchemaVersion == 0 || len(snapshot.ContentHash) != 64 {
		t.Fatalf("incomplete snapshot identity: %+v", snapshot)
	}
}

func TestSnapshotEmitsOnlySourceProfileFromBingoConfig(t *testing.T) {
	project := t.TempDir()
	config := `{
  "compilerOptions": {"strict": true},
  "bingoOptions": {
    "profile": "unsafe",
    "runtime": "audit-runtime-v1",
    "llvmMajor": 20,
    "targetTriple": "x86_64-unknown-linux-gnu",
    "cpu": "baseline",
    "features": ["sse2"],
    "gc": "arc",
    "exceptions": "none",
    "overflow": "js-number",
    "boundsCheck": "off",
    "emit": ["hir"]
  },
  "files": ["main.ts"]
}`
	if err := os.WriteFile(filepath.Join(project, "tsconfig.json"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "main.ts"), []byte(`export const value = 1;`), 0o600); err != nil {
		t.Fatal(err)
	}

	readOptions := func(t *testing.T, args ...string) map[string]json.RawMessage {
		t.Helper()
		output := filepath.Join(t.TempDir(), "snapshot.json")
		command := append([]string{"snapshot", "--output", output}, args...)
		command = append(command, project)
		var stdout, stderr bytes.Buffer
		if code := run(context.Background(), command, &stdout, &stderr); code != exitSuccess {
			t.Fatalf("exit = %d, stderr = %s", code, &stderr)
		}
		var snapshot struct {
			Program struct {
				Config struct {
					Bingo map[string]json.RawMessage `json:"bingo"`
				} `json:"config"`
			} `json:"program"`
		}
		data, err := os.ReadFile(output)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(data, &snapshot); err != nil {
			t.Fatal(err)
		}
		return snapshot.Program.Config.Bingo
	}

	fromConfig := readOptions(t)
	if got := string(fromConfig["profile"]); got != `"unsafe"` {
		t.Fatalf("source profile = %s, want unsafe", got)
	}
	if len(fromConfig) != 1 {
		t.Fatalf("snapshot leaked backend Bingo options: %v", fromConfig)
	}

	withProfile := readOptions(t, "--profile", "interop")
	if got := string(withProfile["profile"]); got != `"interop"` {
		t.Fatalf("explicit profile was not applied: %s", got)
	}
	if len(withProfile) != 1 {
		t.Fatalf("profile override leaked backend Bingo options: %v", withProfile)
	}
}

func TestDumpSnapshotAliasWritesToStdout(t *testing.T) {
	project := writeProject(t, `export const value = 1;`)
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), []string{"dump-snapshot", project}, &stdout, &stderr); code != exitSuccess {
		t.Fatalf("exit = %d, stderr = %s", code, &stderr)
	}
	if !strings.Contains(stdout.String(), `"schemaVersion"`) || !strings.HasSuffix(stdout.String(), "\n") {
		t.Fatalf("unexpected snapshot output:\n%s", &stdout)
	}
}

func TestSnapshotTypeErrorReturnsCanonicalDiagnostics(t *testing.T) {
	project := writeProject(t, `export const value: string = 1;`)
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), []string{"snapshot", project}, &stdout, &stderr); code != exitDiagnostics {
		t.Fatalf("exit = %d, stderr = %s", code, &stderr)
	}
	if stdout.Len() != 0 {
		t.Fatalf("snapshot should not be emitted for TypeScript errors: %s", &stdout)
	}
	var diagnostics []map[string]any
	if err := json.Unmarshal(stderr.Bytes(), &diagnostics); err != nil {
		t.Fatalf("diagnostics are not JSON: %v\n%s", err, &stderr)
	}
	if len(diagnostics) == 0 {
		t.Fatal("expected at least one diagnostic")
	}
}

func TestDoctorJSONWrapsAuthoritativeScript(t *testing.T) {
	repositoryRoot := writeFakeRepositoryRoot(t)
	var gotDirectory, gotName string
	var gotArgs []string
	environment := commandEnvironment{
		goos:  "windows",
		getwd: func() (string, error) { return repositoryRoot, nil },
		lookPath: func(name string) (string, error) {
			return filepath.Join(repositoryRoot, name+".exe"), nil
		},
		execute: func(_ context.Context, directory, name string, args ...string) (processResult, error) {
			gotDirectory, gotName, gotArgs = directory, name, append([]string(nil), args...)
			return processResult{Output: []byte("[ok] Go\r\n[ok] LLD\r\n"), ExitCode: 0}, nil
		},
	}
	var stdout, stderr bytes.Buffer
	if code := runWithEnvironment(context.Background(), []string{"doctor", "--json"}, &stdout, &stderr, environment); code != exitSuccess {
		t.Fatalf("exit = %d, stderr = %s", code, &stderr)
	}
	var report doctorReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if !report.OK || report.ExitCode != 0 || len(report.Checks) != 2 {
		t.Fatalf("unexpected report: %+v", report)
	}
	if report.Checks[0].Name != "Go" || !report.Checks[0].OK || report.Checks[1].Name != "LLD" {
		t.Fatalf("checks were not parsed: %+v", report.Checks)
	}
	if gotDirectory != repositoryRoot || !strings.HasSuffix(gotName, "pwsh.exe") {
		t.Fatalf("execute directory/name = %q, %q", gotDirectory, gotName)
	}
	doctorScript := filepath.Join(repositoryRoot, "scripts", "doctor.ps1")
	if len(gotArgs) == 0 || gotArgs[len(gotArgs)-1] != doctorScript {
		t.Fatalf("doctor script not passed to PowerShell: %q", gotArgs)
	}
}

func TestDoctorUsesBashRunnerOutsideWindows(t *testing.T) {
	repositoryRoot := writeFakeRepositoryRoot(t)
	doctorScript := filepath.Join(repositoryRoot, "scripts", "doctor.sh")
	if err := os.WriteFile(doctorScript, []byte("#!/usr/bin/env bash\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	var gotDirectory, gotName string
	var gotArgs []string
	environment := commandEnvironment{
		goos:  "linux",
		getwd: func() (string, error) { return repositoryRoot, nil },
		lookPath: func(name string) (string, error) {
			return filepath.Join(repositoryRoot, name), nil
		},
		execute: func(_ context.Context, directory, name string, args ...string) (processResult, error) {
			gotDirectory, gotName, gotArgs = directory, name, append([]string(nil), args...)
			return processResult{Output: []byte("[ok]   Go: go version go1.26.0 linux/amd64\n"), ExitCode: 0}, nil
		},
	}
	var stdout, stderr bytes.Buffer
	if code := runWithEnvironment(context.Background(), []string{"doctor", "--json"}, &stdout, &stderr, environment); code != exitSuccess {
		t.Fatalf("exit = %d, stderr = %s", code, &stderr)
	}
	if gotDirectory != repositoryRoot || !strings.HasSuffix(gotName, "bash") {
		t.Fatalf("execute directory/name = %q, %q", gotDirectory, gotName)
	}
	wantArgs := []string{"--noprofile", "--norc", doctorScript}
	if strings.Join(gotArgs, "\x00") != strings.Join(wantArgs, "\x00") {
		t.Fatalf("bash args = %q, want %q", gotArgs, wantArgs)
	}
}

func TestFrontendStageReportsMissingRunner(t *testing.T) {
	repositoryRoot := writeFakeRepositoryRoot(t)
	legacyRunner := filepath.Join(repositoryRoot, "typescript-go", "internal", "tsfrontend", "frontend_fixture_test.go")
	if err := os.MkdirAll(filepath.Dir(legacyRunner), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyRunner, []byte("package tsfrontend\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	environment := commandEnvironment{
		getwd:    func() (string, error) { return repositoryRoot, nil },
		lookPath: func(name string) (string, error) { return name, nil },
		execute: func(context.Context, string, string, ...string) (processResult, error) {
			t.Fatal("missing runner must not execute a process")
			return processResult{}, nil
		},
	}
	var stdout, stderr bytes.Buffer
	if code := runWithEnvironment(context.Background(), []string{"test", "--stage", "frontend", "--json"}, &stdout, &stderr, environment); code != exitUsage {
		t.Fatalf("exit = %d, stderr = %s", code, &stderr)
	}
	var report frontendTestReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.OK || !strings.Contains(report.Error, "not implemented") {
		t.Fatalf("unexpected report: %+v", report)
	}
}

func TestFrontendStageRejectsRunnerOverride(t *testing.T) {
	repositoryRoot := writeFakeRepositoryRoot(t)
	runner := filepath.Join(repositoryRoot, "bypass.go")
	if err := os.WriteFile(runner, []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	environment := commandEnvironment{
		getwd:    func() (string, error) { return repositoryRoot, nil },
		lookPath: func(name string) (string, error) { return name, nil },
		execute: func(context.Context, string, string, ...string) (processResult, error) {
			t.Fatal("runner override must not execute a process")
			return processResult{}, nil
		},
	}
	var stdout, stderr bytes.Buffer
	args := []string{"test", "--stage", "frontend", "--runner", runner, "--json"}
	if code := runWithEnvironment(context.Background(), args, &stdout, &stderr, environment); code != exitUsage {
		t.Fatalf("exit = %d, stderr = %s", code, &stderr)
	}
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), "flag provided but not defined: -runner") {
		t.Fatalf("unexpected output: stdout=%s stderr=%s", &stdout, &stderr)
	}
}

func TestFrontendStagePrefersRepositoryPowerShellRunner(t *testing.T) {
	repositoryRoot := writeFakeRepositoryRoot(t)
	runner := filepath.Join(repositoryRoot, "scripts", "test-frontend.ps1")
	if err := os.WriteFile(runner, []byte("exit 0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var gotDirectory, gotName string
	var gotArgs []string
	environment := commandEnvironment{
		getwd:    func() (string, error) { return repositoryRoot, nil },
		lookPath: func(name string) (string, error) { return filepath.Join(repositoryRoot, name+".exe"), nil },
		execute: func(_ context.Context, directory, name string, args ...string) (processResult, error) {
			gotDirectory, gotName, gotArgs = directory, name, append([]string(nil), args...)
			return processResult{Output: []byte(
				"[frontend:package] command: go test ./internal/tsfrontend/... -count=1\n" +
					"[frontend:package] result: passed; exit=0; elapsedMs=10\n" +
					"[frontend:summary] result: passed=1; failed=0; failedStages=none\n",
			), ExitCode: 0}, nil
		},
	}
	var stdout, stderr bytes.Buffer
	if code := runWithEnvironment(context.Background(), []string{"test", "--stage", "frontend", "--json"}, &stdout, &stderr, environment); code != exitSuccess {
		t.Fatalf("exit = %d, stderr = %s", code, &stderr)
	}
	if gotDirectory != repositoryRoot || !strings.HasSuffix(gotName, "pwsh.exe") {
		t.Fatalf("execute directory/name = %q, %q", gotDirectory, gotName)
	}
	if len(gotArgs) == 0 || gotArgs[len(gotArgs)-1] != runner {
		t.Fatalf("PowerShell args = %q", gotArgs)
	}
	var report frontendTestReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if !report.OK || report.Runner != "scripts/test-frontend.ps1" {
		t.Fatalf("unexpected report: %+v", report)
	}
	if len(report.Output) != 3 || !strings.Contains(report.Output[0], "command:") || !strings.Contains(report.Output[1], "result: passed") || !strings.Contains(report.Output[2], "[frontend:summary]") {
		t.Fatalf("runner output was not preserved: %#v", report.Output)
	}
}

func TestFrontendStageReportsRunnerFailureAndStageOutput(t *testing.T) {
	repositoryRoot := writeFakeRepositoryRoot(t)
	runner := filepath.Join(repositoryRoot, "scripts", "test-frontend.ps1")
	if err := os.WriteFile(runner, []byte("exit 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	environment := commandEnvironment{
		getwd:    func() (string, error) { return repositoryRoot, nil },
		lookPath: func(name string) (string, error) { return filepath.Join(repositoryRoot, name+".exe"), nil },
		execute: func(context.Context, string, string, ...string) (processResult, error) {
			return processResult{
				Output:   []byte("[frontend:package] command: go test ./internal/tsfrontend/... -count=1\n[frontend:package] result: failed; exit=1; elapsedMs=10\n"),
				ExitCode: 1,
			}, nil
		},
	}
	var stdout, stderr bytes.Buffer
	if code := runWithEnvironment(context.Background(), []string{"test", "--stage", "frontend", "--json"}, &stdout, &stderr, environment); code != exitDiagnostics {
		t.Fatalf("exit = %d, want %d, stderr = %s", code, exitDiagnostics, &stderr)
	}
	var report frontendTestReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.OK || len(report.Output) != 2 || !strings.Contains(report.Output[0], "command:") || !strings.Contains(report.Output[1], "result: failed") {
		t.Fatalf("unexpected failure report: %+v", report)
	}
}

func writeProject(t *testing.T, source string) string {
	t.Helper()
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "tsconfig.json"), []byte(`{"compilerOptions":{"strict":true},"files":["main.ts"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "main.ts"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	return directory
}

func writeFakeRepositoryRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	lock := `{"schemaVersion":2,"lockFormat":"ts2bin.lock.v2","typescriptGo":{"commit":"12318e599d21f516defea3b20e5d44b9369da723"}}`
	if err := os.WriteFile(filepath.Join(root, "ts2bin.lock.json"), []byte(lock), 0o600); err != nil {
		t.Fatal(err)
	}
	scripts := filepath.Join(root, "scripts")
	if err := os.Mkdir(scripts, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(scripts, "doctor.ps1"), []byte("exit 0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}
