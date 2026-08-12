//go:build llvm20 && cgo && linux

package llvmbackend

import (
	"fmt"

	llvm "tinygo.org/x/go-llvm"
)

func emitPropertyAccessObject(targetMachine llvm.TargetMachine, manifest ToolchainManifest, plan PropertyAccessBackendPlan) (PropertyAccessEmission, error) {
	ctx := llvm.NewContext()
	defer ctx.Dispose()
	module := ctx.NewModule("ts2bin-property-access-dynamic")
	defer module.Dispose()
	module.SetTarget(manifest.TargetTriple)
	module.SetDataLayout(manifest.DataLayout.LayoutString)
	builder := ctx.NewBuilder()
	defer builder.Dispose()
	if err := emitPropertyAccessFunction(ctx, builder, module, plan); err != nil {
		return PropertyAccessEmission{}, err
	}
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		return PropertyAccessEmission{}, fmt.Errorf("verify property access LLVM module: %w", err)
	}
	ir := []byte(module.String())
	buffer, err := targetMachine.EmitToMemoryBuffer(module, llvm.ObjectFile)
	if err != nil {
		return PropertyAccessEmission{}, fmt.Errorf("emit property access object: %w", err)
	}
	defer buffer.Dispose()
	return newEmission(plan.ContentHash, manifest, ir, buffer.Bytes())
}

func emitPropertyAccessFunction(ctx llvm.Context, builder llvm.Builder, module llvm.Module, plan PropertyAccessBackendPlan) error {
	if err := VerifyCanonicalPropertyAccessBackendPlan(plan); err != nil {
		return err
	}
	i32 := ctx.Int32Type()
	i64 := ctx.Int64Type()
	i16 := ctx.Int16Type()
	dynamicValue := ctx.StructType([]llvm.Type{i32, i32, i64}, false)
	utf16View := ctx.StructType([]llvm.Type{llvm.PointerType(i16, 0), i64}, false)
	outValue := llvm.PointerType(dynamicValue, 0)
	runtimeType := llvm.FunctionType(i32, []llvm.Type{dynamicValue, utf16View, outValue}, false)
	runtime := llvm.AddFunction(module, plan.RuntimeSymbol, runtimeType)
	runtime.SetFunctionCallConv(llvm.CCallConv)
	entry := llvm.AddFunction(module, plan.EntrySymbol, runtimeType)
	entry.SetFunctionCallConv(llvm.CCallConv)
	entry.Param(0).SetName("receiver")
	entry.Param(1).SetName("key")
	entry.Param(2).SetName("out.value")
	block := llvm.AddBasicBlock(entry, "entry")
	success := llvm.AddBasicBlock(entry, "success")
	failure := llvm.AddBasicBlock(entry, "failure")
	builder.SetInsertPointAtEnd(block)
	status := builder.CreateCall(runtimeType, runtime, []llvm.Value{entry.Param(0), entry.Param(1), entry.Param(2)}, "status")
	isSuccess := builder.CreateICmp(llvm.IntEQ, status, llvm.ConstInt(i32, uint64(plan.SuccessStatus), false), "status.is.success")
	builder.CreateCondBr(isSuccess, success, failure)
	builder.SetInsertPointAtEnd(success)
	builder.CreateRet(llvm.ConstInt(i32, uint64(plan.SuccessStatus), false))
	builder.SetInsertPointAtEnd(failure)
	undefined := llvm.ConstStruct([]llvm.Value{
		llvm.ConstInt(i32, 0, false),
		llvm.ConstInt(i32, 0, false),
		llvm.ConstInt(i64, 0, false),
	}, false)
	builder.CreateStore(undefined, entry.Param(2))
	builder.CreateRet(status)
	return nil
}
