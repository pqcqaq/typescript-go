package ast2bingo

import (
	"context"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestReplayDependencyClosureExcludesFrontendAndCheckerPackages(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "list", "-buildvcs=false", "-deps", "./internal/ast2bingo", "./cmd/ts2bin-replay")
	command.Dir = filepath.Clean(filepath.Join("..", ".."))
	command.Env = append(os.Environ(), "GOPROXY=off", "GOSUMDB=off")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("go list replay dependencies: %v: %s", err, strings.TrimSpace(string(output)))
	}
	banned := map[string]struct{}{
		"github.com/microsoft/typescript-go/internal/ast":        {},
		"github.com/microsoft/typescript-go/internal/astnav":     {},
		"github.com/microsoft/typescript-go/internal/checker":    {},
		"github.com/microsoft/typescript-go/internal/compiler":   {},
		"github.com/microsoft/typescript-go/internal/tsoptions":  {},
		"github.com/microsoft/typescript-go/internal/tsfrontend": {},
	}
	for _, dependency := range strings.Fields(string(output)) {
		if _, forbidden := banned[dependency]; forbidden {
			t.Fatalf("replay dependency closure includes forbidden package %q", dependency)
		}
	}
}

func TestConsumerProductionFilesDoNotImportCheckerOrAST(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	banned := []string{
		"/internal/ast",
		"/internal/checker",
		"/internal/compiler",
		"/internal/tsoptions",
	}
	files := token.NewFileSet()
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(".", entry.Name())
		file, err := parser.ParseFile(files, path, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for _, spec := range file.Imports {
			path, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				t.Fatal(err)
			}
			for _, suffix := range banned {
				if path == strings.TrimPrefix(suffix, "/") || strings.Contains(path, suffix+"/") || strings.HasSuffix(path, suffix) {
					t.Fatalf("%s imports forbidden checker/AST package %q", file.Name.Name, path)
				}
			}
		}
	}
}

func TestFrontendProductionFilesDoNotImportBingoIR(t *testing.T) {
	entries, err := os.ReadDir("../tsfrontend")
	if err != nil {
		t.Fatal(err)
	}
	files := token.NewFileSet()
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		path := filepath.Join("..", "tsfrontend", entry.Name())
		file, err := parser.ParseFile(files, path, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for _, spec := range file.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				t.Fatal(err)
			}
			if strings.HasSuffix(importPath, "/internal/bingo") || strings.Contains(importPath, "/internal/bingo/") {
				t.Fatalf("%s imports Bingo IR package %q", entry.Name(), importPath)
			}
		}
	}
}
