//go:build llvm20 && cgo && linux

package llvmbackend

import (
	"fmt"

	"github.com/microsoft/typescript-go/internal/bingo"
	llvm "tinygo.org/x/go-llvm"
)

func emitVERT013aObject(targetMachine llvm.TargetMachine, manifest ToolchainManifest, bound bingo.VERT013aBoundMIR) (VERT013aEmission, error) {
	ctx := llvm.NewContext()
	defer ctx.Dispose()
	module := ctx.NewModule("ts2bin-vert-013a")
	defer module.Dispose()
	module.SetTarget(manifest.TargetTriple)
	module.SetDataLayout(manifest.DataLayout.LayoutString)
	builder := ctx.NewBuilder()
	defer builder.Dispose()
	if err := emitVERT013aModule(ctx, builder, module, bound); err != nil {
		return VERT013aEmission{}, err
	}
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		return VERT013aEmission{}, fmt.Errorf("verify VERT-013a LLVM module: %w", err)
	}
	llvmIR := []byte(module.String())
	buffer, err := targetMachine.EmitToMemoryBuffer(module, llvm.ObjectFile)
	if err != nil {
		return VERT013aEmission{}, fmt.Errorf("emit VERT-013a object: %w", err)
	}
	defer buffer.Dispose()
	return newVERT013aEmission(bound, manifest, llvmIR, buffer.Bytes())
}

func emitVERT013aModule(ctx llvm.Context, builder llvm.Builder, module llvm.Module, bound bingo.VERT013aBoundMIR) error {
	mir := bound.MIR
	if len(mir.Functions) != 3 || len(mir.Layout.Properties) != 1 {
		return fmt.Errorf("unsupported VERT-013a MIR shape")
	}
	i8, i32, i64 := ctx.Int8Type(), ctx.Int32Type(), ctx.Int64Type()
	ptr, double := llvm.PointerType(i8, 0), ctx.DoubleType()
	null := llvm.ConstNull(ptr)
	offset := mir.Layout.Properties[0].FieldOffset
	shape, err := emitVERT012Shape(ctx, module, "class.instance", mir.Layout, ptr, i8, ctx.Int16Type(), i32, i64, null)
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
			return vert010RuntimeFunction{}, fmt.Errorf("VERT-013a runtime capability %q is unbound", logical)
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

	constructorType := llvm.FunctionType(ptr, []llvm.Type{double}, false)
	constructor := llvm.AddFunction(module, "vert013a.Counter.constructor", constructorType)
	constructor.SetLinkage(llvm.PrivateLinkage)
	constructor.SetFunctionCallConv(llvm.CCallConv)
	constructorEntry := llvm.AddBasicBlock(constructor, "entry")
	builder.SetInsertPointAtEnd(constructorEntry)
	out := builder.CreateAlloca(ptr, "receiver.out")
	builder.CreateStore(null, out)
	status := builder.CreateCall(alloc.functionType, alloc.value, []llvm.Value{shape, out}, "receiver.alloc.status")
	emitVERT010StatusCheck(ctx, builder, module, constructor, status, "receiver.alloc")
	receiver := builder.CreateLoad(ptr, out, "receiver")
	field := builder.CreateInBoundsGEP(i8, receiver, []llvm.Value{llvm.ConstInt(i64, uint64(offset), false)}, "receiver.value")
	builder.CreateStore(llvm.ConstFloat(double, 0), field)
	builder.CreateStore(constructor.Param(0), field)
	builder.CreateRet(receiver)

	methodType := llvm.FunctionType(double, []llvm.Type{ptr}, false)
	method := llvm.AddFunction(module, "vert013a.Counter.increment", methodType)
	method.SetLinkage(llvm.PrivateLinkage)
	method.SetFunctionCallConv(llvm.CCallConv)
	methodEntry := llvm.AddBasicBlock(method, "entry")
	builder.SetInsertPointAtEnd(methodEntry)
	field = builder.CreateInBoundsGEP(i8, method.Param(0), []llvm.Value{llvm.ConstInt(i64, uint64(offset), false)}, "receiver.value")
	current := builder.CreateLoad(double, field, "current")
	next := builder.CreateFAdd(current, llvm.ConstFloat(double, 1), "next")
	builder.CreateStore(next, field)
	builder.CreateRet(next)

	entryType := llvm.FunctionType(double, []llvm.Type{double}, false)
	entryFunction := llvm.AddFunction(module, "classCounter", entryType)
	entryFunction.SetFunctionCallConv(llvm.CCallConv)
	entry := llvm.AddBasicBlock(entryFunction, "entry")
	builder.SetInsertPointAtEnd(entry)
	callChecked := func(name string, runtime vert010RuntimeFunction, args []llvm.Value) {
		status := builder.CreateCall(runtime.functionType, runtime.value, args, name+".status")
		emitVERT010StatusCheck(ctx, builder, module, entryFunction, status, name)
	}
	zero32 := llvm.ConstInt(i32, 0, false)
	slotsType := llvm.ArrayType(ptr, 1)
	slots := builder.CreateAlloca(slotsType, "gc.slots")
	slot := builder.CreateInBoundsGEP(slotsType, slots, []llvm.Value{zero32, zero32}, "gc.slot.receiver")
	builder.CreateStore(null, slot)
	frameType := ctx.StructType([]llvm.Type{ptr, ptr, i32, i32, i64}, false)
	frame := builder.CreateAlloca(frameType, "gc.frame")
	builder.CreateStore(null, builder.CreateStructGEP(frameType, frame, 0, "frame.previous"))
	builder.CreateStore(slot, builder.CreateStructGEP(frameType, frame, 1, "frame.slots"))
	builder.CreateStore(llvm.ConstInt(i32, 1, false), builder.CreateStructGEP(frameType, frame, 2, "frame.slot_count"))
	builder.CreateStore(zero32, builder.CreateStructGEP(frameType, frame, 3, "frame.reserved"))
	builder.CreateStore(llvm.ConstInt(i64, 0, false), builder.CreateStructGEP(frameType, frame, 4, "frame.active_bits"))
	receiverOut := builder.CreateAlloca(ptr, "receiver.reload.out")
	builder.CreateStore(null, receiverOut)
	callChecked("frame.link", frameLink, []llvm.Value{frame})
	callChecked("root.clear", rootClear, []llvm.Value{frame, zero32})
	callChecked("root.publish.empty", rootPublish, []llvm.Value{frame, llvm.ConstInt(i64, 0, false)})
	receiver = builder.CreateCall(constructorType, constructor, []llvm.Value{entryFunction.Param(0)}, "receiver.constructed")
	callChecked("root.store.receiver", rootStore, []llvm.Value{frame, zero32, receiver})
	callChecked("root.publish.receiver", rootPublish, []llvm.Value{frame, llvm.ConstInt(i64, 1, false)})
	callChecked("safepoint.forced", safepoint, nil)
	callChecked("root.reload.receiver", rootReload, []llvm.Value{frame, zero32, receiverOut})
	receiver = builder.CreateLoad(ptr, receiverOut, "receiver.reloaded")
	first := builder.CreateCall(methodType, method, []llvm.Value{receiver}, "method.first")
	second := builder.CreateCall(methodType, method, []llvm.Value{receiver}, "method.second")
	result := builder.CreateFAdd(first, second, "result")
	callChecked("frame.unlink", frameUnlink, []llvm.Value{frame})
	builder.CreateRet(result)
	return nil
}
