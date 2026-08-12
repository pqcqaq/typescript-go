//go:build llvm20 && cgo && linux

package llvmbackend

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/internal/ast2bingo"
	"github.com/microsoft/typescript-go/internal/bingo"
	"github.com/microsoft/typescript-go/internal/frontendwire"
	llvm "tinygo.org/x/go-llvm"
)

func TestVERT011LLVMUsesBoundCapabilitiesAccessorsAndCFG(t *testing.T) {
	bound := testVERT011BoundMIR(t)
	llvm.InitializeAllTargetInfos()
	llvm.InitializeAllTargets()
	llvm.InitializeAllTargetMCs()
	target, err := llvm.GetTargetFromTriple(FirstSliceTriple)
	if err != nil {
		t.Fatal(err)
	}
	machine := target.CreateTargetMachine(FirstSliceTriple, FirstSliceCPU, "", llvm.CodeGenLevelNone, llvm.RelocPIC, llvm.CodeModelSmall)
	defer machine.Dispose()
	ctx := llvm.NewContext()
	defer ctx.Dispose()
	module := ctx.NewModule("vert011-test")
	defer module.Dispose()
	module.SetTarget(FirstSliceTriple)
	module.SetDataLayout(FirstSliceDataLayout)
	builder := ctx.NewBuilder()
	defer builder.Dispose()
	if err := emitVERT011Module(ctx, builder, module, bound); err != nil {
		t.Fatal(err)
	}
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("O0 verify: %v", err)
	}
	ir := module.String()
	for _, binding := range bound.Closure.Bindings {
		if !strings.Contains(ir, "@"+binding.SymbolName+"(") {
			t.Fatalf("bound capability symbol %q is absent", binding.SymbolName)
		}
	}
	for _, fragment := range []string{"@vert011.shape = private constant", "@vert011.properties = private constant", "@vert011.trace = private constant", "define private void @vert011.getter.", "define private void @vert011.setter.", "br i1 %loaded.is_nullish", "result = phi double"} {
		if !strings.Contains(ir, fragment) {
			t.Fatalf("VERT-011 LLVM evidence %q is absent", fragment)
		}
	}
	if strings.Contains(ir, "@bingo_gc_") {
		t.Fatal("VERT-011 LLVM bypassed bound capability symbols")
	}
	options := llvm.NewPassBuilderOptions()
	defer options.Dispose()
	options.SetVerifyEach(true)
	if err := module.RunPasses("default<O2>", machine, options); err != nil {
		t.Fatalf("O2 passes: %v", err)
	}
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("O2 verify: %v", err)
	}
	optimized := module.String()
	for _, binding := range bound.Closure.Bindings {
		if !strings.Contains(optimized, "@"+binding.SymbolName+"(") {
			t.Fatalf("O2 removed required capability call %q", binding.SymbolName)
		}
	}
}

func TestTargetMachineEmitsVERT011ELFObject(t *testing.T) {
	bound := testVERT011BoundMIR(t)
	machine, err := OpenFirstSliceTargetMachine()
	if err != nil {
		t.Fatal(err)
	}
	defer machine.Close()
	emission, err := machine.EmitVERT011Object(bound)
	if err != nil {
		t.Fatal(err)
	}
	if emission.MIRContentHash != bound.ContentHash || len(emission.LLVMIR) == 0 || len(emission.Object) < 4 || emission.Object[0] != 0x7f || string(emission.Object[1:4]) != "ELF" {
		t.Fatalf("invalid VERT-011 emission identity")
	}
}

