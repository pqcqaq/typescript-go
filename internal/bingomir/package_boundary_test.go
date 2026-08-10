package bingomir

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestMIRReplayDependencyClosureExcludesFrontendAndCheckerPackages(t *testing.T) {
	command := exec.Command("go", "list", "-deps", "./internal/bingomir")
	command.Dir = filepath.Clean(filepath.Join("..", ".."))
	output, err := command.Output()
	if err != nil {
		t.Fatalf("go list MIR replay dependencies: %v", err)
	}
	banned := map[string]struct{}{
		"github.com/microsoft/typescript-go/internal/ast":        {},
		"github.com/microsoft/typescript-go/internal/astnav":     {},
		"github.com/microsoft/typescript-go/internal/binder":     {},
		"github.com/microsoft/typescript-go/internal/checker":    {},
		"github.com/microsoft/typescript-go/internal/compiler":   {},
		"github.com/microsoft/typescript-go/internal/parser":     {},
		"github.com/microsoft/typescript-go/internal/tsfrontend": {},
		"github.com/microsoft/typescript-go/internal/tsoptions":  {},
	}
	for _, dependency := range strings.Fields(string(output)) {
		if _, forbidden := banned[dependency]; forbidden {
			t.Fatalf("MIR replay dependency closure includes forbidden package %q", dependency)
		}
	}
}
