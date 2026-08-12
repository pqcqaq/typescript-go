//go:build llvm20 && cgo && linux

package llvmbackend

import (
	"fmt"

	"github.com/microsoft/typescript-go/internal/bingo"
	llvm "tinygo.org/x/go-llvm"
)

func emitVERT012Object(targetMachine llvm.TargetMachine, manifest ToolchainManifest, bound bingo.VERT012BoundMIR) (VERT012Emission, error) {
	ctx := llvm.NewContext()
	defer ctx.Dispose()
	module := ctx.NewModule("ts2bin-vert-012")
	defer module.Dispose()
	module.SetTarget(manifest.TargetTriple)
	module.SetDataLayout(manifest.DataLayout.LayoutString)
	builder := ctx.NewBuilder()
	defer builder.Dispose()
	if err := emitVERT012Module(ctx, builder, module, bound); err != nil {
		return VERT012Emission{}, err
	}
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		return VERT012Emission{}, fmt.Errorf("verify VERT-012 LLVM module: %w", err)
	}
	llvmIR := []byte(module.String())
	buffer, err := targetMachine.EmitToMemoryBuffer(module, llvm.ObjectFile)
	if err != nil {
		return VERT012Emission{}, fmt.Errorf("emit VERT-012 object: %w", err)
	}
	defer buffer.Dispose()
	return newVERT012Emission(bound, manifest, llvmIR, buffer.Bytes())
}

