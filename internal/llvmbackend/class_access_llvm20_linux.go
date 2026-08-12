//go:build llvm20 && cgo && linux

package llvmbackend

import (
	"fmt"

	"github.com/microsoft/typescript-go/internal/bingo"
	llvm "tinygo.org/x/go-llvm"
)

func emitClassAccessObject(targetMachine llvm.TargetMachine, manifest ToolchainManifest, bound bingo.ClassAccessBoundMIR) (ClassAccessEmission, error) {
	ctx := llvm.NewContext()
	defer ctx.Dispose()
	module := ctx.NewModule("ts2bin-classaccess")
	defer module.Dispose()
	module.SetTarget(manifest.TargetTriple)
	module.SetDataLayout(manifest.DataLayout.LayoutString)
	builder := ctx.NewBuilder()
	defer builder.Dispose()
	if err := emitClassAccessModule(ctx, builder, module, bound); err != nil {
		return ClassAccessEmission{}, err
	}
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		return ClassAccessEmission{}, fmt.Errorf("verify OBJ-003b LLVM module: %w", err)
	}
	llvmIR := []byte(module.String())
	buffer, err := targetMachine.EmitToMemoryBuffer(module, llvm.ObjectFile)
	if err != nil {
		return ClassAccessEmission{}, fmt.Errorf("emit OBJ-003b object: %w", err)
	}
	defer buffer.Dispose()
	return newEmission(bound.ContentHash, manifest, llvmIR, buffer.Bytes())
}

