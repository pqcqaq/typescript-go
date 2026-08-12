//go:build llvm20 && cgo && linux

package llvmbackend

import (
	"fmt"

	"github.com/microsoft/typescript-go/internal/bingo"
	llvm "tinygo.org/x/go-llvm"
)

type vert010RuntimeFunction struct {
	value        llvm.Value
	functionType llvm.Type
}

func emitVERT010Object(targetMachine llvm.TargetMachine, manifest ToolchainManifest, bound bingo.VERT010BoundMIR) (VERT010Emission, error) {
	ctx := llvm.NewContext()
	defer ctx.Dispose()
	module := ctx.NewModule("ts2bin-vert-010")
	defer module.Dispose()
	module.SetTarget(manifest.TargetTriple)
	module.SetDataLayout(manifest.DataLayout.LayoutString)
	builder := ctx.NewBuilder()
	defer builder.Dispose()

	if err := emitVERT010Function(ctx, builder, module, bound); err != nil {
		return VERT010Emission{}, err
	}
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		return VERT010Emission{}, fmt.Errorf("verify VERT-010 LLVM module: %w", err)
	}
	llvmIR := []byte(module.String())
	buffer, err := targetMachine.EmitToMemoryBuffer(module, llvm.ObjectFile)
	if err != nil {
		return VERT010Emission{}, fmt.Errorf("emit VERT-010 object: %w", err)
	}
	defer buffer.Dispose()
	return newVERT010Emission(bound, manifest, llvmIR, buffer.Bytes())
}

