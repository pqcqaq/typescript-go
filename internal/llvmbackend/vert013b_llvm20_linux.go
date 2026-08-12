//go:build llvm20 && cgo && linux

package llvmbackend

import (
	"fmt"

	"github.com/microsoft/typescript-go/internal/bingo"
	llvm "tinygo.org/x/go-llvm"
)

func emitVERT013bObject(targetMachine llvm.TargetMachine, manifest ToolchainManifest, bound bingo.VERT013bBoundMIR) (VERT013bEmission, error) {
	ctx := llvm.NewContext()
	defer ctx.Dispose()
	module := ctx.NewModule("ts2bin-vert-013b")
	defer module.Dispose()
	module.SetTarget(manifest.TargetTriple)
	module.SetDataLayout(manifest.DataLayout.LayoutString)
	builder := ctx.NewBuilder()
	defer builder.Dispose()
	if err := emitVERT013bModule(ctx, builder, module, bound); err != nil {
		return VERT013bEmission{}, err
	}
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		return VERT013bEmission{}, fmt.Errorf("verify VERT-013b LLVM module: %w", err)
	}
	llvmIR := []byte(module.String())
	buffer, err := targetMachine.EmitToMemoryBuffer(module, llvm.ObjectFile)
	if err != nil {
		return VERT013bEmission{}, fmt.Errorf("emit VERT-013b object: %w", err)
	}
	defer buffer.Dispose()
	return newVERT013bEmission(bound, manifest, llvmIR, buffer.Bytes())
}