func TestVERT011ObjectLinksRunsAndMatchesNode(t *testing.T) {
	bound := testVERT011BoundMIR(t)
	runtimeSymbols := map[bingo.RuntimeCapabilityID]string{
		"rt.gc.alloc": "bingo_gc_alloc_v1", "rt.gc.frame.link": "bingo_gc_frame_link_v1",
		"rt.gc.frame.unlink": "bingo_gc_frame_unlink_v1", "rt.gc.root.clear": "bingo_gc_root_clear_v1",
		"rt.gc.root.publish": "bingo_gc_root_publish_v1", "rt.gc.root.reload": "bingo_gc_root_reload_v1",
		"rt.gc.root.store": "bingo_gc_root_store_v1", "rt.gc.safepoint": "bingo_gc_safepoint_v1",
	}
	bindings := make([]bingo.BoundCapability, len(bound.MIR.LogicalCapabilityRequirements))
	for index, requirement := range bound.MIR.LogicalCapabilityRequirements {
		bindings[index] = bingo.BoundCapability{LogicalName: requirement, SymbolName: runtimeSymbols[requirement], SignatureHash: strings.Repeat("f", 64)}
	}
	var err error
	bound, err = bingo.NewVERT011BoundMIR(bound.MIR, bound.TargetContextHash, bound.Closure.AvailableCapabilityCatalogHash, bindings)
	if err != nil {
		t.Fatal(err)
	}
	machine, err := OpenFirstSliceTargetMachine()
	if err != nil {
		t.Fatal(err)
	}
	defer machine.Close()
	emission, err := machine.EmitVERT011Object(bound)
	if err != nil {
		t.Fatal(err)
	}
	clang, err := exec.LookPath("clang-20")
	if err != nil {
		t.Fatal(err)
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	runtimeDirectory := filepath.Clean(filepath.Join("..", "..", "..", "runtime", "bingo-rt", "target", "first-slice"))
	archive := filepath.Join(runtimeDirectory, "cargo", FirstSliceTriple, "release", "libbingo_runtime.a")
	if _, err := os.Stat(archive); err != nil {
		t.Fatalf("runtime archive unavailable: %v", err)
	}
	workspace := t.TempDir()
	objectPath, harnessPath, executable := filepath.Join(workspace, "property.o"), filepath.Join(workspace, "harness.c"), filepath.Join(workspace, "property")
	if err := os.WriteFile(objectPath, emission.Object, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(harnessPath, []byte(vert011HarnessSource), 0o600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(clang, "--target="+FirstSliceTriple, "-fuse-ld=lld", "-no-pie", "-Wl,--build-id=none", "-Wl,--no-undefined", objectPath, harnessPath, archive, "-ldl", "-lpthread", "-lm", "-o", executable)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("link VERT-011 executable: %v: %s", err, output)
	}
	tests := []struct {
		input, want string
		setter      int
	}{
		{"0000000000000000", "0000000000000000", 0}, {"8000000000000000", "8000000000000000", 0},
		{"3ff0000000000000", "3ff0000000000000", 0}, {"7ff0000000000000", "7ff0000000000000", 0},
		{"7ff8000000000042", "7ff8000000000042", 0}, {"null", "3ff0000000000000", 1}, {"undefined", "3ff0000000000000", 1},
	}
	for _, test := range tests {
		output, err := exec.Command(executable, test.input).CombinedOutput()
		if err != nil {
			t.Fatalf("native(%s): %v: %s", test.input, err, output)
		}
		if got := strings.TrimSpace(string(output)); got != test.want {
			t.Fatalf("native(%s) = %s, want %s", test.input, got, test.want)
		}
		nodeOutput, err := exec.Command(node, "-e", vert011NodeOracle, test.input).CombinedOutput()
		if err != nil {
			t.Fatalf("Node(%s): %v: %s", test.input, err, nodeOutput)
		}
		var result struct {
			Bits                               string `json:"bits"`
			Receiver, Key, Getter, Setter, RHS int
		}
		if err := json.Unmarshal(nodeOutput, &result); err != nil {
			t.Fatal(err)
		}
		if result.Bits != test.want || result.Receiver != 1 || result.Key != 1 || result.Getter != 1 || result.Setter != test.setter || result.RHS != test.setter {
			t.Fatalf("Node(%s) = %#v, native bits = %s", test.input, result, strings.TrimSpace(string(output)))
		}
	}
}

const vert011HarnessSource = `
#include <inttypes.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
extern double propertyNullishAssign(double payload, uint8_t tag);
int main(int argc, char **argv) {
  if (argc != 2) return 2;
  uint64_t bits = 0; uint8_t tag = 0;
  if (strcmp(argv[1], "null") == 0) tag = 1;
  else if (strcmp(argv[1], "undefined") == 0) tag = 2;
  else { char *end = 0; bits = strtoull(argv[1], &end, 16); if (!end || *end) return 3; }
  double payload = 0; memcpy(&payload, &bits, sizeof(bits));
  double result = propertyNullishAssign(payload, tag); memcpy(&bits, &result, sizeof(bits));
  printf("%016" PRIx64 "\n", bits); return 0;
}`

const vert011NodeOracle = `
const input = process.argv[1];
let value;
if (input === "null") value = null;
else if (input === "undefined") value = undefined;
else { const b = Buffer.alloc(8); b.writeBigUInt64LE(BigInt("0x" + input)); value = b.readDoubleLE(); }
const c = {receiver:0,key:0,getter:0,setter:0,rhs:0};
const object = {backing:value, get result(){c.getter++;return this.backing}, set result(v){c.setter++;this.backing=v}};
function receiver(){c.receiver++;return object} function key(){c.key++;return "result"} function rhs(){c.rhs++;return 1}
const result = (receiver()[key()] ??= rhs()); const b = Buffer.alloc(8); b.writeDoubleLE(result);
console.log(JSON.stringify({bits:b.readBigUInt64LE().toString(16).padStart(16,"0"),...c}));`

func testVERT011BoundMIR(t testing.TB) bingo.VERT011BoundMIR {
	t.Helper()
	data, err := os.ReadFile("../../testdata/ts2bin/propertynullishassign/frontend-snapshot.json")
	if err != nil {
		t.Fatal(err)
	}
	frontend, err := frontendwire.DecodeFrontendSnapshot(data)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := ast2bingo.NewCompilerBuildIdentity("86cc4767d4ebadb9b7845d0ab8eb2b05785c3fee", "9a53ae50f6da67c9b3948b239d8292967e42422b")
	if err != nil {
		t.Fatal(err)
	}
	replay, err := ast2bingo.ReplayVERT011Snapshot(frontend.Program, identity)
	if err != nil {
		t.Fatal(err)
	}
	target, err := bingo.CanonicalObjectLayoutTarget(bingo.ObjectLayoutX8664Triple)
	if err != nil {
		t.Fatal(err)
	}
	layout, err := bingo.PlanObjectLayout(replay.HIR.PlaceRefs.Places[0].ObjectTypeKey, target, []bingo.ObjectLayoutPropertyInput{
		{Key: "backing", Kind: bingo.ObjectPropertyData, Representation: "nullable-f64"},
		{Key: "result", Kind: bingo.ObjectPropertyAccessor},
	})
	if err != nil {
		t.Fatal(err)
	}
	mir, err := bingo.LowerVERT011MIR(replay.HIR, layout)
	if err != nil {
		t.Fatal(err)
	}
	bindings := make([]bingo.BoundCapability, len(mir.LogicalCapabilityRequirements))
	for index, requirement := range mir.LogicalCapabilityRequirements {
		bindings[index] = bingo.BoundCapability{LogicalName: requirement, SymbolName: "test_" + strings.NewReplacer(".", "_").Replace(string(requirement)), SignatureHash: strings.Repeat("f", 64)}
	}
	bound, err := bingo.NewVERT011BoundMIR(mir, strings.Repeat("1", 64), strings.Repeat("2", 64), bindings)
	if err != nil {
		t.Fatal(err)
	}
	return bound
}