func emitVERT010Function(ctx llvm.Context, builder llvm.Builder, module llvm.Module, bound bingo.VERT010BoundMIR) error {
	mir := bound.MIR
	if len(mir.Layout.Fields) != 1 || len(mir.Function.Instructions) != 8 {
		return fmt.Errorf("unsupported VERT-010 MIR shape")
	}
	i8, i32, i64 := ctx.Int8Type(), ctx.Int32Type(), ctx.Int64Type()
	ptr := llvm.PointerType(i8, 0)
	double := ctx.DoubleType()
	void := ctx.VoidType()
	null := llvm.ConstNull(ptr)

	traceType := ctx.StructType([]llvm.Type{i32, i32, i64, i32, i32, ptr, ptr}, false)
	trace := llvm.AddGlobal(module, traceType, "vert010.trace")
	trace.SetLinkage(llvm.PrivateLinkage)
	trace.SetGlobalConstant(true)
	trace.SetInitializer(llvm.ConstNamedStruct(traceType, []llvm.Value{
		llvm.ConstInt(i32, 1, false), llvm.ConstInt(i32, 0, false), llvm.ConstInt(i64, uint64(mir.Layout.ObjectSize), false),
		llvm.ConstInt(i32, 0, false), llvm.ConstInt(i32, 0, false), null, null,
	}))

	keyBytes := llvm.ConstString("value", true)
	key := llvm.AddGlobal(module, keyBytes.Type(), "vert010.key.value")
	key.SetLinkage(llvm.PrivateLinkage)
	key.SetGlobalConstant(true)
	key.SetInitializer(keyBytes)
	propertyType := ctx.StructType([]llvm.Type{ptr, i8, i8, ctx.Int16Type(), i32, i32, i32, i32, ptr}, false)
	property := llvm.AddGlobal(module, propertyType, "vert010.property.value")
	property.SetLinkage(llvm.PrivateLinkage)
	property.SetGlobalConstant(true)
	property.SetInitializer(llvm.ConstNamedStruct(propertyType, []llvm.Value{
		key, llvm.ConstInt(i8, 1, false), llvm.ConstInt(i8, 0, false), llvm.ConstInt(ctx.Int16Type(), 0, false),
		llvm.ConstInt(i32, uint64(mir.Layout.Fields[0].FieldOffset), false), llvm.ConstInt(i32, uint64(^uint32(0)), false),
		llvm.ConstInt(i32, 0, false), llvm.ConstInt(i32, 0, false), null,
	}))

	shapeType := ctx.StructType([]llvm.Type{i32, i32, i64, i64, i32, i32, ptr, ptr}, false)
	shape := llvm.AddGlobal(module, shapeType, "vert010.shape")
	shape.SetLinkage(llvm.PrivateLinkage)
	shape.SetGlobalConstant(true)
	shape.SetInitializer(llvm.ConstNamedStruct(shapeType, []llvm.Value{
		llvm.ConstInt(i32, 1, false), llvm.ConstInt(i32, 0, false), llvm.ConstInt(i64, uint64(mir.Layout.ObjectSize), false),
		llvm.ConstInt(i64, uint64(mir.Layout.ObjectAlign), false), llvm.ConstInt(i32, 1, false), llvm.ConstInt(i32, 0, false), property, trace,
	}))

	bindings := make(map[bingo.RuntimeCapabilityID]string, len(bound.Closure.Bindings))
	for _, binding := range bound.Closure.Bindings {
		bindings[binding.LogicalName] = binding.SymbolName
	}
	declare := func(logical bingo.RuntimeCapabilityID, args []llvm.Type) (vert010RuntimeFunction, error) {
		name := bindings[logical]
		if name == "" {
			return vert010RuntimeFunction{}, fmt.Errorf("VERT-010 runtime capability %q is unbound", logical)
		}
		functionType := llvm.FunctionType(i32, args, false)
		function := llvm.AddFunction(module, name, functionType)
		function.SetFunctionCallConv(llvm.CCallConv)
		return vert010RuntimeFunction{value: function, functionType: functionType}, nil
	}
	alloc, err := declare("rt.gc.alloc", []llvm.Type{ptr, llvm.PointerType(ptr, 0)})
	if err != nil {
		return err
	}
	frameLink, err := declare("rt.gc.frame.link", []llvm.Type{ptr})
	if err != nil {
		return err
	}
	frameUnlink, err := declare("rt.gc.frame.unlink", []llvm.Type{ptr})
	if err != nil {
		return err
	}
	rootClear, err := declare("rt.gc.root.clear", []llvm.Type{ptr, i32})
	if err != nil {
		return err
	}
	rootPublish, err := declare("rt.gc.root.publish", []llvm.Type{ptr, i64})
	if err != nil {
		return err
	}
	rootReload, err := declare("rt.gc.root.reload", []llvm.Type{ptr, i32, llvm.PointerType(ptr, 0)})
	if err != nil {
		return err
	}
	rootStore, err := declare("rt.gc.root.store", []llvm.Type{ptr, i32, ptr})
	if err != nil {
		return err
	}
	safepoint, err := declare("rt.gc.safepoint", nil)
	if err != nil {
		return err
	}

	functionType := llvm.FunctionType(double, []llvm.Type{double}, false)
	function := llvm.AddFunction(module, mir.Function.Name, functionType)
	function.SetFunctionCallConv(llvm.CCallConv)
	function.Param(0).SetName("value")
	entry := llvm.AddBasicBlock(function, "entry")
	builder.SetInsertPointAtEnd(entry)
	slotsType := llvm.ArrayType(ptr, 1)
	slots := builder.CreateAlloca(slotsType, "gc.slots")
	zero32 := llvm.ConstInt(i32, 0, false)
	slot := builder.CreateInBoundsGEP(slotsType, slots, []llvm.Value{zero32, zero32}, "gc.slot.0")
	builder.CreateStore(null, slot)
	frameType := ctx.StructType([]llvm.Type{ptr, ptr, i32, i32, i64}, false)
	frame := builder.CreateAlloca(frameType, "gc.frame")
	builder.CreateStore(null, builder.CreateStructGEP(frameType, frame, 0, "frame.previous"))
	builder.CreateStore(slot, builder.CreateStructGEP(frameType, frame, 1, "frame.slots"))
	builder.CreateStore(llvm.ConstInt(i32, 1, false), builder.CreateStructGEP(frameType, frame, 2, "frame.slot_count"))
	builder.CreateStore(zero32, builder.CreateStructGEP(frameType, frame, 3, "frame.reserved"))
	builder.CreateStore(llvm.ConstInt(i64, 0, false), builder.CreateStructGEP(frameType, frame, 4, "frame.active_bits"))
	outObject := builder.CreateAlloca(ptr, "object.out")
	builder.CreateStore(null, outObject)

	callChecked := func(name string, runtime vert010RuntimeFunction, args []llvm.Value) {
		status := builder.CreateCall(runtime.functionType, runtime.value, args, name+".status")
		emitVERT010StatusCheck(ctx, builder, module, function, status, name)
	}
	callChecked("frame.link", frameLink, []llvm.Value{frame})
	callChecked("root.clear", rootClear, []llvm.Value{frame, zero32})
	callChecked("root.publish.empty", rootPublish, []llvm.Value{frame, llvm.ConstInt(i64, 0, false)})
	callChecked("safepoint.allocation", safepoint, nil)
	callChecked("object.alloc", alloc, []llvm.Value{shape, outObject})
	object := builder.CreateLoad(ptr, outObject, "object")
	fieldPointer := builder.CreateInBoundsGEP(i8, object, []llvm.Value{llvm.ConstInt(i64, uint64(mir.Layout.Fields[0].FieldOffset), false)}, "field.value")
	builder.CreateStore(function.Param(0), fieldPointer)
	callChecked("root.store", rootStore, []llvm.Value{frame, zero32, object})
	callChecked("root.publish.live", rootPublish, []llvm.Value{frame, llvm.ConstInt(i64, 1, false)})
	callChecked("safepoint.forced", safepoint, nil)
	callChecked("root.reload", rootReload, []llvm.Value{frame, zero32, outObject})
	reloaded := builder.CreateLoad(ptr, outObject, "object.reloaded")
	reloadedField := builder.CreateInBoundsGEP(i8, reloaded, []llvm.Value{llvm.ConstInt(i64, uint64(mir.Layout.Fields[0].FieldOffset), false)}, "field.value.reloaded")
	current := builder.CreateLoad(double, reloadedField, "alias.value")
	updated := builder.CreateFAdd(current, llvm.ConstFloat(double, 1), "alias.value.updated")
	builder.CreateStore(updated, reloadedField)
	result := builder.CreateLoad(double, reloadedField, "object.value")
	callChecked("frame.unlink", frameUnlink, []llvm.Value{frame})
	builder.CreateRet(result)
	_ = void
	return nil
}

func emitVERT010StatusCheck(ctx llvm.Context, builder llvm.Builder, module llvm.Module, function llvm.Value, status llvm.Value, name string) {
	ok := llvm.AddBasicBlock(function, name+".ok")
	fail := llvm.AddBasicBlock(function, name+".fail")
	builder.CreateCondBr(builder.CreateICmp(llvm.IntEQ, status, llvm.ConstInt(ctx.Int32Type(), 0, false), name+".succeeded"), ok, fail)
	builder.SetInsertPointAtEnd(fail)
	trapType := llvm.FunctionType(ctx.VoidType(), nil, false)
	trap := module.NamedFunction("llvm.trap")
	if trap.IsNil() {
		trap = llvm.AddFunction(module, "llvm.trap", trapType)
		trap.AddFunctionAttr(ctx.CreateEnumAttribute(llvm.AttributeKindID("nounwind"), 0))
		trap.AddFunctionAttr(ctx.CreateEnumAttribute(llvm.AttributeKindID("noreturn"), 0))
	}
	builder.CreateCall(trapType, trap, nil, "")
	builder.CreateUnreachable()
	builder.SetInsertPointAtEnd(ok)
}