func emitClassAccessModule(ctx llvm.Context, builder llvm.Builder, module llvm.Module, bound bingo.ClassAccessBoundMIR) error {
	if len(bound.Layout.MIR.Functions) != 5 || len(bound.Layout.Derived.Properties) != 2 {
		return fmt.Errorf("unsupported OBJ-003b bound MIR shape")
	}
	i8, i32, i64 := ctx.Int8Type(), ctx.Int32Type(), ctx.Int64Type()
	ptr, double := llvm.PointerType(i8, 0), ctx.DoubleType()
	null, zero32 := llvm.ConstNull(ptr), llvm.ConstInt(i32, 0, false)
	secretOffset, valueOffset := bound.Layout.Derived.Properties[0].FieldOffset, bound.Layout.Derived.Properties[1].FieldOffset
	shape, err := emitVERT013bShape(ctx, module, bound.Layout.Derived, ptr, i8, ctx.Int16Type(), i32, i64, null)
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
			return vert010RuntimeFunction{}, fmt.Errorf("OBJ-003b runtime capability %q is unbound", logical)
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
	makeFrame := func(prefix string) llvm.Value {
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
		return frame
	}
	callChecked := func(function llvm.Value, name string, runtime vert010RuntimeFunction, args []llvm.Value) {
		status := builder.CreateCall(runtime.functionType, runtime.value, args, name+".status")
		emitVERT010StatusCheck(ctx, builder, module, function, status, name)
	}
	fieldPtr := func(receiver llvm.Value, offset uint32, name string) llvm.Value {
		return builder.CreateInBoundsGEP(i8, receiver, []llvm.Value{llvm.ConstInt(i64, uint64(offset), false)}, name)
	}

	baseType := llvm.FunctionType(ptr, []llvm.Type{ptr}, false)
	baseCtor := llvm.AddFunction(module, "classaccess.Vault.constructor", baseType)
	baseCtor.SetLinkage(llvm.PrivateLinkage)
	baseCtor.SetFunctionCallConv(llvm.CCallConv)
	b := llvm.AddBasicBlock(baseCtor, "entry")
	builder.SetInsertPointAtEnd(b)
	builder.CreateStore(llvm.ConstFloat(double, 1), fieldPtr(baseCtor.Param(0), secretOffset, "receiver.secret"))
	builder.CreateStore(llvm.ConstFloat(double, 2), fieldPtr(baseCtor.Param(0), valueOffset, "receiver.value"))
	builder.CreateRet(baseCtor.Param(0))

	derivedType := llvm.FunctionType(ptr, nil, false)
	derivedCtor := llvm.AddFunction(module, "classaccess.DerivedVault.constructor", derivedType)
	derivedCtor.SetLinkage(llvm.PrivateLinkage)
	derivedCtor.SetFunctionCallConv(llvm.CCallConv)
	b = llvm.AddBasicBlock(derivedCtor, "entry")
	builder.SetInsertPointAtEnd(b)
	out := builder.CreateAlloca(ptr, "receiver.out")
	builder.CreateStore(null, out)
	status := builder.CreateCall(alloc.functionType, alloc.value, []llvm.Value{shape, out}, "receiver.alloc.status")
	emitVERT010StatusCheck(ctx, builder, module, derivedCtor, status, "receiver.alloc")
	receiver := builder.CreateLoad(ptr, out, "receiver")
	frame := makeFrame("constructor.gc")
	callChecked(derivedCtor, "constructor.frame.link", frameLink, []llvm.Value{frame})
	callChecked(derivedCtor, "constructor.root.store", rootStore, []llvm.Value{frame, zero32, receiver})
	callChecked(derivedCtor, "constructor.root.publish", rootPublish, []llvm.Value{frame, llvm.ConstInt(i64, 1, false)})
	callChecked(derivedCtor, "constructor.super.safepoint", safepoint, nil)
	reloadOut := builder.CreateAlloca(ptr, "constructor.receiver.reload.out")
	builder.CreateStore(null, reloadOut)
	callChecked(derivedCtor, "constructor.root.reload", rootReload, []llvm.Value{frame, zero32, reloadOut})
	receiver = builder.CreateLoad(ptr, reloadOut, "constructor.receiver.reloaded")
	receiver = builder.CreateCall(baseType, baseCtor, []llvm.Value{receiver}, "super.receiver")
	callChecked(derivedCtor, "constructor.frame.unlink", frameUnlink, []llvm.Value{frame})
	builder.CreateRet(receiver)

	methodType := llvm.FunctionType(double, []llvm.Type{ptr, ptr}, false)
	readSecret := llvm.AddFunction(module, "classaccess.Vault.readSecret", methodType)
	readSecret.SetLinkage(llvm.PrivateLinkage)
	readSecret.SetFunctionCallConv(llvm.CCallConv)
	b = llvm.AddBasicBlock(readSecret, "entry")
	builder.SetInsertPointAtEnd(b)
	builder.CreateRet(builder.CreateLoad(double, fieldPtr(readSecret.Param(1), secretOffset, "other.secret"), "secret"))
	readValue := llvm.AddFunction(module, "classaccess.DerivedVault.readValue", methodType)
	readValue.SetLinkage(llvm.PrivateLinkage)
	readValue.SetFunctionCallConv(llvm.CCallConv)
	b = llvm.AddBasicBlock(readValue, "entry")
	builder.SetInsertPointAtEnd(b)
	builder.CreateRet(builder.CreateLoad(double, fieldPtr(readValue.Param(1), valueOffset, "other.value"), "value"))

	entryType := llvm.FunctionType(double, nil, false)
	entry := llvm.AddFunction(module, "classAccess", entryType)
	entry.SetFunctionCallConv(llvm.CCallConv)
	b = llvm.AddBasicBlock(entry, "entry")
	builder.SetInsertPointAtEnd(b)
	frame = makeFrame("entry.gc")
	callChecked(entry, "entry.frame.link", frameLink, []llvm.Value{frame})
	callChecked(entry, "entry.root.clear", rootClear, []llvm.Value{frame, zero32})
	callChecked(entry, "entry.root.publish.empty", rootPublish, []llvm.Value{frame, llvm.ConstInt(i64, 0, false)})
	receiver = builder.CreateCall(derivedType, derivedCtor, nil, "receiver.constructed")
	reloadOut = builder.CreateAlloca(ptr, "entry.receiver.reload.out")
	builder.CreateStore(null, reloadOut)
	rootedCall := func(label string, method llvm.Value) llvm.Value {
		callChecked(entry, label+".root.store", rootStore, []llvm.Value{frame, zero32, receiver})
		callChecked(entry, label+".root.publish", rootPublish, []llvm.Value{frame, llvm.ConstInt(i64, 1, false)})
		callChecked(entry, label+".safepoint", safepoint, nil)
		callChecked(entry, label+".root.reload", rootReload, []llvm.Value{frame, zero32, reloadOut})
		receiver = builder.CreateLoad(ptr, reloadOut, label+".receiver.reloaded")
		return builder.CreateCall(methodType, method, []llvm.Value{receiver, receiver}, label+".result")
	}
	first, second := rootedCall("read.secret", readSecret), rootedCall("read.value", readValue)
	result := builder.CreateFAdd(first, second, "result")
	callChecked(entry, "entry.frame.unlink", frameUnlink, []llvm.Value{frame})
	builder.CreateRet(result)
	return nil
}