func emitVERT012Module(ctx llvm.Context, builder llvm.Builder, module llvm.Module, bound bingo.VERT012BoundMIR) error {
	mir := bound.MIR
	if len(mir.Layouts) != 2 || len(mir.Functions) != 2 {
		return fmt.Errorf("unsupported VERT-012 MIR shape")
	}
	i8, i16, i32, i64 := ctx.Int8Type(), ctx.Int16Type(), ctx.Int32Type(), ctx.Int64Type()
	ptr, double := llvm.PointerType(i8, 0), ctx.DoubleType()
	null := llvm.ConstNull(ptr)
	cellLayout, environmentLayout := mir.Layouts[0].Contract, mir.Layouts[1].Contract
	cellOffset, environmentOffset := cellLayout.Properties[0].FieldOffset, environmentLayout.Properties[0].FieldOffset
	cellShape, err := emitVERT012Shape(ctx, module, "cell", cellLayout, ptr, i8, i16, i32, i64, null)
	if err != nil {
		return err
	}
	environmentShape, err := emitVERT012Shape(ctx, module, "environment", environmentLayout, ptr, i8, i16, i32, i64, null)
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
			return vert010RuntimeFunction{}, fmt.Errorf("VERT-012 runtime capability %q is unbound", logical)
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
	barrier, err := declare("rt.gc.write_barrier", []llvm.Type{ptr, i32, ptr})
	if err != nil {
		return err
	}

	closureType := llvm.FunctionType(double, []llvm.Type{ptr}, false)
	closureCode := llvm.AddFunction(module, "vert012.closure.increment", closureType)
	closureCode.SetLinkage(llvm.PrivateLinkage)
	closureCode.SetFunctionCallConv(llvm.CCallConv)
	closureEntry := llvm.AddBasicBlock(closureCode, "entry")
	builder.SetInsertPointAtEnd(closureEntry)
	cellAddress := builder.CreateInBoundsGEP(i8, closureCode.Param(0), []llvm.Value{llvm.ConstInt(i64, uint64(environmentOffset), false)}, "environment.cell")
	cell := builder.CreateLoad(ptr, cellAddress, "cell")
	valueAddress := builder.CreateInBoundsGEP(i8, cell, []llvm.Value{llvm.ConstInt(i64, uint64(cellOffset), false)}, "cell.value")
	value := builder.CreateLoad(double, valueAddress, "value")
	next := builder.CreateFAdd(value, llvm.ConstFloat(double, 1), "next")
	builder.CreateStore(next, valueAddress)
	builder.CreateRet(next)

	entryType := llvm.FunctionType(double, []llvm.Type{double}, false)
	entryFunction := llvm.AddFunction(module, "closureCounter", entryType)
	entryFunction.SetFunctionCallConv(llvm.CCallConv)
	entry := llvm.AddBasicBlock(entryFunction, "entry")
	builder.SetInsertPointAtEnd(entry)
	callChecked := func(name string, runtime vert010RuntimeFunction, args []llvm.Value) {
		status := builder.CreateCall(runtime.functionType, runtime.value, args, name+".status")
		emitVERT010StatusCheck(ctx, builder, module, entryFunction, status, name)
	}
	slotsType := llvm.ArrayType(ptr, 2)
	slots := builder.CreateAlloca(slotsType, "gc.slots")
	zero32, one32 := llvm.ConstInt(i32, 0, false), llvm.ConstInt(i32, 1, false)
	slot0 := builder.CreateInBoundsGEP(slotsType, slots, []llvm.Value{zero32, zero32}, "gc.slot.cell")
	slot1 := builder.CreateInBoundsGEP(slotsType, slots, []llvm.Value{zero32, one32}, "gc.slot.environment")
	builder.CreateStore(null, slot0)
	builder.CreateStore(null, slot1)
	frameType := ctx.StructType([]llvm.Type{ptr, ptr, i32, i32, i64}, false)
	frame := builder.CreateAlloca(frameType, "gc.frame")
	builder.CreateStore(null, builder.CreateStructGEP(frameType, frame, 0, "frame.previous"))
	builder.CreateStore(slot0, builder.CreateStructGEP(frameType, frame, 1, "frame.slots"))
	builder.CreateStore(llvm.ConstInt(i32, 2, false), builder.CreateStructGEP(frameType, frame, 2, "frame.slot_count"))
	builder.CreateStore(zero32, builder.CreateStructGEP(frameType, frame, 3, "frame.reserved"))
	builder.CreateStore(llvm.ConstInt(i64, 0, false), builder.CreateStructGEP(frameType, frame, 4, "frame.active_bits"))
	cellOut, environmentOut := builder.CreateAlloca(ptr, "cell.out"), builder.CreateAlloca(ptr, "environment.out")
	builder.CreateStore(null, cellOut)
	builder.CreateStore(null, environmentOut)
	callChecked("frame.link", frameLink, []llvm.Value{frame})
	callChecked("root.clear.cell", rootClear, []llvm.Value{frame, zero32})
	callChecked("root.clear.environment", rootClear, []llvm.Value{frame, one32})
	callChecked("root.publish.empty", rootPublish, []llvm.Value{frame, llvm.ConstInt(i64, 0, false)})
	callChecked("safepoint.cell.allocation", safepoint, nil)
	callChecked("cell.alloc", alloc, []llvm.Value{cellShape, cellOut})
	cell = builder.CreateLoad(ptr, cellOut, "cell.allocated")
	valueAddress = builder.CreateInBoundsGEP(i8, cell, []llvm.Value{llvm.ConstInt(i64, uint64(cellOffset), false)}, "cell.value.init")
	builder.CreateStore(entryFunction.Param(0), valueAddress)
	callChecked("root.store.cell", rootStore, []llvm.Value{frame, zero32, cell})
	callChecked("root.clear.environment.again", rootClear, []llvm.Value{frame, one32})
	callChecked("root.publish.cell", rootPublish, []llvm.Value{frame, llvm.ConstInt(i64, 1, false)})
	callChecked("safepoint.environment.allocation", safepoint, nil)
	callChecked("environment.alloc", alloc, []llvm.Value{environmentShape, environmentOut})
	callChecked("root.reload.cell", rootReload, []llvm.Value{frame, zero32, cellOut})
	cell = builder.CreateLoad(ptr, cellOut, "cell.reloaded")
	environment := builder.CreateLoad(ptr, environmentOut, "environment.allocated")
	cellAddress = builder.CreateInBoundsGEP(i8, environment, []llvm.Value{llvm.ConstInt(i64, uint64(environmentOffset), false)}, "environment.cell.init")
	builder.CreateStore(cell, cellAddress)
	callChecked("environment.cell.barrier", barrier, []llvm.Value{environment, llvm.ConstInt(i32, uint64(environmentOffset), false), cell})
	callChecked("root.store.cell.again", rootStore, []llvm.Value{frame, zero32, cell})
	callChecked("root.store.environment", rootStore, []llvm.Value{frame, one32, environment})
	callChecked("root.publish.both", rootPublish, []llvm.Value{frame, llvm.ConstInt(i64, 3, false)})
	callChecked("safepoint.forced", safepoint, nil)
	callChecked("root.reload.cell.forced", rootReload, []llvm.Value{frame, zero32, cellOut})
	callChecked("root.reload.environment.forced", rootReload, []llvm.Value{frame, one32, environmentOut})
	environment = builder.CreateLoad(ptr, environmentOut, "environment.reloaded")
	closureAggregate := llvm.Undef(ctx.StructType([]llvm.Type{ptr, ptr}, false))
	closureAggregate = builder.CreateInsertValue(closureAggregate, closureCode, 0, "closure.code")
	closureAggregate = builder.CreateInsertValue(closureAggregate, environment, 1, "closure.environment")
	code := builder.CreateExtractValue(closureAggregate, 0, "closure.code.extract")
	closureEnvironment := builder.CreateExtractValue(closureAggregate, 1, "closure.environment.extract")
	first := builder.CreateCall(closureType, code, []llvm.Value{closureEnvironment}, "closure.first")
	second := builder.CreateCall(closureType, code, []llvm.Value{closureEnvironment}, "closure.second")
	result := builder.CreateFAdd(first, second, "result")
	callChecked("frame.unlink", frameUnlink, []llvm.Value{frame})
	builder.CreateRet(result)
	return nil
}

