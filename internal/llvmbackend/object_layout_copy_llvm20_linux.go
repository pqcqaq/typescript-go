//go:build llvm20 && cgo && linux

package llvmbackend

import (
	"fmt"

	"github.com/microsoft/typescript-go/internal/bingo"
	llvm "tinygo.org/x/go-llvm"
)

func emitObjectLayoutCopyObject(targetMachine llvm.TargetMachine, manifest ToolchainManifest, plan ObjectLayoutCopyBackendPlan) (ObjectLayoutCopyEmission, error) {
	ctx := llvm.NewContext()
	defer ctx.Dispose()
	module := ctx.NewModule("ts2bin-object-layout-copy")
	defer module.Dispose()
	module.SetTarget(manifest.TargetTriple)
	module.SetDataLayout(manifest.DataLayout.LayoutString)
	builder := ctx.NewBuilder()
	defer builder.Dispose()
	if err := emitObjectLayoutCopyFunction(ctx, builder, module, plan); err != nil {
		return ObjectLayoutCopyEmission{}, err
	}
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		return ObjectLayoutCopyEmission{}, fmt.Errorf("verify object layout copy LLVM module: %w", err)
	}
	ir := []byte(module.String())
	buffer, err := targetMachine.EmitToMemoryBuffer(module, llvm.ObjectFile)
	if err != nil {
		return ObjectLayoutCopyEmission{}, fmt.Errorf("emit object layout copy object: %w", err)
	}
	defer buffer.Dispose()
	return newEmission(plan.ContentHash, manifest, ir, buffer.Bytes())
}

func emitObjectLayoutCopyFunction(ctx llvm.Context, builder llvm.Builder, module llvm.Module, plan ObjectLayoutCopyBackendPlan) error {
	if err := VerifyCanonicalObjectLayoutCopyBackendPlan(plan); err != nil {
		return err
	}
	i8, i32, i64 := ctx.Int8Type(), ctx.Int32Type(), ctx.Int64Type()
	ptr := llvm.PointerType(i8, 0)
	double := ctx.DoubleType()
	null := llvm.ConstNull(ptr)
	shape, err := emitVERT012Shape(ctx, module, "object.layout.copy.target", plan.Bound.MIR.HIR.Copy.TargetLayout, ptr, i8, ctx.Int16Type(), i32, i64, null)
	if err != nil {
		return err
	}
	bindings := make(map[bingo.RuntimeCapabilityID]string, len(plan.Bound.Bindings))
	for _, binding := range plan.Bound.Bindings {
		bindings[binding.LogicalName] = binding.SymbolName
	}
	declare := func(logical bingo.RuntimeCapabilityID, args []llvm.Type) (vert010RuntimeFunction, error) {
		name := bindings[logical]
		if name == "" {
			return vert010RuntimeFunction{}, fmt.Errorf("object layout copy runtime capability %q is unbound", logical)
		}
		typ := llvm.FunctionType(i32, args, false)
		fn := llvm.AddFunction(module, name, typ)
		fn.SetFunctionCallConv(llvm.CCallConv)
		return vert010RuntimeFunction{value: fn, functionType: typ}, nil
	}
	alloc, err := declare("rt.gc.alloc", []llvm.Type{ptr, llvm.PointerType(ptr, 0)})
	if err != nil {
		return err
	}
	link, err := declare("rt.gc.frame.link", []llvm.Type{ptr})
	if err != nil {
		return err
	}
	unlink, err := declare("rt.gc.frame.unlink", []llvm.Type{ptr})
	if err != nil {
		return err
	}
	publish, err := declare("rt.gc.root.publish", []llvm.Type{ptr, i64})
	if err != nil {
		return err
	}
	reload, err := declare("rt.gc.root.reload", []llvm.Type{ptr, i32, llvm.PointerType(ptr, 0)})
	if err != nil {
		return err
	}
	store, err := declare("rt.gc.root.store", []llvm.Type{ptr, i32, ptr})
	if err != nil {
		return err
	}
	fnType := llvm.FunctionType(ptr, []llvm.Type{ptr}, false)
	fn := llvm.AddFunction(module, plan.FunctionName, fnType)
	fn.SetFunctionCallConv(llvm.CCallConv)
	fn.Param(0).SetName("source")
	entry := llvm.AddBasicBlock(fn, "entry")
	builder.SetInsertPointAtEnd(entry)
	zero := llvm.ConstInt(i32, 0, false)
	slotsType := llvm.ArrayType(ptr, 1)
	slots := builder.CreateAlloca(slotsType, "gc.slots")
	slot := builder.CreateInBoundsGEP(slotsType, slots, []llvm.Value{zero, zero}, "gc.slot.0")
	builder.CreateStore(null, slot)
	frameType := ctx.StructType([]llvm.Type{ptr, ptr, i32, i32, i64}, false)
	frame := builder.CreateAlloca(frameType, "gc.frame")
	builder.CreateStore(null, builder.CreateStructGEP(frameType, frame, 0, "frame.previous"))
	builder.CreateStore(slot, builder.CreateStructGEP(frameType, frame, 1, "frame.slots"))
	builder.CreateStore(llvm.ConstInt(i32, 1, false), builder.CreateStructGEP(frameType, frame, 2, "frame.slot_count"))
	builder.CreateStore(zero, builder.CreateStructGEP(frameType, frame, 3, "frame.reserved"))
	builder.CreateStore(llvm.ConstInt(i64, 0, false), builder.CreateStructGEP(frameType, frame, 4, "frame.active_bits"))
	sourceOut := builder.CreateAlloca(ptr, "source.out")
	targetOut := builder.CreateAlloca(ptr, "target.out")
	builder.CreateStore(fn.Param(0), sourceOut)
	builder.CreateStore(null, targetOut)
	checked := func(name string, r vert010RuntimeFunction, args []llvm.Value) {
		status := builder.CreateCall(r.functionType, r.value, args, name+".status")
		emitVERT010StatusCheck(ctx, builder, module, fn, status, name)
	}
	checked("frame.link", link, []llvm.Value{frame})
	checked("root.store.source", store, []llvm.Value{frame, zero, fn.Param(0)})
	checked("root.publish.source", publish, []llvm.Value{frame, llvm.ConstInt(i64, 1, false)})
	checked("target.alloc", alloc, []llvm.Value{shape, targetOut})
	checked("root.reload.source", reload, []llvm.Value{frame, zero, sourceOut})
	source := builder.CreateLoad(ptr, sourceOut, "source.reloaded")
	target := builder.CreateLoad(ptr, targetOut, "target")
	sourceField := builder.CreateInBoundsGEP(i8, source, []llvm.Value{llvm.ConstInt(i64, uint64(plan.SourceOffset), false)}, "source.field")
	targetField := builder.CreateInBoundsGEP(i8, target, []llvm.Value{llvm.ConstInt(i64, uint64(plan.TargetOffset), false)}, "target.field")
	value := builder.CreateLoad(double, sourceField, "source.value")
	builder.CreateStore(value, targetField)
	checked("frame.unlink", unlink, []llvm.Value{frame})
	builder.CreateRet(target)
	return nil
}
