//go:build llvm20 && cgo && linux

package llvmbackend

import (
	"fmt"

	llvm "tinygo.org/x/go-llvm"
)

func emitCheckedObjectCastObject(targetMachine llvm.TargetMachine, manifest ToolchainManifest, plan CheckedObjectCastBackendPlan) (CheckedObjectCastEmission, error) {
	ctx := llvm.NewContext()
	defer ctx.Dispose()
	module := ctx.NewModule("ts2bin-checked-object-cast")
	defer module.Dispose()
	module.SetTarget(manifest.TargetTriple)
	module.SetDataLayout(manifest.DataLayout.LayoutString)
	builder := ctx.NewBuilder()
	defer builder.Dispose()
	if err := emitCheckedObjectCastFunction(ctx, builder, module, plan); err != nil {
		return CheckedObjectCastEmission{}, err
	}
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		return CheckedObjectCastEmission{}, fmt.Errorf("verify checked object cast LLVM module: %w", err)
	}
	ir := []byte(module.String())
	buffer, err := targetMachine.EmitToMemoryBuffer(module, llvm.ObjectFile)
	if err != nil {
		return CheckedObjectCastEmission{}, fmt.Errorf("emit checked object cast object: %w", err)
	}
	defer buffer.Dispose()
	return newEmission(plan.ContentHash, manifest, ir, buffer.Bytes())
}