func emitVERT012Shape(ctx llvm.Context, module llvm.Module, name string, layout bingo.ObjectLayoutContract, ptr, i8, i16, i32, i64 llvm.Type, null llvm.Value) (llvm.Value, error) {
	if len(layout.Properties) != 1 {
		return llvm.Value{}, fmt.Errorf("VERT-012 %s layout has unexpected properties", name)
	}
	traceType := ctx.StructType([]llvm.Type{i32, i32, i64, i32, i32, ptr, ptr}, false)
	offsets := null
	traceCount := uint64(0)
	if len(layout.TraceOffsets) == 1 {
		offsetArrayType := llvm.ArrayType(i32, 1)
		offsetArray := llvm.AddGlobal(module, offsetArrayType, "vert012."+name+".trace.offsets")
		offsetArray.SetLinkage(llvm.PrivateLinkage)
		offsetArray.SetGlobalConstant(true)
		offsetArray.SetInitializer(llvm.ConstArray(i32, []llvm.Value{llvm.ConstInt(i32, uint64(layout.TraceOffsets[0]), false)}))
		offsets = offsetArray
		traceCount = 1
	}
	trace := llvm.AddGlobal(module, traceType, "vert012."+name+".trace")
	trace.SetLinkage(llvm.PrivateLinkage)
	trace.SetGlobalConstant(true)
	trace.SetInitializer(llvm.ConstNamedStruct(traceType, []llvm.Value{llvm.ConstInt(i32, 1, false), llvm.ConstInt(i32, 0, false), llvm.ConstInt(i64, uint64(layout.ObjectSize), false), llvm.ConstInt(i32, traceCount, false), llvm.ConstInt(i32, 0, false), offsets, null}))
	keyBytes := llvm.ConstString(layout.Properties[0].Key, true)
	key := llvm.AddGlobal(module, keyBytes.Type(), "vert012."+name+".key")
	key.SetLinkage(llvm.PrivateLinkage)
	key.SetGlobalConstant(true)
	key.SetInitializer(keyBytes)
	propertyType := ctx.StructType([]llvm.Type{ptr, i8, i8, i16, i32, i32, i32, i32, ptr}, false)
	property := llvm.AddGlobal(module, propertyType, "vert012."+name+".property")
	property.SetLinkage(llvm.PrivateLinkage)
	property.SetGlobalConstant(true)
	property.SetInitializer(llvm.ConstNamedStruct(propertyType, []llvm.Value{key, llvm.ConstInt(i8, 1, false), llvm.ConstInt(i8, 0, false), llvm.ConstInt(i16, 0, false), llvm.ConstInt(i32, uint64(layout.Properties[0].FieldOffset), false), llvm.ConstInt(i32, uint64(^uint32(0)), false), llvm.ConstInt(i32, 0, false), llvm.ConstInt(i32, 0, false), null}))
	shapeType := ctx.StructType([]llvm.Type{i32, i32, i64, i64, i32, i32, ptr, ptr}, false)
	shape := llvm.AddGlobal(module, shapeType, "vert012."+name+".shape")
	shape.SetLinkage(llvm.PrivateLinkage)
	shape.SetGlobalConstant(true)
	shape.SetInitializer(llvm.ConstNamedStruct(shapeType, []llvm.Value{llvm.ConstInt(i32, 1, false), llvm.ConstInt(i32, 0, false), llvm.ConstInt(i64, uint64(layout.ObjectSize), false), llvm.ConstInt(i64, uint64(layout.ObjectAlign), false), llvm.ConstInt(i32, 1, false), llvm.ConstInt(i32, 0, false), property, trace}))
	return shape, nil
}
