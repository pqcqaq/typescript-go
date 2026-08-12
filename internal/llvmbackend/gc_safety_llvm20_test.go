//go:build llvm20 && cgo && linux

package llvmbackend

import (
	"fmt"
	"testing"

	"github.com/microsoft/typescript-go/internal/bingo"
	llvm "tinygo.org/x/go-llvm"
)

func TestGCSafetyLLVMRootPublicationSurvivesO0AndO2(t *testing.T) {
	llvm.InitializeAllTargetInfos()
	llvm.InitializeAllTargets()
	llvm.InitializeAllTargetMCs()
	for _, triple := range []string{bingo.ObjectLayoutX8664Triple, bingo.ObjectLayoutAArch64Triple} {
		t.Run(triple, func(t *testing.T) {
			target, err := llvm.GetTargetFromTriple(triple)
			if err != nil {
				t.Fatal(err)
			}
			machine := target.CreateTargetMachine(triple, "generic", "", llvm.CodeGenLevelNone, llvm.RelocPIC, llvm.CodeModelSmall)
			defer machine.Dispose()
			ctx, module, want := buildGCSafetyLLVMLitmus(t, machine, triple)
			defer ctx.Dispose()
			defer module.Dispose()
			if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
				t.Fatalf("O0 verify: %v", err)
			}
			if got := gcSafetyCallOrder(module); !equalStrings(got, want) {
				t.Fatalf("O0 call order = %v, want %v", got, want)
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
			if got := gcSafetyCallOrder(module); !equalStrings(got, want) {
				t.Fatalf("O2 call order = %v, want %v", got, want)
			}
		})
	}
}

func buildGCSafetyLLVMLitmus(t *testing.T, machine llvm.TargetMachine, triple string) (llvm.Context, llvm.Module, []string) {
	t.Helper()
	ctx := llvm.NewContext()
	module := ctx.NewModule("gc-root-publication-litmus")
	data := machine.CreateTargetData()
	defer data.Dispose()
	module.SetDataLayout(data.String())
	module.SetTarget(triple)
	ptr := llvm.PointerType(ctx.Int8Type(), 0)
	i32, i64 := ctx.Int32Type(), ctx.Int64Type()
	void := ctx.VoidType()
	type declaration struct {
		value        llvm.Value
		functionType llvm.Type
	}
	declare := func(name string, result llvm.Type, args []llvm.Type) declaration {
		functionType := llvm.FunctionType(result, args, false)
		fn := llvm.AddFunction(module, name, functionType)
		fn.AddFunctionAttr(ctx.CreateEnumAttribute(llvm.AttributeKindID("noinline"), 0))
		return declaration{value: fn, functionType: functionType}
	}
	link := declare("bingo_gc_frame_link_v1", void, []llvm.Type{ptr})
	store := declare("bingo_gc_root_store_v1", void, []llvm.Type{ptr, i32, ptr})
	clear := declare("bingo_gc_root_clear_v1", void, []llvm.Type{ptr, i32})
	publish := declare("bingo_gc_root_publish_v1", void, []llvm.Type{ptr, i64})
	safepoint := declare("bingo_gc_safepoint_v1", void, nil)
	reload := declare("bingo_gc_root_reload_v1", ptr, []llvm.Type{ptr, i32})
	barrier := declare("bingo_gc_write_barrier_v1", void, []llvm.Type{ptr, i32, ptr})
	unlink := declare("bingo_gc_frame_unlink_v1", void, []llvm.Type{ptr})
	functionType := llvm.FunctionType(ptr, []llvm.Type{ptr, ptr, ptr}, false)
	function := llvm.AddFunction(module, "gc_root_publication_litmus", functionType)
	entry := llvm.AddBasicBlock(function, "entry")
	builder := ctx.NewBuilder()
	defer builder.Dispose()
	builder.SetInsertPointAtEnd(entry)
	call := func(fn declaration, args []llvm.Value) {
		builder.CreateCall(fn.functionType, fn.value, args, "")
	}
	call(link, []llvm.Value{function.Param(0)})
	call(store, []llvm.Value{function.Param(0), llvm.ConstInt(i32, 0, false), function.Param(1)})
	call(clear, []llvm.Value{function.Param(0), llvm.ConstInt(i32, 1, false)})
	call(publish, []llvm.Value{function.Param(0), llvm.ConstInt(i64, 1, false)})
	call(safepoint, nil)
	reloaded := builder.CreateCall(reload.functionType, reload.value, []llvm.Value{function.Param(0), llvm.ConstInt(i32, 0, false)}, "reloaded")
	call(barrier, []llvm.Value{function.Param(2), llvm.ConstInt(i32, 0, false), reloaded})
	call(unlink, []llvm.Value{function.Param(0)})
	builder.CreateRet(reloaded)
	return ctx, module, []string{"bingo_gc_frame_link_v1", "bingo_gc_root_store_v1", "bingo_gc_root_clear_v1", "bingo_gc_root_publish_v1", "bingo_gc_safepoint_v1", "bingo_gc_root_reload_v1", "bingo_gc_write_barrier_v1", "bingo_gc_frame_unlink_v1"}
}

func gcSafetyCallOrder(module llvm.Module) []string {
	function := module.NamedFunction("gc_root_publication_litmus")
	if function.IsNil() {
		return nil
	}
	result := make([]string, 0)
	for instruction := function.EntryBasicBlock().FirstInstruction(); !instruction.IsNil(); instruction = llvm.NextInstruction(instruction) {
		if instruction.IsACallInst().IsNil() {
			continue
		}
		called := instruction.CalledValue()
		name := called.Name()
		if name == "" {
			name = fmt.Sprintf("opcode:%d", instruction.InstructionOpcode())
		}
		result = append(result, name)
	}
	return result
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
