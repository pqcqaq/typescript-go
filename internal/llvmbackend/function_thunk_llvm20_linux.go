//go:build llvm20 && cgo && linux

package llvmbackend

import (
	"fmt"

	llvm "tinygo.org/x/go-llvm"
)

func emitFunctionThunkObject(targetMachine llvm.TargetMachine, manifest ToolchainManifest, plan FunctionThunkBackendPlan) (FunctionThunkEmission, error) {
	ctx := llvm.NewContext()
	defer ctx.Dispose()
	module := ctx.NewModule("ts2bin-function-thunk")
	defer module.Dispose()
	module.SetTarget(manifest.TargetTriple)
	module.SetDataLayout(manifest.DataLayout.LayoutString)
	builder := ctx.NewBuilder()
	defer builder.Dispose()
	if err := emitFunctionThunkFunction(ctx, builder, module, plan); err != nil {
		return FunctionThunkEmission{}, err
	}
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		return FunctionThunkEmission{}, fmt.Errorf("verify function thunk LLVM module: %w", err)
	}
	ir := []byte(module.String())
	buffer, err := targetMachine.EmitToMemoryBuffer(module, llvm.ObjectFile)
	if err != nil {
		return FunctionThunkEmission{}, fmt.Errorf("emit function thunk object: %w", err)
	}
	defer buffer.Dispose()
	return newEmission(plan.ContentHash, manifest, ir, buffer.Bytes())
}

func emitFunctionThunkFunction(ctx llvm.Context, builder llvm.Builder, module llvm.Module, plan FunctionThunkBackendPlan) error {
	if err := VerifyCanonicalFunctionThunkBackendPlan(plan); err != nil {
		return err
	}
	i8 := ctx.Int8Type()
	ptr := llvm.PointerType(i8, 0)
	funcRef := ctx.StructType([]llvm.Type{ptr, ptr}, false)
	sourceType := llvm.FunctionType(ptr, []llvm.Type{ptr, ptr}, false)
	wrapperType := llvm.FunctionType(ptr, []llvm.Type{funcRef, ptr}, false)
	wrapper := llvm.AddFunction(module, plan.FunctionName, wrapperType)
	wrapper.SetFunctionCallConv(llvm.CCallConv)
	wrapper.Param(0).SetName("source")
	wrapper.Param(1).SetName("target.parameter")
	entry := llvm.AddBasicBlock(wrapper, "entry")
	builder.SetInsertPointAtEnd(entry)
	code := builder.CreateExtractValue(wrapper.Param(0), plan.MIR.FunctionRefABI.CodeFieldIndex, "source.code")
	environment := builder.CreateExtractValue(wrapper.Param(0), plan.MIR.FunctionRefABI.EnvironmentFieldIndex, "source.environment")
	result := builder.CreateCall(sourceType, code, []llvm.Value{environment, wrapper.Param(1)}, "source.result")
	builder.CreateRet(result)
	return nil
}
