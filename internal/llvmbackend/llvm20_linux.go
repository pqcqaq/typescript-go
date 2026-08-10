//go:build llvm20 && cgo && linux

package llvmbackend

import (
	"fmt"
	"sync"

	"github.com/microsoft/typescript-go/internal/bingo"
	llvm "tinygo.org/x/go-llvm"
)

var (
	llvmInit    sync.Once
	llvmInitErr error
)

func openFirstSliceTargetMachine() (*TargetMachine, error) {
	llvmInit.Do(func() {
		llvm.InitializeAllTargetInfos()
		llvm.InitializeAllTargets()
		llvm.InitializeAllTargetMCs()
		llvm.InitializeAllAsmParsers()
		llvm.InitializeAllAsmPrinters()
		llvmInitErr = llvm.InitializeNativeAsmPrinter()
	})
	if llvmInitErr != nil {
		return nil, fmt.Errorf("initialize LLVM native asm printer: %w", llvmInitErr)
	}
	target, err := llvm.GetTargetFromTriple(FirstSliceTriple)
	if err != nil {
		return nil, fmt.Errorf("resolve LLVM target %s: %w", FirstSliceTriple, err)
	}
	targetMachine := target.CreateTargetMachine(FirstSliceTriple, FirstSliceCPU, "", llvm.CodeGenLevelNone, llvm.RelocPIC, llvm.CodeModelSmall)
	data := targetMachine.CreateTargetData()
	layoutContext := llvm.NewContext()
	double := layoutContext.DoubleType()
	layout := newDataLayout(FirstSliceTriple, data.String(), uint32(data.PointerSize()*8), uint32(data.TypeSizeInBits(double)), uint32(data.ABITypeAlignment(double)), data.ByteOrder() == llvm.LittleEndian)
	layoutContext.Dispose()
	data.Dispose()
	manifest := newToolchainManifest(layout)
	if err := ValidateToolchainManifest(manifest); err != nil {
		targetMachine.Dispose()
		return nil, fmt.Errorf("validate observed LLVM manifest: %w", err)
	}
	return &TargetMachine{
		manifest: manifest,
		emit:     func() ([]byte, error) { return emitProbeObject(targetMachine, manifest) },
		emitFirstSlice: func(module bingo.FirstSliceMIRArtifact) (FirstSliceEmission, error) {
			return emitFirstSliceObject(targetMachine, manifest, module)
		},
		dispose: targetMachine.Dispose,
	}, nil
}

func emitProbeObject(targetMachine llvm.TargetMachine, manifest ToolchainManifest) ([]byte, error) {
	ctx := llvm.NewContext()
	defer ctx.Dispose()
	module := ctx.NewModule("ts2bin-be-001a-probe")
	defer module.Dispose()
	module.SetTarget(manifest.TargetTriple)
	module.SetDataLayout(manifest.DataLayout.LayoutString)
	double := ctx.DoubleType()
	function := llvm.AddFunction(module, "be001a_probe", llvm.FunctionType(double, nil, false))
	block := llvm.AddBasicBlock(function, "entry")
	builder := ctx.NewBuilder()
	defer builder.Dispose()
	builder.SetInsertPointAtEnd(block)
	builder.CreateRet(llvm.ConstFloat(double, 0))
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		return nil, fmt.Errorf("verify probe module: %w", err)
	}
	buffer, err := targetMachine.EmitToMemoryBuffer(module, llvm.ObjectFile)
	if err != nil {
		return nil, fmt.Errorf("emit probe object: %w", err)
	}
	defer buffer.Dispose()
	return buffer.Bytes(), nil
}

