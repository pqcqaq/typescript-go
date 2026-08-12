//go:build llvm20 && cgo && linux

package bingomir

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/internal/llvmbackend"
)

func TestResolveAndBindPropertyAccessStopsAtStaticRuntimeProfileGap(t *testing.T) {
	snapshot, identity, _, plan := propertyAccessPipelineFixture(t)
	machine, err := llvmbackend.OpenFirstSliceTargetMachine()
	if err != nil {
		t.Fatal(err)
	}
	defer machine.Close()
	lowered, err := LowerPropertyAccess(snapshot, identity, plan, machine)
	if err != nil {
		t.Fatal(err)
	}
	runtimeManifest, err := os.ReadFile(filepath.Join("..", "targetcontext", "testdata", "runtime-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveAndBindPropertyAccess(lowered, plan, machine, runtimeManifest); err == nil || !strings.Contains(err.Error(), "does not match runtime manifest") {
		t.Fatalf("property access did not stop at static runtime profile gap: %v", err)
	}
}
