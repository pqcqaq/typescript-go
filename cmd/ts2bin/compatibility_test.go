package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/internal/tsfrontend"
)

func TestCompatibilityCommandReportsCompatibleCheckoutAndDrift(t *testing.T) {
	repositoryRoot := writeFakeRepositoryRoot(t)
	baselinePath := filepath.Join(repositoryRoot, "compatibility.json")
	baseline := commandCompatibilitySnapshot("semantic-before")
	writeCommandCompatibilityBaseline(t, baselinePath, baseline)

	current := baseline
	environment := commandEnvironment{
		getwd: func() (string, error) { return repositoryRoot, nil },
		collectCompatibility: func(_ context.Context, moduleRoot string) (tsfrontend.CompatibilitySnapshot, error) {
			if moduleRoot != filepath.Join(repositoryRoot, "typescript-go") {
				t.Fatalf("module root = %q", moduleRoot)
			}
			return current, nil
		},
	}

	var stdout, stderr bytes.Buffer
	args := []string{"compatibility", "--json", "--baseline", baselinePath}
	if code := runWithEnvironment(context.Background(), args, &stdout, &stderr, environment); code != exitSuccess {
		t.Fatalf("compatible exit = %d, stderr = %s", code, &stderr)
	}
	var report tsfrontend.CompatibilityReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if !report.Compatible || len(report.Changes) != 0 {
		t.Fatalf("compatible report = %+v", report)
	}

	observedCommit := strings.Repeat("a", 40)
	current.ObservedCheckoutCommit = observedCommit
	stdout.Reset()
	stderr.Reset()
	if code := runWithEnvironment(context.Background(), args, &stdout, &stderr, environment); code != exitDiagnostics {
		t.Fatalf("checkout drift exit = %d, stderr = %s", code, &stderr)
	}
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Compatible || report.ExpectedCheckoutCommit != baseline.TypeScriptGoCommit || report.ObservedCheckoutCommit != observedCommit || len(report.Changes) != 1 {
		t.Fatalf("checkout drift report = %+v", report)
	}
	checkoutChange := report.Changes[0]
	if checkoutChange.Category != tsfrontend.CompatibilityCategoryLock || checkoutChange.Kind != tsfrontend.CompatibilityChangeChanged ||
		checkoutChange.Key != "typescript-go.checkout" || checkoutChange.Before != baseline.TypeScriptGoCommit || checkoutChange.After != observedCommit {
		t.Fatalf("checkout drift change = %+v", checkoutChange)
	}

	current.ObservedCheckoutCommit = ""
	current.Semantics = []tsfrontend.CompatibilityEntry{{Key: "fixture/basic", Value: "semantic-after"}}
	stdout.Reset()
	stderr.Reset()
	args[0] = "upstream-audit"
	if code := runWithEnvironment(context.Background(), args, &stdout, &stderr, environment); code != exitDiagnostics {
		t.Fatalf("drift exit = %d, stderr = %s", code, &stderr)
	}
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Compatible || len(report.Changes) != 1 {
		t.Fatalf("drift report = %+v", report)
	}
	change := report.Changes[0]
	if change.Category != tsfrontend.CompatibilityCategorySemantic || change.Kind != tsfrontend.CompatibilityChangeChanged || change.Key != "fixture/basic" {
		t.Fatalf("drift change = %+v", change)
	}
}

