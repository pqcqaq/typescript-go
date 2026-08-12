//go:build llvm20 && cgo && linux

package llvmbackend

import (
	"fmt"

	"github.com/microsoft/typescript-go/internal/bingo"
	llvm "tinygo.org/x/go-llvm"
)

func emitVERT011Object(targetMachine llvm.TargetMachine, manifest ToolchainManifest, bound bingo.VERT011BoundMIR) (VERT011Emission, error) {
	ctx := llvm.NewContext()
	defer ctx.Dispose()
	module := ctx.NewModule("ts2bin-vert-011")
	defer module.Dispose()
	module.SetTarget(manifest.TargetTriple)
	module.SetDataLayout(manifest.DataLayout.LayoutString)
	builder := ctx.NewBuilder()
	defer builder.Dispose()

	if err := emitVERT011Module(ctx, builder, module, bound); err != nil {
		return VERT011Emission{}, err
	}
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		return VERT011Emission{}, fmt.Errorf("verify VERT-011 LLVM module: %w", err)
	}
	llvmIR := []byte(module.String())
	buffer, err := targetMachine.EmitToMemoryBuffer(module, llvm.ObjectFile)
	if err != nil {
		return VERT011Emission{}, fmt.Errorf("emit VERT-011 object: %w", err)
	}
	defer buffer.Dispose()
	return newVERT011Emission(bound, manifest, llvmIR, buffer.Bytes())
}