func emitVERT013bModule(ctx llvm.Context, builder llvm.Builder, module llvm.Module, bound bingo.VERT013bBoundMIR) error {
	mir := bound.MIR
	if len(mir.Functions) != 4 || len(mir.Layout.Base.Properties) != 1 || len(mir.Layout.Derived.Properties) != 2 {
		return fmt.Errorf("unsupported VERT-013b MIR shape")
	}
	i8, i32, i64 := ctx.Int8Type(), ctx.Int32Type(), ctx.Int64Type()
	ptr, double := llvm.PointerType(i8, 0), ctx.DoubleType()
	null, zero32 := llvm.ConstNull(ptr), llvm.ConstInt(i32, 0, false)
	baseOffset := mir.Layout.Derived.Properties[0].FieldOffset
	stepOffset := mir.Layout.Derived.Properties[1].FieldOffset
	shape, err := emitVERT013bShape(ctx, module, mir.Layout.Derived, ptr, i8, ctx.Int16Type(), i32, i64, null)
	if err != nil {
		return err
	}
	bindings := make(map[bingo.RuntimeCapabilityID]string, len(bound.Closure.Bindings))
	for _, binding := range bound.Closure.Bindings {
		bindings[binding.LogicalName] = binding.SymbolName
	}
	declare := func(logical bingo.RuntimeCapabilityID, args []llvm.Type) (vert010RuntimeFunction, error) {
		name := bindings[logical]
		if name == "" {
			return vert010RuntimeFunction{}, fmt.Errorf("VERT-013b runtime capability %q is unbound", logical)
		}
		type_ := llvm.FunctionType(i32, args, false)
		value := llvm.AddFunction(module, name, type_)
		value.SetFunctionCallConv(llvm.CCallConv)
		return vert010RuntimeFunction{value: value, functionType: type_}, nil
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
	frameType := ctx.StructType([]llvm.Type{ptr, ptr, i32, i32, i64}, false)
	makeFrame := func(function llvm.Value, prefix string) (llvm.Value, llvm.Value) {
		slotsType := llvm.ArrayType(ptr, 1)
		slots := builder.CreateAlloca(slotsType, prefix+".slots")
		slot := builder.CreateInBoundsGEP(slotsType, slots, []llvm.Value{zero32, zero32}, prefix+".slot.receiver")
		builder.CreateStore(null, slot)
		frame := builder.CreateAlloca(frameType, prefix+".frame")
		builder.CreateStore(null, builder.CreateStructGEP(frameType, frame, 0, prefix+".frame.previous"))
		builder.CreateStore(slot, builder.CreateStructGEP(frameType, frame, 1, prefix+".frame.slots"))
		builder.CreateStore(llvm.ConstInt(i32, 1, false), builder.CreateStructGEP(frameType, frame, 2, prefix+".frame.slot_count"))
		builder.CreateStore(zero32, builder.CreateStructGEP(frameType, frame, 3, prefix+".frame.reserved"))
		builder.CreateStore(llvm.ConstInt(i64, 0, false), builder.CreateStructGEP(frameType, frame, 4, prefix+".frame.active_bits"))
		_ = function
		return frame, slot
	}
	callChecked := func(function llvm.Value, name string, runtime vert010RuntimeFunction, args []llvm.Value) {
		status := builder.CreateCall(runtime.functionType, runtime.value, args, name+".status")
		emitVERT010StatusCheck(ctx, builder, module, function, status, name)
	}
	fieldPtr := func(receiver llvm.Value, offset uint32, name string) llvm.Value {
		return builder.CreateInBoundsGEP(i8, receiver, []llvm.Value{llvm.ConstInt(i64, uint64(offset), false)}, name)
	}

	baseType := llvm.FunctionType(ptr, []llvm.Type{ptr, double}, false)
	baseCtor := llvm.AddFunction(module, "vert013b.Counter.constructor", baseType)
	baseCtor.SetLinkage(llvm.PrivateLinkage)
	baseCtor.SetFunctionCallConv(llvm.CCallConv)
	b := llvm.AddBasicBlock(baseCtor, "entry")
	builder.SetInsertPointAtEnd(b)
	valueField := fieldPtr(baseCtor.Param(0), baseOffset, "receiver.value")
	builder.CreateStore(llvm.ConstFloat(double, 0), valueField)
	builder.CreateStore(baseCtor.Param(1), valueField)
	builder.CreateRet(baseCtor.Param(0))

	derivedType := llvm.FunctionType(ptr, []llvm.Type{double, double}, false)
	derivedCtor := llvm.AddFunction(module, "vert013b.StepCounter.constructor", derivedType)
	derivedCtor.SetLinkage(llvm.PrivateLinkage)
	derivedCtor.SetFunctionCallConv(llvm.CCallConv)
	b = llvm.AddBasicBlock(derivedCtor, "entry")
	builder.SetInsertPointAtEnd(b)
	out := builder.CreateAlloca(ptr, "receiver.out")
	builder.CreateStore(null, out)
	status := builder.CreateCall(alloc.functionType, alloc.value, []llvm.Value{shape, out}, "receiver.alloc.status")
	emitVERT010StatusCheck(ctx, builder, module, derivedCtor, status, "receiver.alloc")
	receiver := builder.CreateLoad(ptr, out, "receiver")
	frame, _ := makeFrame(derivedCtor, "constructor.gc")
	callChecked(derivedCtor, "constructor.frame.link", frameLink, []llvm.Value{frame})
	callChecked(derivedCtor, "constructor.root.store", rootStore, []llvm.Value{frame, zero32, receiver})
	callChecked(derivedCtor, "constructor.root.publish", rootPublish, []llvm.Value{frame, llvm.ConstInt(i64, 1, false)})
	callChecked(derivedCtor, "constructor.super.safepoint", safepoint, nil)
	reloadOut := builder.CreateAlloca(ptr, "constructor.receiver.reload.out")
	builder.CreateStore(null, reloadOut)
	callChecked(derivedCtor, "constructor.root.reload", rootReload, []llvm.Value{frame, zero32, reloadOut})
	receiver = builder.CreateLoad(ptr, reloadOut, "constructor.receiver.reloaded")
	receiver = builder.CreateCall(baseType, baseCtor, []llvm.Value{receiver, derivedCtor.Param(0)}, "super.receiver")
	stepField := fieldPtr(receiver, stepOffset, "receiver.step")
	builder.CreateStore(llvm.ConstFloat(double, 1), stepField)
	builder.CreateStore(derivedCtor.Param(1), stepField)
	callChecked(derivedCtor, "constructor.frame.unlink", frameUnlink, []llvm.Value{frame})
	builder.CreateRet(receiver)

	methodType := llvm.FunctionType(double, []llvm.Type{ptr}, false)
	method := llvm.AddFunction(module, "vert013b.StepCounter.increment", methodType)
	method.SetLinkage(llvm.PrivateLinkage)
	method.SetFunctionCallConv(llvm.CCallConv)
	b = llvm.AddBasicBlock(method, "entry")
	builder.SetInsertPointAtEnd(b)
	valueField = fieldPtr(method.Param(0), baseOffset, "receiver.value")
	stepField = fieldPtr(method.Param(0), stepOffset, "receiver.step")
	next := builder.CreateFAdd(builder.CreateLoad(double, valueField, "value"), builder.CreateLoad(double, stepField, "step"), "next")
	builder.CreateStore(next, valueField)
	builder.CreateRet(next)

	entryType := llvm.FunctionType(double, []llvm.Type{double, double}, false)
	entry := llvm.AddFunction(module, "derivedCounter", entryType)
	entry.SetFunctionCallConv(llvm.CCallConv)
	b = llvm.AddBasicBlock(entry, "entry")
	builder.SetInsertPointAtEnd(b)
	frame, _ = makeFrame(entry, "entry.gc")
	callChecked(entry, "entry.frame.link", frameLink, []llvm.Value{frame})
	callChecked(entry, "entry.root.clear", rootClear, []llvm.Value{frame, zero32})
	callChecked(entry, "entry.root.publish.empty", rootPublish, []llvm.Value{frame, llvm.ConstInt(i64, 0, false)})
	receiver = builder.CreateCall(derivedType, derivedCtor, []llvm.Value{entry.Param(0), entry.Param(1)}, "receiver.constructed")
	reloadOut = builder.CreateAlloca(ptr, "entry.receiver.reload.out")
	builder.CreateStore(null, reloadOut)
	rootedCall := func(label string) llvm.Value {
		callChecked(entry, label+".root.store", rootStore, []llvm.Value{frame, zero32, receiver})
		callChecked(entry, label+".root.publish", rootPublish, []llvm.Value{frame, llvm.ConstInt(i64, 1, false)})
		callChecked(entry, label+".safepoint", safepoint, nil)
		callChecked(entry, label+".root.reload", rootReload, []llvm.Value{frame, zero32, reloadOut})
		receiver = builder.CreateLoad(ptr, reloadOut, label+".receiver.reloaded")
		return builder.CreateCall(methodType, method, []llvm.Value{receiver}, label+".result")
	}
	first, second := rootedCall("first.method"), rootedCall("second.method")
	result := builder.CreateFAdd(first, second, "result")
	callChecked(entry, "entry.frame.unlink", frameUnlink, []llvm.Value{frame})
	builder.CreateRet(result)
	return nil
}

func emitVERT013bShape(ctx llvm.Context, module llvm.Module, layout bingo.ObjectLayoutContract, ptr, i8, i16, i32, i64 llvm.Type, null llvm.Value) (llvm.Value, error) {
	if len(layout.Properties) != 2 || len(layout.TraceOffsets) != 0 {
		return llvm.Value{}, fmt.Errorf("VERT-013b derived layout has unexpected properties or trace offsets")
	}
	traceType := ctx.StructType([]llvm.Type{i32, i32, i64, i32, i32, ptr, ptr}, false)
	trace := llvm.AddGlobal(module, traceType, "vert013b.derived.trace")
	trace.SetLinkage(llvm.PrivateLinkage)
	trace.SetGlobalConstant(true)
	trace.SetInitializer(llvm.ConstNamedStruct(traceType, []llvm.Value{llvm.ConstInt(i32, 1, false), llvm.ConstInt(i32, 0, false), llvm.ConstInt(i64, uint64(layout.ObjectSize), false), llvm.ConstInt(i32, 0, false), llvm.ConstInt(i32, 0, false), null, null}))
	propertyType := ctx.StructType([]llvm.Type{ptr, i8, i8, i16, i32, i32, i32, i32, ptr}, false)
	properties := make([]llvm.Value, len(layout.Properties))
	for index, mapping := range layout.Properties {
		keyBytes := llvm.ConstString(mapping.Key, true)
		key := llvm.AddGlobal(module, keyBytes.Type(), fmt.Sprintf("vert013b.derived.key.%d", index))
		key.SetLinkage(llvm.PrivateLinkage)
		key.SetGlobalConstant(true)
		key.SetInitializer(keyBytes)
		properties[index] = llvm.ConstNamedStruct(propertyType, []llvm.Value{key, llvm.ConstInt(i8, 1, false), llvm.ConstInt(i8, 0, false), llvm.ConstInt(i16, 0, false), llvm.ConstInt(i32, uint64(mapping.FieldOffset), false), llvm.ConstInt(i32, uint64(^uint32(0)), false), llvm.ConstInt(i32, 0, false), llvm.ConstInt(i32, uint64(index), false), null})
	}
	propertyArrayType := llvm.ArrayType(propertyType, len(properties))
	propertyArray := llvm.AddGlobal(module, propertyArrayType, "vert013b.derived.properties")
	propertyArray.SetLinkage(llvm.PrivateLinkage)
	propertyArray.SetGlobalConstant(true)
	propertyArray.SetInitializer(llvm.ConstArray(propertyType, properties))
	shapeType := ctx.StructType([]llvm.Type{i32, i32, i64, i64, i32, i32, ptr, ptr}, false)
	shape := llvm.AddGlobal(module, shapeType, "vert013b.derived.shape")
	shape.SetLinkage(llvm.PrivateLinkage)
	shape.SetGlobalConstant(true)
	shape.SetInitializer(llvm.ConstNamedStruct(shapeType, []llvm.Value{llvm.ConstInt(i32, 1, false), llvm.ConstInt(i32, 0, false), llvm.ConstInt(i64, uint64(layout.ObjectSize), false), llvm.ConstInt(i64, uint64(layout.ObjectAlign), false), llvm.ConstInt(i32, uint64(len(properties)), false), llvm.ConstInt(i32, 0, false), propertyArray, trace}))
	return shape, nil
}