func emitFirstSliceObject(targetMachine llvm.TargetMachine, manifest ToolchainManifest, mir bingo.FirstSliceMIRArtifact) (FirstSliceEmission, error) {
	ctx := llvm.NewContext()
	defer ctx.Dispose()
	module := ctx.NewModule("ts2bin-first-slice")
	defer module.Dispose()
	module.SetTarget(manifest.TargetTriple)
	module.SetDataLayout(manifest.DataLayout.LayoutString)

	builder := ctx.NewBuilder()
	defer builder.Dispose()
	switch mir.Functions[0].Name {
	case "add":
		emitNumberAddLLVM(ctx, builder, module, mir.Functions[0])
	case "choose":
		emitBooleanChooseLLVM(ctx, builder, module, mir.Functions[0])
	default:
		return FirstSliceEmission{}, fmt.Errorf("unsupported primitive LLVM function %q", mir.Functions[0].Name)
	}

	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		return FirstSliceEmission{}, fmt.Errorf("verify first-slice LLVM module: %w", err)
	}
	llvmIR := []byte(module.String())
	buffer, err := targetMachine.EmitToMemoryBuffer(module, llvm.ObjectFile)
	if err != nil {
		return FirstSliceEmission{}, fmt.Errorf("emit first-slice object: %w", err)
	}
	defer buffer.Dispose()
	return newFirstSliceEmission(mir, manifest, llvmIR, buffer.Bytes())
}

func emitNumberAddLLVM(ctx llvm.Context, builder llvm.Builder, module llvm.Module, mir bingo.FirstSliceMIRFunction) {
	double := ctx.DoubleType()
	function := llvm.AddFunction(module, "add", llvm.FunctionType(double, []llvm.Type{double, double}, false))
	function.SetFunctionCallConv(llvm.CCallConv)
	function.AddFunctionAttr(ctx.CreateEnumAttribute(llvm.AttributeKindID("nounwind"), 0))
	function.Param(0).SetName(mir.Parameters[0].Name)
	function.Param(1).SetName(mir.Parameters[1].Name)
	entry := llvm.AddBasicBlock(function, "entry")
	builder.SetInsertPointAtEnd(entry)
	result := builder.CreateFAdd(function.Param(0), function.Param(1), "sum")
	builder.CreateRet(result)
}

func emitBooleanChooseLLVM(ctx llvm.Context, builder llvm.Builder, module llvm.Module, mir bingo.FirstSliceMIRFunction) {
	double := ctx.DoubleType()
	i8 := ctx.Int8Type()
	functionType := llvm.FunctionType(double, []llvm.Type{i8, double, double}, false)
	function := llvm.AddFunction(module, "choose", functionType)
	function.SetFunctionCallConv(llvm.CCallConv)
	function.AddFunctionAttr(ctx.CreateEnumAttribute(llvm.AttributeKindID("nounwind"), 0))
	for index, parameter := range mir.Parameters {
		function.Param(index).SetName(parameter.Name)
	}

	entry := llvm.AddBasicBlock(function, "entry")
	decode := llvm.AddBasicBlock(function, "decode.boolean")
	invalid := llvm.AddBasicBlock(function, "invalid.boolean")
	trueBlock := llvm.AddBasicBlock(function, "bb2.true")
	falseBlock := llvm.AddBasicBlock(function, "bb3.false")

	builder.SetInsertPointAtEnd(entry)
	canonical := builder.CreateICmp(llvm.IntULT, function.Param(0), llvm.ConstInt(i8, 2, false), "flag.canonical")
	builder.CreateCondBr(canonical, decode, invalid)

	builder.SetInsertPointAtEnd(invalid)
	trapType := llvm.FunctionType(ctx.VoidType(), nil, false)
	trap := llvm.AddFunction(module, "llvm.trap", trapType)
	trap.AddFunctionAttr(ctx.CreateEnumAttribute(llvm.AttributeKindID("nounwind"), 0))
	trap.AddFunctionAttr(ctx.CreateEnumAttribute(llvm.AttributeKindID("noreturn"), 0))
	builder.CreateCall(trapType, trap, nil, "")
	builder.CreateUnreachable()

	builder.SetInsertPointAtEnd(decode)
	condition := builder.CreateTrunc(function.Param(0), ctx.Int1Type(), "flag.i1")
	builder.CreateCondBr(condition, trueBlock, falseBlock)

	builder.SetInsertPointAtEnd(trueBlock)
	builder.CreateRet(function.Param(1))
	builder.SetInsertPointAtEnd(falseBlock)
	builder.CreateRet(function.Param(2))
}