func emitVERT011Module(ctx llvm.Context, builder llvm.Builder, module llvm.Module, bound bingo.VERT011BoundMIR) error {
	mir := bound.MIR
	if len(mir.Layout.Fields) != 2 || len(mir.Function.Blocks) != 4 || mir.Accessors.GetterSignature != bingo.VERT011GetterSignature || mir.Accessors.SetterSignature != bingo.VERT011SetterSignature {
		return fmt.Errorf("unsupported VERT-011 MIR shape")
	}
	i8, i16, i32, i64 := ctx.Int8Type(), ctx.Int16Type(), ctx.Int32Type(), ctx.Int64Type()
	ptr := llvm.PointerType(i8, 0)
	double, void := ctx.DoubleType(), ctx.VoidType()
	null := llvm.ConstNull(ptr)
	backingOffset := mir.Layout.Fields[0].FieldOffset

	shape, err := emitVERT011Descriptors(ctx, module, mir, ptr, i8, i16, i32, i64, null)
	if err != nil {
		return err
	}
	getter, getterType := emitVERT011Getter(ctx, builder, module, mir.Accessors.GetterSymbolKey, backingOffset, ptr, i8, double, void)
	setter, setterType := emitVERT011Setter(ctx, builder, module, mir.Accessors.SetterSymbolKey, backingOffset, ptr, i8, double, void)

	bindings := make(map[bingo.RuntimeCapabilityID]string, len(bound.Closure.Bindings))
	for _, binding := range bound.Closure.Bindings {
		bindings[binding.LogicalName] = binding.SymbolName
	}
	declare := func(logical bingo.RuntimeCapabilityID, args []llvm.Type) (vert010RuntimeFunction, error) {
		name := bindings[logical]
		if name == "" {
			return vert010RuntimeFunction{}, fmt.Errorf("VERT-011 runtime capability %q is unbound", logical)
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

	functionType := llvm.FunctionType(double, []llvm.Type{double, i8}, false)
	function := llvm.AddFunction(module, mir.Function.Name, functionType)
	function.SetFunctionCallConv(llvm.CCallConv)
	function.Param(0).SetName("value.payload")
	function.Param(1).SetName("value.tag")
	entry := llvm.AddBasicBlock(function, "entry")
	assign := llvm.AddBasicBlock(function, "assign")
	skip := llvm.AddBasicBlock(function, "skip")
	merge := llvm.AddBasicBlock(function, "merge")
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
	payloadField := builder.CreateInBoundsGEP(i8, object, []llvm.Value{llvm.ConstInt(i64, uint64(backingOffset), false)}, "backing.payload")
	tagField := builder.CreateInBoundsGEP(i8, object, []llvm.Value{llvm.ConstInt(i64, uint64(backingOffset+8), false)}, "backing.tag")
	builder.CreateStore(function.Param(0), payloadField)
	builder.CreateStore(function.Param(1), tagField)
	callChecked("root.store", rootStore, []llvm.Value{frame, zero32, object})
	callChecked("root.publish.live", rootPublish, []llvm.Value{frame, llvm.ConstInt(i64, 1, false)})
	callChecked("safepoint.forced", safepoint, nil)
	callChecked("root.reload", rootReload, []llvm.Value{frame, zero32, outObject})
	reloaded := builder.CreateLoad(ptr, outObject, "object.reloaded")
	getterPayload := builder.CreateAlloca(double, "getter.payload")
	getterTag := builder.CreateAlloca(i8, "getter.tag")
	builder.CreateCall(getterType, getter, []llvm.Value{reloaded, getterPayload, getterTag}, "")
	loadedPayload := builder.CreateLoad(double, getterPayload, "loaded.payload")
	loadedTag := builder.CreateLoad(i8, getterTag, "loaded.tag")
	isNullish := builder.CreateICmp(llvm.IntNE, loadedTag, llvm.ConstInt(i8, 0, false), "loaded.is_nullish")
	builder.CreateCondBr(isNullish, assign, skip)

	builder.SetInsertPointAtEnd(assign)
	one := llvm.ConstFloat(double, 1)
	builder.CreateCall(setterType, setter, []llvm.Value{reloaded, one}, "")
	builder.CreateBr(merge)

	builder.SetInsertPointAtEnd(skip)
	builder.CreateBr(merge)

	builder.SetInsertPointAtEnd(merge)
	result := builder.CreatePHI(double, "result")
	result.AddIncoming([]llvm.Value{one, loadedPayload}, []llvm.BasicBlock{assign, skip})
	callChecked("frame.unlink", frameUnlink, []llvm.Value{frame})
	builder.CreateRet(result)
	return nil
}

func emitVERT011Getter(ctx llvm.Context, builder llvm.Builder, module llvm.Module, symbol string, offset uint32, ptr, i8, double, void llvm.Type) (llvm.Value, llvm.Type) {
	type_ := llvm.FunctionType(void, []llvm.Type{ptr, llvm.PointerType(double, 0), llvm.PointerType(i8, 0)}, false)
	function := llvm.AddFunction(module, "vert011.getter."+symbol, type_)
	function.SetLinkage(llvm.PrivateLinkage)
	function.SetFunctionCallConv(llvm.CCallConv)
	block := llvm.AddBasicBlock(function, "entry")
	builder.SetInsertPointAtEnd(block)
	payload := builder.CreateInBoundsGEP(i8, function.Param(0), []llvm.Value{llvm.ConstInt(ctx.Int64Type(), uint64(offset), false)}, "backing.payload")
	tag := builder.CreateInBoundsGEP(i8, function.Param(0), []llvm.Value{llvm.ConstInt(ctx.Int64Type(), uint64(offset+8), false)}, "backing.tag")
	builder.CreateStore(builder.CreateLoad(double, payload, "payload"), function.Param(1))
	builder.CreateStore(builder.CreateLoad(i8, tag, "tag"), function.Param(2))
	builder.CreateRetVoid()
	return function, type_
}

func emitVERT011Setter(ctx llvm.Context, builder llvm.Builder, module llvm.Module, symbol string, offset uint32, ptr, i8, double, void llvm.Type) (llvm.Value, llvm.Type) {
	type_ := llvm.FunctionType(void, []llvm.Type{ptr, double}, false)
	function := llvm.AddFunction(module, "vert011.setter."+symbol, type_)
	function.SetLinkage(llvm.PrivateLinkage)
	function.SetFunctionCallConv(llvm.CCallConv)
	block := llvm.AddBasicBlock(function, "entry")
	builder.SetInsertPointAtEnd(block)
	payload := builder.CreateInBoundsGEP(i8, function.Param(0), []llvm.Value{llvm.ConstInt(ctx.Int64Type(), uint64(offset), false)}, "backing.payload")
	tag := builder.CreateInBoundsGEP(i8, function.Param(0), []llvm.Value{llvm.ConstInt(ctx.Int64Type(), uint64(offset+8), false)}, "backing.tag")
	builder.CreateStore(function.Param(1), payload)
	builder.CreateStore(llvm.ConstInt(i8, 0, false), tag)
	builder.CreateRetVoid()
	return function, type_
}

func emitVERT011Descriptors(ctx llvm.Context, module llvm.Module, mir bingo.VERT011MIRModule, ptr, i8, i16, i32, i64 llvm.Type, null llvm.Value) (llvm.Value, error) {
	traceType := ctx.StructType([]llvm.Type{i32, i32, i64, i32, i32, ptr, ptr}, false)
	trace := llvm.AddGlobal(module, traceType, "vert011.trace")
	trace.SetLinkage(llvm.PrivateLinkage)
	trace.SetGlobalConstant(true)
	trace.SetInitializer(llvm.ConstNamedStruct(traceType, []llvm.Value{llvm.ConstInt(i32, 1, false), llvm.ConstInt(i32, 0, false), llvm.ConstInt(i64, uint64(mir.Layout.ObjectSize), false), llvm.ConstInt(i32, 0, false), llvm.ConstInt(i32, 0, false), null, null}))
	propertyType := ctx.StructType([]llvm.Type{ptr, i8, i8, i16, i32, i32, i32, i32, ptr}, false)
	properties := make([]llvm.Value, 2)
	for index, field := range mir.Layout.Fields {
		keyBytes := llvm.ConstString(field.PropertyKey, true)
		key := llvm.AddGlobal(module, keyBytes.Type(), fmt.Sprintf("vert011.key.%d", index))
		key.SetLinkage(llvm.PrivateLinkage)
		key.SetGlobalConstant(true)
		key.SetInitializer(keyBytes)
		kind := uint64(1)
		if field.Kind == bingo.ObjectPropertyAccessor {
			kind = 2
		}
		properties[index] = llvm.ConstNamedStruct(propertyType, []llvm.Value{key, llvm.ConstInt(i8, kind, false), llvm.ConstInt(i8, 0, false), llvm.ConstInt(i16, 0, false), llvm.ConstInt(i32, uint64(field.FieldOffset), false), llvm.ConstInt(i32, uint64(^uint32(0)), false), llvm.ConstInt(i32, 0, false), llvm.ConstInt(i32, uint64(index), false), null})
	}
	propertyArrayType := llvm.ArrayType(propertyType, len(properties))
	propertyArray := llvm.AddGlobal(module, propertyArrayType, "vert011.properties")
	propertyArray.SetLinkage(llvm.PrivateLinkage)
	propertyArray.SetGlobalConstant(true)
	propertyArray.SetInitializer(llvm.ConstArray(propertyType, properties))
	shapeType := ctx.StructType([]llvm.Type{i32, i32, i64, i64, i32, i32, ptr, ptr}, false)
	shape := llvm.AddGlobal(module, shapeType, "vert011.shape")
	shape.SetLinkage(llvm.PrivateLinkage)
	shape.SetGlobalConstant(true)
	shape.SetInitializer(llvm.ConstNamedStruct(shapeType, []llvm.Value{llvm.ConstInt(i32, 1, false), llvm.ConstInt(i32, 0, false), llvm.ConstInt(i64, uint64(mir.Layout.ObjectSize), false), llvm.ConstInt(i64, uint64(mir.Layout.ObjectAlign), false), llvm.ConstInt(i32, 2, false), llvm.ConstInt(i32, 0, false), propertyArray, trace}))
	return shape, nil
}
