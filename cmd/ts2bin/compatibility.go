package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/microsoft/typescript-go/internal/tsfrontend"
)

func runCompatibility(ctx context.Context, command string, args []string, stdout, stderr io.Writer, environment commandEnvironment) int {
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(stderr)
	jsonOutput := flags.Bool("json", false, "print the compatibility report as JSON")
	baselinePath := flags.String("baseline", "", "compatibility baseline path relative to the repository root")
	updateBaseline := flags.Bool("update-baseline", false, "replace the baseline with the audited current checkout")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return exitUsage
	}

	repositoryRoot, err := resolveRepositoryRoot(environment)
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin %s: %v\n", command, err)
		return exitUsage
	}
	expectedCheckoutCommit, err := readCompatibilityCheckoutCommit(repositoryRoot)
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin %s: read repository lock: %v\n", command, err)
		return exitUsage
	}
	moduleRoot := filepath.Join(repositoryRoot, "typescript-go")
	resolvedBaseline, err := resolveCompatibilityBaselinePath(repositoryRoot, moduleRoot, *baselinePath)
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin %s: %v\n", command, err)
		return exitUsage
	}
	var baseline tsfrontend.CompatibilitySnapshot
	if !*updateBaseline {
		data, err := os.ReadFile(resolvedBaseline)
		if err != nil {
			fmt.Fprintf(stderr, "ts2bin %s: read baseline %q: %v\n", command, resolvedBaseline, err)
			return exitUsage
		}
		baseline, err = tsfrontend.ParseCompatibilitySnapshot(data)
		if err != nil {
			fmt.Fprintf(stderr, "ts2bin %s: parse baseline %q: %v\n", command, resolvedBaseline, err)
			return exitUsage
		}
	}

	collector := environment.collectCompatibility
	if collector == nil {
		collector = tsfrontend.CollectCurrentCompatibilitySnapshot
	}
	current, err := collector(ctx, moduleRoot)
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin %s: collect current checkout: %v\n", command, err)
		return exitUsage
	}
	current.ExpectedCheckoutCommit = expectedCheckoutCommit

	baselineUpdated := false
	if *updateBaseline {
		checkoutReport, err := tsfrontend.CompareCompatibilitySnapshots(current, current)
		if err != nil {
			fmt.Fprintf(stderr, "ts2bin %s: validate current checkout: %v\n", command, err)
			return exitUsage
		}
		if checkoutReport.Compatible {
			if err := ensureCompatibilityPathInsideRepository(repositoryRoot, resolvedBaseline); err != nil {
				fmt.Fprintf(stderr, "ts2bin %s: revalidate baseline path: %v\n", command, err)
				return exitUsage
			}
			if err := writeCompatibilityBaseline(repositoryRoot, resolvedBaseline, current); err != nil {
				fmt.Fprintf(stderr, "ts2bin %s: update baseline: %v\n", command, err)
				return exitUsage
			}
			baselineUpdated = true
		}
		baseline = current
	}

	report, err := tsfrontend.CompareCompatibilitySnapshots(baseline, current)
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin %s: compare compatibility: %v\n", command, err)
		return exitUsage
	}
	if *jsonOutput {
		data, err := tsfrontend.MarshalCompatibilityReport(report)
		if err != nil {
			fmt.Fprintf(stderr, "ts2bin %s: encode compatibility report: %v\n", command, err)
			return exitUsage
		}
		fmt.Fprintln(stdout, string(data))
	} else if !report.Compatible {
		fmt.Fprintf(stdout, "incompatible: %d compatibility change(s) from %s to %s\n", len(report.Changes), shortCommit(report.BaselineCommit), shortCommit(report.CurrentCommit))
		for _, change := range report.Changes {
			fmt.Fprintf(stdout, "%s %s %s\n", change.Category, change.Kind, change.Key)
		}
	} else if baselineUpdated {
		fmt.Fprintf(stdout, "compatibility baseline updated: %s (%s)\n", compatibilityDisplayPath(repositoryRoot, resolvedBaseline), shortCommit(current.TypeScriptGoCommit))
		writeCompatibilityCounts(stdout, current)
	} else {
		fmt.Fprintf(stdout, "compatible: baseline %s matches current %s\n", shortCommit(report.BaselineCommit), shortCommit(report.CurrentCommit))
		writeCompatibilityCounts(stdout, current)
	}
	if !report.Compatible {
		return exitDiagnostics
	}
	return exitSuccess
}

func readCompatibilityCheckoutCommit(repositoryRoot string) (string, error) {
	data, err := os.ReadFile(filepath.Join(repositoryRoot, "ts2bin.lock.json"))
	if err != nil {
		return "", err
	}
	var lock struct {
		SchemaVersion int    `json:"schemaVersion"`
		LockFormat    string `json:"lockFormat"`
		TypeScriptGo  struct {
			Commit string `json:"commit"`
		} `json:"typescriptGo"`
	}
	if err := json.Unmarshal(data, &lock); err != nil {
		return "", fmt.Errorf("decode ts2bin.lock.json: %w", err)
	}
	if lock.SchemaVersion != 2 || lock.LockFormat != "ts2bin.lock.v2" {
		return "", fmt.Errorf("unsupported lock schema %d or format %q", lock.SchemaVersion, lock.LockFormat)
	}
	decoded, err := hex.DecodeString(lock.TypeScriptGo.Commit)
	if err != nil || len(decoded) != 20 || strings.ToLower(lock.TypeScriptGo.Commit) != lock.TypeScriptGo.Commit {
		return "", fmt.Errorf("typescript-go commit %q is not a lowercase 40-character SHA-1", lock.TypeScriptGo.Commit)
	}
	return lock.TypeScriptGo.Commit, nil
}

