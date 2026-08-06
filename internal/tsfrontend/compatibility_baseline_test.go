package tsfrontend

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCheckedInCompatibilityBaselineMatchesCurrentCheckout(t *testing.T) {
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	moduleRoot := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "..", ".."))
	baselinePath := filepath.Join(moduleRoot, filepath.FromSlash(CompatibilityBaselineModulePath))
	data, err := os.ReadFile(baselinePath)
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := ParseCompatibilitySnapshot(data)
	if err != nil {
		t.Fatal(err)
	}
	canonicalBaseline, err := MarshalCompatibilitySnapshot(baseline)
	if err != nil {
		t.Fatal(err)
	}
	canonicalBaseline = append(canonicalBaseline, '\n')
	if !bytes.Equal(data, canonicalBaseline) {
		t.Fatal("checked-in compatibility baseline is not canonical JSON")
	}
	current, err := CollectCurrentCompatibilitySnapshot(context.Background(), moduleRoot)
	if err != nil {
		t.Fatal(err)
	}
	observedCommit, err := resolveCompatibilityCheckoutCommit(context.Background(), moduleRoot)
	if err != nil {
		t.Fatal(err)
	}
	if current.TypeScriptGoCommit != TypeScriptGoCommit {
		t.Fatalf("snapshot upstream commit = %q, want pinned %q", current.TypeScriptGoCommit, TypeScriptGoCommit)
	}
	if current.ObservedCheckoutCommit != observedCommit {
		t.Fatalf("observed checkout commit = %q, want git HEAD %q", current.ObservedCheckoutCommit, observedCommit)
	}
	report, err := CompareCompatibilitySnapshots(baseline, current)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Compatible {
		t.Fatalf("checked-in compatibility baseline drifted: %+v", report.Changes)
	}
	canonicalCurrent, err := MarshalCompatibilitySnapshot(current)
	if err != nil {
		t.Fatal(err)
	}
	canonicalCurrent = append(canonicalCurrent, '\n')
	if !bytes.Equal(canonicalBaseline, canonicalCurrent) {
		t.Fatal("current compatibility collection is not byte-identical to the checked-in baseline")
	}
	if len(current.Semantics) == 0 {
		t.Fatal("semantic fixture digests are empty")
	}
}

func TestCompatibilityCheckoutCommitComesFromGitHEAD(t *testing.T) {
	repository := t.TempDir()
	runCompatibilityGit(t, repository, "init", "--quiet")
	if err := os.WriteFile(filepath.Join(repository, "source.txt"), []byte("current checkout\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runCompatibilityGit(t, repository, "add", "source.txt")
	runCompatibilityGit(t, repository, "-c", "user.name=ts2bin test", "-c", "user.email=ts2bin@example.invalid", "commit", "--quiet", "-m", "fixture")
	want := strings.TrimSpace(runCompatibilityGit(t, repository, "rev-parse", "--verify", "HEAD"))
	got, err := resolveCompatibilityCheckoutCommit(context.Background(), repository)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("checkout commit = %q, want git HEAD %q", got, want)
	}
	if got == TypeScriptGoCommit {
		t.Fatal("temporary checkout unexpectedly reused the compile-time TypeScriptGoCommit")
	}
}

func runCompatibilityGit(t *testing.T, repository string, args ...string) string {
	t.Helper()
	commandArgs := append([]string{"-C", repository}, args...)
	output, err := exec.Command("git", commandArgs...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
	return string(output)
}
