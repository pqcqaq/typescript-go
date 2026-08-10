//go:build llvm20 && cgo && linux

package llvmbackend

import (
	"fmt"
	"sync"

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
		dispose:  targetMachine.Dispose,
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