func writeCompatibilityBaseline(repositoryRoot, path string, snapshot tsfrontend.CompatibilitySnapshot) error {
	data, err := tsfrontend.MarshalCompatibilitySnapshot(snapshot)
	if err != nil {
		return fmt.Errorf("encode current snapshot: %w", err)
	}
	data = append(data, '\n')
	relative, err := filepath.Rel(repositoryRoot, path)
	if err != nil {
		return fmt.Errorf("resolve baseline path relative to repository: %w", err)
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return fmt.Errorf("baseline path %q escapes repository root", path)
	}
	repository, err := os.OpenRoot(repositoryRoot)
	if err != nil {
		return fmt.Errorf("open repository root: %w", err)
	}
	defer repository.Close()
	parentPath := filepath.Dir(relative)
	parent, err := repository.OpenRoot(parentPath)
	if err != nil {
		return fmt.Errorf("open baseline directory %q inside repository: %w", parentPath, err)
	}
	defer parent.Close()
	baselineName := filepath.Base(relative)

	mode := os.FileMode(0o644)
	if existing, readErr := parent.ReadFile(baselineName); readErr == nil {
		if _, parseErr := tsfrontend.ParseCompatibilitySnapshot(existing); parseErr != nil {
			return fmt.Errorf("refusing to overwrite non-baseline file %q: %w", path, parseErr)
		}
		if info, statErr := parent.Stat(baselineName); statErr == nil {
			mode = info.Mode().Perm()
		}
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return fmt.Errorf("read existing baseline %q: %w", path, readErr)
	}
	temporaryName, temporary, err := createCompatibilityTemporary(parent)
	if err != nil {
		return fmt.Errorf("create temporary baseline: %w", err)
	}
	defer parent.Remove(temporaryName)
	if err := temporary.Chmod(mode); err != nil {
		temporary.Close()
		return fmt.Errorf("set temporary baseline permissions: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return fmt.Errorf("write temporary baseline: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("sync temporary baseline: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary baseline: %w", err)
	}
	verified, err := parent.ReadFile(temporaryName)
	if err != nil {
		return fmt.Errorf("verify temporary baseline: %w", err)
	}
	if _, err := tsfrontend.ParseCompatibilitySnapshot(verified); err != nil {
		return fmt.Errorf("verify temporary baseline: %w", err)
	}
	if err := parent.Rename(temporaryName, baselineName); err != nil {
		return fmt.Errorf("replace baseline %q: %w", path, err)
	}
	return nil
}

func createCompatibilityTemporary(parent *os.Root) (string, *os.File, error) {
	for attempt := 0; attempt < 32; attempt++ {
		var entropy [12]byte
		if _, err := rand.Read(entropy[:]); err != nil {
			return "", nil, err
		}
		name := ".ts2bin-compatibility-" + hex.EncodeToString(entropy[:]) + ".json"
		file, err := parent.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		return name, file, err
	}
	return "", nil, fmt.Errorf("exhausted temporary baseline name attempts")
}

func writeCompatibilityCounts(writer io.Writer, snapshot tsfrontend.CompatibilitySnapshot) {
	fmt.Fprintf(writer, "kinds=%d api=%d stdlib=%d semantics=%d\n", len(snapshot.Kinds), len(snapshot.API), len(snapshot.Stdlib), len(snapshot.Semantics))
}

func compatibilityDisplayPath(repositoryRoot, path string) string {
	relative, err := filepath.Rel(repositoryRoot, path)
	if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return filepath.ToSlash(relative)
	}
	return path
}

func resolveCompatibilityBaselinePath(repositoryRoot, moduleRoot, requested string) (string, error) {
	path := requested
	if path == "" {
		path = filepath.Join(moduleRoot, filepath.FromSlash(tsfrontend.CompatibilityBaselineModulePath))
	} else if !filepath.IsAbs(path) {
		path = filepath.Join(repositoryRoot, path)
	}
	path = filepath.Clean(path)
	relative, err := filepath.Rel(repositoryRoot, path)
	if err != nil {
		return "", fmt.Errorf("resolve baseline path %q: %w", requested, err)
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("baseline path %q escapes repository root", requested)
	}
	if err := ensureCompatibilityPathInsideRepository(repositoryRoot, path); err != nil {
		return "", fmt.Errorf("baseline path %q: %w", requested, err)
	}
	return path, nil
}

func ensureCompatibilityPathInsideRepository(repositoryRoot, path string) error {
	root, err := filepath.Abs(repositoryRoot)
	if err != nil {
		return fmt.Errorf("resolve repository root: %w", err)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return fmt.Errorf("resolve repository root links: %w", err)
	}
	target, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolve path: %w", err)
	}
	ancestor := target
	for {
		if _, err := os.Lstat(ancestor); err == nil {
			break
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("inspect path: %w", err)
		}
		parent := filepath.Dir(ancestor)
		if parent == ancestor {
			return fmt.Errorf("path has no existing ancestor")
		}
		ancestor = parent
	}
	resolvedAncestor, err := filepath.EvalSymlinks(ancestor)
	if err != nil {
		return fmt.Errorf("resolve existing path links: %w", err)
	}
	relative, err := filepath.Rel(root, resolvedAncestor)
	if err != nil {
		return fmt.Errorf("compare resolved path: %w", err)
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("resolved path escapes repository root")
	}
	return nil
}