func TestReadCompatibilityCheckoutCommitValidatesRepositoryLock(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "ts2bin.lock.json")
	const commit = "12318e599d21f516defea3b20e5d44b9369da723"
	valid := `{"schemaVersion":2,"lockFormat":"ts2bin.lock.v2","typescriptGo":{"commit":"` + commit + `"}}`
	if err := os.WriteFile(path, []byte(valid), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := readCompatibilityCheckoutCommit(root)
	if err != nil || got != commit {
		t.Fatalf("locked checkout commit = %q, %v", got, err)
	}
	for _, invalid := range []string{
		`{"schemaVersion":1,"lockFormat":"ts2bin.lock.v2","typescriptGo":{"commit":"` + commit + `"}}`,
		`{"schemaVersion":2,"lockFormat":"wrong","typescriptGo":{"commit":"` + commit + `"}}`,
		`{"schemaVersion":2,"lockFormat":"ts2bin.lock.v2","typescriptGo":{"commit":"MAIN"}}`,
		valid + `{}`,
	} {
		if err := os.WriteFile(path, []byte(invalid), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := readCompatibilityCheckoutCommit(root); err == nil {
			t.Errorf("invalid lock was accepted: %s", invalid)
		}
	}
}

func TestCompatibilityCommandRefusesBaselineUpdateForUnlockedCheckout(t *testing.T) {
	repositoryRoot := writeFakeRepositoryRoot(t)
	baselinePath := filepath.Join(repositoryRoot, "compatibility.json")
	baseline := commandCompatibilitySnapshot("old")
	writeCommandCompatibilityBaseline(t, baselinePath, baseline)
	before, err := os.ReadFile(baselinePath)
	if err != nil {
		t.Fatal(err)
	}
	current := commandCompatibilitySnapshot("new")
	current.ObservedCheckoutCommit = strings.Repeat("a", 40)
	environment := commandEnvironment{
		getwd: func() (string, error) { return repositoryRoot, nil },
		collectCompatibility: func(context.Context, string) (tsfrontend.CompatibilitySnapshot, error) {
			return current, nil
		},
	}

	var stdout, stderr bytes.Buffer
	args := []string{"compatibility", "--json", "--update-baseline", "--baseline", baselinePath}
	if code := runWithEnvironment(context.Background(), args, &stdout, &stderr, environment); code != exitDiagnostics {
		t.Fatalf("unlocked update exit = %d, stderr = %s", code, &stderr)
	}
	after, err := os.ReadFile(baselinePath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("baseline changed despite checkout/lock mismatch")
	}
	var report tsfrontend.CompatibilityReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Compatible || len(report.Changes) != 1 || report.Changes[0].Key != "typescript-go.checkout" {
		t.Fatalf("unlocked update report = %+v", report)
	}
}

func TestCompatibilityCommandExplicitBaselineUpdateAndOverwriteGuard(t *testing.T) {
	repositoryRoot := writeFakeRepositoryRoot(t)
	baselinePath := filepath.Join(repositoryRoot, "compatibility.json")
	writeCommandCompatibilityBaseline(t, baselinePath, commandCompatibilitySnapshot("old"))
	current := commandCompatibilitySnapshot("new")
	environment := commandEnvironment{
		getwd: func() (string, error) { return repositoryRoot, nil },
		collectCompatibility: func(context.Context, string) (tsfrontend.CompatibilitySnapshot, error) {
			return current, nil
		},
	}

	var stdout, stderr bytes.Buffer
	args := []string{"compatibility", "--json", "--update-baseline", "--baseline", baselinePath}
	if code := runWithEnvironment(context.Background(), args, &stdout, &stderr, environment); code != exitSuccess {
		t.Fatalf("update exit = %d, stderr = %s", code, &stderr)
	}
	data, err := os.ReadFile(baselinePath)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := tsfrontend.ParseCompatibilitySnapshot(data)
	if err != nil {
		t.Fatal(err)
	}
	report, err := tsfrontend.CompareCompatibilitySnapshots(updated, current)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Compatible {
		t.Fatalf("updated baseline drifted: %+v", report.Changes)
	}

	invalidPath := filepath.Join(repositoryRoot, "not-a-baseline.json")
	const invalid = "do not overwrite me\n"
	if err := os.WriteFile(invalidPath, []byte(invalid), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	args[len(args)-1] = invalidPath
	if code := runWithEnvironment(context.Background(), args, &stdout, &stderr, environment); code != exitUsage {
		t.Fatalf("guard exit = %d, stderr = %s", code, &stderr)
	}
	got, err := os.ReadFile(invalidPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != invalid || !strings.Contains(stderr.String(), "refusing to overwrite non-baseline file") {
		t.Fatalf("overwrite guard: file = %q, stderr = %s", got, &stderr)
	}
}

func TestCompatibilityCommandRejectsBaselineOutsideRepository(t *testing.T) {
	repositoryRoot := writeFakeRepositoryRoot(t)
	environment := commandEnvironment{
		getwd: func() (string, error) { return repositoryRoot, nil },
		collectCompatibility: func(context.Context, string) (tsfrontend.CompatibilitySnapshot, error) {
			t.Fatal("an escaping baseline path must fail before collection")
			return tsfrontend.CompatibilitySnapshot{}, nil
		},
	}
	var stdout, stderr bytes.Buffer
	args := []string{"compatibility", "--baseline", filepath.Join("..", "outside.json")}
	if code := runWithEnvironment(context.Background(), args, &stdout, &stderr, environment); code != exitUsage {
		t.Fatalf("exit = %d, stderr = %s", code, &stderr)
	}
	if !strings.Contains(stderr.String(), "escapes repository root") {
		t.Fatalf("stderr = %s", &stderr)
	}

	stdout.Reset()
	stderr.Reset()
	sibling := repositoryRoot + "-sibling"
	args[1] = "--baseline"
	args[2] = sibling + string(filepath.Separator) + "baseline.json"
	if code := runWithEnvironment(context.Background(), args, &stdout, &stderr, environment); code != exitUsage {
		t.Fatalf("sibling exit = %d, stderr = %s", code, &stderr)
	}
	if !strings.Contains(stderr.String(), "escapes repository root") {
		t.Fatalf("sibling stderr = %s", &stderr)
	}
}

func TestCompatibilityCommandRejectsResolvedLinkOutsideRepository(t *testing.T) {
	repositoryRoot := writeFakeRepositoryRoot(t)
	outside := t.TempDir()
	linkedDirectory := filepath.Join(repositoryRoot, "linked-baselines")
	if err := os.Symlink(outside, linkedDirectory); err != nil {
		t.Skipf("cannot create directory symlink: %v", err)
	}
	environment := commandEnvironment{
		getwd: func() (string, error) { return repositoryRoot, nil },
		collectCompatibility: func(context.Context, string) (tsfrontend.CompatibilitySnapshot, error) {
			t.Fatal("an escaping resolved baseline path must fail before collection")
			return tsfrontend.CompatibilitySnapshot{}, nil
		},
	}
	var stdout, stderr bytes.Buffer
	args := []string{"compatibility", "--baseline", filepath.Join(linkedDirectory, "baseline.json")}
	if code := runWithEnvironment(context.Background(), args, &stdout, &stderr, environment); code != exitUsage {
		t.Fatalf("exit = %d, stderr = %s", code, &stderr)
	}
	if !strings.Contains(stderr.String(), "resolved path escapes repository root") {
		t.Fatalf("stderr = %s", &stderr)
	}
}

func TestWriteCompatibilityBaselineConfinesResolvedPathToRepository(t *testing.T) {
	repositoryRoot := writeFakeRepositoryRoot(t)
	outside := t.TempDir()
	linkedDirectory := filepath.Join(repositoryRoot, "linked-write")
	if err := os.Symlink(outside, linkedDirectory); err != nil {
		t.Skipf("cannot create directory symlink: %v", err)
	}
	baselinePath := filepath.Join(linkedDirectory, "baseline.json")
	if err := writeCompatibilityBaseline(repositoryRoot, baselinePath, commandCompatibilitySnapshot("current")); err == nil {
		t.Fatal("root-confined baseline writer accepted a resolved path outside the repository")
	}
	if _, err := os.Stat(filepath.Join(outside, "baseline.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("outside baseline was written or stat failed unexpectedly: %v", err)
	}
}

func TestCompatibilityCommandRevalidatesBaselinePathBeforeUpdate(t *testing.T) {
	repositoryRoot := writeFakeRepositoryRoot(t)
	outside := t.TempDir()
	linkedDirectory := filepath.Join(repositoryRoot, "late-link")
	if err := os.Mkdir(linkedDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	probe := filepath.Join(repositoryRoot, "symlink-probe")
	if err := os.Symlink(outside, probe); err != nil {
		t.Skipf("cannot create directory symlink: %v", err)
	}
	if err := os.Remove(probe); err != nil {
		t.Fatal(err)
	}
	environment := commandEnvironment{
		getwd: func() (string, error) { return repositoryRoot, nil },
		collectCompatibility: func(context.Context, string) (tsfrontend.CompatibilitySnapshot, error) {
			if err := os.Remove(linkedDirectory); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, linkedDirectory); err != nil {
				t.Fatal(err)
			}
			return commandCompatibilitySnapshot("current"), nil
		},
	}
	baselinePath := filepath.Join(linkedDirectory, "baseline.json")
	var stdout, stderr bytes.Buffer
	args := []string{"compatibility", "--update-baseline", "--baseline", baselinePath}
	if code := runWithEnvironment(context.Background(), args, &stdout, &stderr, environment); code != exitUsage {
		t.Fatalf("exit = %d, stderr = %s", code, &stderr)
	}
	if !strings.Contains(stderr.String(), "revalidate baseline path") || !strings.Contains(stderr.String(), "resolved path escapes repository root") {
		t.Fatalf("stderr = %s", &stderr)
	}
	if _, err := os.Stat(filepath.Join(outside, "baseline.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("outside baseline was written or stat failed unexpectedly: %v", err)
	}
}

func commandCompatibilitySnapshot(semantic string) tsfrontend.CompatibilitySnapshot {
	return tsfrontend.CompatibilitySnapshot{
		SchemaVersion:      tsfrontend.CompatibilitySchemaVersion,
		TypeScriptGoCommit: "12318e599d21f516defea3b20e5d44b9369da723",
		Kinds:              []tsfrontend.CompatibilityEntry{{Key: "KindA", Value: "1"}},
		API:                []tsfrontend.CompatibilityEntry{{Key: "internal/ast.API", Value: "func()"}},
		Stdlib:             []tsfrontend.CompatibilityEntry{{Key: "lib.es5.d.ts", Value: "stdlib"}},
		Semantics:          []tsfrontend.CompatibilityEntry{{Key: "fixture/basic", Value: semantic}},
	}
}

func writeCommandCompatibilityBaseline(t *testing.T, path string, snapshot tsfrontend.CompatibilitySnapshot) {
	t.Helper()
	data, err := tsfrontend.MarshalCompatibilitySnapshot(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}
