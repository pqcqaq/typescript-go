//go:build llvm20 && cgo && linux

package llvmbackend

import (
	"fmt"

	llvm "tinygo.org/x/go-llvm"
)

func emitObjectViewObject(targetMachine llvm.TargetMachine, manifest ToolchainManifest, plan ObjectViewBackendPlan) (ObjectViewEmission, error) {
	ctx := llvm.NewContext()
	defer ctx.Dispose()
	module := ctx.NewModule("ts2bin-object-view")
	defer module.Dispose()
	module.SetTarget(manifest.TargetTriple)
	module.SetDataLayout(manifest.DataLayout.LayoutString)
	builder := ctx.NewBuilder()
	defer builder.Dispose()
	if err := emitObjectViewFunction(ctx, builder, module, plan); err != nil {
		return ObjectViewEmission{}, err
	}
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		return ObjectViewEmission{}, fmt.Errorf("verify ObjectView LLVM module: %w", err)
	}
	ir := []byte(module.String())
	buffer, err := targetMachine.EmitToMemoryBuffer(module, llvm.ObjectFile)
	if err != nil {
		return ObjectViewEmission{}, fmt.Errorf("emit ObjectView object: %w", err)
	}
	defer buffer.Dispose()
	return newEmission(plan.ContentHash, manifest, ir, buffer.Bytes())
}

func emitObjectViewFunction(ctx llvm.Context, builder llvm.Builder, module llvm.Module, plan ObjectViewBackendPlan) error {
	if err := VerifyCanonicalObjectViewBackendPlan(plan); err != nil {
		return err
	}
	i8 := ctx.Int8Type()
	pointer := llvm.PointerType(i8, 0)
	double := ctx.DoubleType()
	if plan.Accessor {
		void := ctx.VoidType()
		getterType := llvm.FunctionType(void, []llvm.Type{pointer, llvm.PointerType(double, 0), llvm.PointerType(i8, 0)}, false)
		getter := llvm.AddFunction(module, "objectview.getter."+plan.GetterSymbolKey, getterType)
		getter.SetLinkage(llvm.PrivateLinkage)
		getterEntry := llvm.AddBasicBlock(getter, "entry")
		builder.SetInsertPointAtEnd(getterEntry)
		payload := builder.CreateInBoundsGEP(i8, getter.Param(0), []llvm.Value{llvm.ConstInt(ctx.Int64Type(), uint64(plan.BackingOffset), false)}, "backing.payload")
		tag := builder.CreateInBoundsGEP(i8, getter.Param(0), []llvm.Value{llvm.ConstInt(ctx.Int64Type(), uint64(plan.BackingOffset+8), false)}, "backing.tag")
		builder.CreateStore(builder.CreateLoad(double, payload, "payload"), getter.Param(1))
		builder.CreateStore(builder.CreateLoad(i8, tag, "tag"), getter.Param(2))
		builder.CreateRetVoid()
		functionType := llvm.FunctionType(void, []llvm.Type{pointer, llvm.PointerType(double, 0), llvm.PointerType(i8, 0)}, false)
		function := llvm.AddFunction(module, plan.FunctionName, functionType)
		function.SetFunctionCallConv(llvm.CCallConv)
		entry := llvm.AddBasicBlock(function, "entry")
		builder.SetInsertPointAtEnd(entry)
		builder.CreateCall(getterType, getter, []llvm.Value{function.Param(0), function.Param(1), function.Param(2)}, "")
		builder.CreateRetVoid()
		return nil
	}
	functionType := llvm.FunctionType(double, []llvm.Type{pointer}, false)
	function := llvm.AddFunction(module, plan.FunctionName, functionType)
	function.SetFunctionCallConv(llvm.CCallConv)
	function.Param(0).SetName("source")
	entry := llvm.AddBasicBlock(function, "entry")
	builder.SetInsertPointAtEnd(entry)
	address := builder.CreateInBoundsGEP(i8, function.Param(0), []llvm.Value{llvm.ConstInt(ctx.Int64Type(), uint64(plan.SourceOffset), false)}, "view.field")
	value := builder.CreateLoad(double, address, "view.value")
	builder.CreateRet(value)
	return nil
}