func emitCheckedObjectCastFunction(ctx llvm.Context, builder llvm.Builder, module llvm.Module, plan CheckedObjectCastBackendPlan) error {
	if err := VerifyCanonicalCheckedObjectCastBackendPlan(plan); err != nil {
		return err
	}
	i8, i16, i32, i64 := ctx.Int8Type(), ctx.Int16Type(), ctx.Int32Type(), ctx.Int64Type()
	ptr := llvm.PointerType(i8, 0)
	null := llvm.ConstNull(ptr)
	layout := plan.Bound.Cast.TargetLayout

	traceOffsets := null
	if len(layout.TraceOffsets) != 0 {
		values := make([]llvm.Value, len(layout.TraceOffsets))
		for index, offset := range layout.TraceOffsets {
			values[index] = llvm.ConstInt(i32, uint64(offset), false)
		}
		arrayType := llvm.ArrayType(i32, len(values))
		array := llvm.AddGlobal(module, arrayType, "checkedcast.target.trace.offsets")
		array.SetLinkage(llvm.PrivateLinkage)
		array.SetGlobalConstant(true)
		array.SetInitializer(llvm.ConstArray(i32, values))
		traceOffsets = array
	}
	traceType := ctx.StructType([]llvm.Type{i32, i32, i64, i32, i32, ptr, ptr}, false)
	trace := llvm.AddGlobal(module, traceType, "checkedcast.target.trace")
	trace.SetLinkage(llvm.PrivateLinkage)
	trace.SetGlobalConstant(true)
	trace.SetInitializer(llvm.ConstNamedStruct(traceType, []llvm.Value{llvm.ConstInt(i32, 1, false), llvm.ConstInt(i32, 0, false), llvm.ConstInt(i64, uint64(layout.ObjectSize), false), llvm.ConstInt(i32, uint64(len(layout.TraceOffsets)), false), llvm.ConstInt(i32, 0, false), traceOffsets, null}))

	propertyType := ctx.StructType([]llvm.Type{ptr, i8, i8, i16, i32, i32, i32, i32, ptr}, false)
	properties := make([]llvm.Value, len(layout.Properties))
	for index, property := range layout.Properties {
		keyBytes := llvm.ConstString(property.Key, true)
		key := llvm.AddGlobal(module, keyBytes.Type(), fmt.Sprintf("checkedcast.target.key.%d", index))
		key.SetLinkage(llvm.PrivateLinkage)
		key.SetGlobalConstant(true)
		key.SetInitializer(keyBytes)
		presence := uint64(^uint32(0))
		if property.PresenceBit >= 0 {
			presence = uint64(property.PresenceBit)
		}
		properties[index] = llvm.ConstNamedStruct(propertyType, []llvm.Value{key, llvm.ConstInt(i8, 1, false), llvm.ConstInt(i8, 0, false), llvm.ConstInt(i16, 0, false), llvm.ConstInt(i32, uint64(property.FieldOffset), false), llvm.ConstInt(i32, presence, false), llvm.ConstInt(i32, uint64(index), false), llvm.ConstInt(i32, uint64(index), false), null})
	}
	propertyArrayType := llvm.ArrayType(propertyType, len(properties))
	propertyArray := llvm.AddGlobal(module, propertyArrayType, "checkedcast.target.properties")
	propertyArray.SetLinkage(llvm.PrivateLinkage)
	propertyArray.SetGlobalConstant(true)
	propertyArray.SetInitializer(llvm.ConstArray(propertyType, properties))
	shapeType := ctx.StructType([]llvm.Type{i32, i32, i64, i64, i32, i32, ptr, ptr}, false)
	shape := llvm.AddGlobal(module, shapeType, "checkedcast.target.shape")
	shape.SetLinkage(llvm.PrivateLinkage)
	shape.SetGlobalConstant(true)
	shape.SetInitializer(llvm.ConstNamedStruct(shapeType, []llvm.Value{llvm.ConstInt(i32, 1, false), llvm.ConstInt(i32, 0, false), llvm.ConstInt(i64, uint64(layout.ObjectSize), false), llvm.ConstInt(i64, uint64(layout.ObjectAlign), false), llvm.ConstInt(i32, uint64(len(properties)), false), llvm.ConstInt(i32, uint64(layout.PresenceWords), false), propertyArray, trace}))

	runtimeType := llvm.FunctionType(i32, []llvm.Type{ptr, ptr, llvm.PointerType(i8, 0)}, false)
	runtime := llvm.AddFunction(module, plan.FunctionName, runtimeType)
	runtime.SetFunctionCallConv(llvm.CCallConv)
	functionType := llvm.FunctionType(i32, []llvm.Type{ptr, llvm.PointerType(ptr, 0), llvm.PointerType(i8, 0)}, false)
	function := llvm.AddFunction(module, "bingo_checked_object_cast_v1", functionType)
	function.SetFunctionCallConv(llvm.CCallConv)
	entry, invalidArgs, invoke, statusFail, validate, valid, invalid := llvm.AddBasicBlock(function, "entry"), llvm.AddBasicBlock(function, "arguments.invalid"), llvm.AddBasicBlock(function, "runtime.invoke"), llvm.AddBasicBlock(function, "status.fail"), llvm.AddBasicBlock(function, "match.validate"), llvm.AddBasicBlock(function, "match.valid"), llvm.AddBasicBlock(function, "match.invalid")
	builder.SetInsertPointAtEnd(entry)
	badValueOut := builder.CreateICmp(llvm.IntEQ, function.Param(1), llvm.ConstNull(llvm.PointerType(ptr, 0)), "value_out.null")
	badMatchOut := builder.CreateICmp(llvm.IntEQ, function.Param(2), llvm.ConstNull(llvm.PointerType(i8, 0)), "match_out.null")
	builder.CreateCondBr(builder.CreateOr(badValueOut, badMatchOut, "outputs.invalid"), invalidArgs, invoke)
	builder.SetInsertPointAtEnd(invalidArgs)
	builder.CreateRet(llvm.ConstInt(i32, 1, false))
	builder.SetInsertPointAtEnd(invoke)
	builder.CreateStore(null, function.Param(1))
	builder.CreateStore(llvm.ConstInt(i8, 0, false), function.Param(2))
	status := builder.CreateCall(runtimeType, runtime, []llvm.Value{function.Param(0), shape, function.Param(2)}, "shape.status")
	builder.CreateCondBr(builder.CreateICmp(llvm.IntEQ, status, llvm.ConstInt(i32, 0, false), "status.ok"), validate, statusFail)
	builder.SetInsertPointAtEnd(statusFail)
	builder.CreateRet(status)
	builder.SetInsertPointAtEnd(validate)
	match := builder.CreateLoad(i8, function.Param(2), "shape.match")
	builder.CreateCondBr(builder.CreateICmp(llvm.IntULE, match, llvm.ConstInt(i8, 1, false), "match.in_domain"), valid, invalid)
	builder.SetInsertPointAtEnd(invalid)
	builder.CreateStore(llvm.ConstInt(i8, 0, false), function.Param(2))
	builder.CreateRet(llvm.ConstInt(i32, 1, false))
	builder.SetInsertPointAtEnd(valid)
	isMatch := builder.CreateICmp(llvm.IntEQ, match, llvm.ConstInt(i8, 1, false), "match.yes")
	builder.CreateStore(builder.CreateSelect(isMatch, function.Param(0), null, "cast.value"), function.Param(1))
	builder.CreateRet(llvm.ConstInt(i32, 0, false))
	return nil
}
