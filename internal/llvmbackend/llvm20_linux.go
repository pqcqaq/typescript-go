//go:build llvm20 && cgo && linux

package llvmbackend

import (
	"fmt"
	"math"
	"strconv"
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
	switch {
	case len(mir.Functions) == 1 && mir.Functions[0].Name == "add":
		emitNumberAddLLVM(ctx, builder, module, mir.Functions[0])
	case len(mir.Functions) == 1 && mir.Functions[0].Name == "choose":
		emitBooleanChooseLLVM(ctx, builder, module, mir.Functions[0])
	case len(mir.Functions) == 1 && mir.Functions[0].Name == "classify":
		if err := emitClassifyLLVM(ctx, builder, module, mir.Functions[0]); err != nil {
			return FirstSliceEmission{}, err
		}
	case len(mir.Functions) == 2 && mir.Functions[0].Name == "add" && mir.Functions[1].Name == "compute":
		if err := emitLocalCallLLVM(ctx, builder, module, mir.Functions); err != nil {
			return FirstSliceEmission{}, err
		}
	case len(mir.Functions) == 1 && mir.Functions[0].Name == "compute":
		emitLoopLLVM(ctx, builder, module, mir.Functions[0])
	case len(mir.Functions) == 1 && (mir.Functions[0].Name == "coalesce" || mir.Functions[0].Name == "coalesceAssign"):
		emitCoalesceLLVM(ctx, builder, module, mir.Functions[0])
	case len(mir.Functions) == 1 && mir.Functions[0].Name == "stringLength":
		emitStringLengthLLVM(ctx, builder, module, mir.Functions[0])
	case len(mir.Functions) == 1 && mir.Functions[0].Name == "main":
		if err := emitApplicationMainLLVM(ctx, builder, module, mir.Functions[0]); err != nil {
			return FirstSliceEmission{}, err
		}
	default:
		return FirstSliceEmission{}, fmt.Errorf("unsupported primitive LLVM function set")
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

func emitApplicationMainLLVM(ctx llvm.Context, builder llvm.Builder, module llvm.Module, mir bingo.FirstSliceMIRFunction) error {
	double := ctx.DoubleType()
	result, err := llvmF64Constant(double, mir.Blocks[0].Instructions[0].NumberBits)
	if err != nil {
		return fmt.Errorf("application main result: %w", err)
	}
	function := llvm.AddFunction(module, "bingo_program_main_v1", llvm.FunctionType(double, nil, false))
	function.SetFunctionCallConv(llvm.CCallConv)
	function.AddFunctionAttr(ctx.CreateEnumAttribute(llvm.AttributeKindID("nounwind"), 0))
	entry := llvm.AddBasicBlock(function, "entry")
	builder.SetInsertPointAtEnd(entry)
	builder.CreateRet(result)
	return nil
}

func emitLocalCallLLVM(ctx llvm.Context, builder llvm.Builder, module llvm.Module, functions []bingo.FirstSliceMIRFunction) error {
	double := ctx.DoubleType()
	functionType := llvm.FunctionType(double, []llvm.Type{double, double}, false)
	helper := llvm.AddFunction(module, "add", functionType)
	helper.SetLinkage(llvm.InternalLinkage)
	helper.SetFunctionCallConv(llvm.CCallConv)
	helper.AddFunctionAttr(ctx.CreateEnumAttribute(llvm.AttributeKindID("nounwind"), 0))
	for index, parameter := range functions[0].Parameters {
		helper.Param(index).SetName(parameter.Name)
	}
	helperBlock := llvm.AddBasicBlock(helper, "entry")
	builder.SetInsertPointAtEnd(helperBlock)
	sum := builder.CreateFAdd(helper.Param(0), helper.Param(1), "sum")
	builder.CreateRet(sum)

	entry := llvm.AddFunction(module, "compute", functionType)
	entry.SetFunctionCallConv(llvm.CCallConv)
	entry.AddFunctionAttr(ctx.CreateEnumAttribute(llvm.AttributeKindID("nounwind"), 0))
	for index, parameter := range functions[1].Parameters {
		entry.Param(index).SetName(parameter.Name)
	}
	block := llvm.AddBasicBlock(entry, "entry")
	builder.SetInsertPointAtEnd(block)
	call := builder.CreateCall(functionType, helper, []llvm.Value{entry.Param(0), entry.Param(1)}, "call.add")
	result := builder.CreateFAdd(call, entry.Param(1), "sum")
	builder.CreateRet(result)
	return nil
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

func emitClassifyLLVM(ctx llvm.Context, builder llvm.Builder, module llvm.Module, mir bingo.FirstSliceMIRFunction) error {
	double := ctx.DoubleType()
	functionType := llvm.FunctionType(double, []llvm.Type{double}, false)
	function := llvm.AddFunction(module, "classify", functionType)
	function.SetFunctionCallConv(llvm.CCallConv)
	function.AddFunctionAttr(ctx.CreateEnumAttribute(llvm.AttributeKindID("nounwind"), 0))
	function.Param(0).SetName(mir.Parameters[0].Name)

	entry := llvm.AddBasicBlock(function, "entry")
	negative := llvm.AddBasicBlock(function, "bb2.negative")
	second := llvm.AddBasicBlock(function, "bb3.second")
	zero := llvm.AddBasicBlock(function, "bb4.zero")
	positive := llvm.AddBasicBlock(function, "bb5.positive")

	zeroThreshold, err := llvmF64Constant(double, mir.Blocks[0].Instructions[0].NumberBits)
	if err != nil {
		return fmt.Errorf("classify zero threshold: %w", err)
	}
	negativeOperand, err := llvmF64Constant(double, mir.Blocks[1].Instructions[0].NumberBits)
	if err != nil {
		return fmt.Errorf("classify negative operand: %w", err)
	}
	oneThreshold, err := llvmF64Constant(double, mir.Blocks[2].Instructions[0].NumberBits)
	if err != nil {
		return fmt.Errorf("classify one threshold: %w", err)
	}
	zeroResult, err := llvmF64Constant(double, mir.Blocks[3].Instructions[0].NumberBits)
	if err != nil {
		return fmt.Errorf("classify zero result: %w", err)
	}
	positiveResult, err := llvmF64Constant(double, mir.Blocks[4].Instructions[0].NumberBits)
	if err != nil {
		return fmt.Errorf("classify positive result: %w", err)
	}

	builder.SetInsertPointAtEnd(entry)
	isNegative := builder.CreateFCmp(llvm.FloatOLT, function.Param(0), zeroThreshold, "value.lt.zero")
	builder.CreateCondBr(isNegative, negative, second)
	builder.SetInsertPointAtEnd(negative)
	builder.CreateRet(builder.CreateFNeg(negativeOperand, "negative.one"))
	builder.SetInsertPointAtEnd(second)
	isZeroClass := builder.CreateFCmp(llvm.FloatOLT, function.Param(0), oneThreshold, "value.lt.one")
	builder.CreateCondBr(isZeroClass, zero, positive)
	builder.SetInsertPointAtEnd(zero)
	builder.CreateRet(zeroResult)
	builder.SetInsertPointAtEnd(positive)
	builder.CreateRet(positiveResult)
	return nil
}

func llvmF64Constant(double llvm.Type, bits string) (llvm.Value, error) {
	value, err := strconv.ParseUint(bits, 16, 64)
	if err != nil {
		return llvm.Value{}, fmt.Errorf("decode binary64 bits %q: %w", bits, err)
	}
	return llvm.ConstFloat(double, math.Float64frombits(value)), nil
}

func emitLoopLLVM(ctx llvm.Context, builder llvm.Builder, module llvm.Module, mir bingo.FirstSliceMIRFunction) {
	double := ctx.DoubleType()
	functionType := llvm.FunctionType(double, []llvm.Type{double, double}, false)
	function := llvm.AddFunction(module, "compute", functionType)
	function.SetFunctionCallConv(llvm.CCallConv)
	function.AddFunctionAttr(ctx.CreateEnumAttribute(llvm.AttributeKindID("nounwind"), 0))
	for index, parameter := range mir.Parameters {
		function.Param(index).SetName(parameter.Name)
	}
	entry := llvm.AddBasicBlock(function, "entry")
	header := llvm.AddBasicBlock(function, "bb2.loop.header")
	body := llvm.AddBasicBlock(function, "bb3.loop.body")
	exit := llvm.AddBasicBlock(function, "bb4.loop.exit")

	builder.SetInsertPointAtEnd(entry)
	builder.CreateBr(header)
	builder.SetInsertPointAtEnd(header)
	value := builder.CreatePHI(double, "value.phi")
	condition := builder.CreateFCmp(llvm.FloatOLT, value, function.Param(1), "value.lt.limit")
	builder.CreateCondBr(condition, body, exit)
	builder.SetInsertPointAtEnd(body)
	next := builder.CreateFAdd(value, function.Param(0), "value.next")
	builder.CreateBr(header)
	value.AddIncoming([]llvm.Value{function.Param(0), next}, []llvm.BasicBlock{entry, body})
	builder.SetInsertPointAtEnd(exit)
	builder.CreateRet(value)
}

func emitCoalesceLLVM(ctx llvm.Context, builder llvm.Builder, module llvm.Module, mir bingo.FirstSliceMIRFunction) {
	double := ctx.DoubleType()
	i8 := ctx.Int8Type()
	nullable := ctx.StructType([]llvm.Type{i8, llvm.ArrayType(i8, 7), double}, false)
	functionType := llvm.FunctionType(double, []llvm.Type{nullable, double}, false)
	function := llvm.AddFunction(module, mir.Name, functionType)
	function.SetFunctionCallConv(llvm.CCallConv)
	function.AddFunctionAttr(ctx.CreateEnumAttribute(llvm.AttributeKindID("nounwind"), 0))
	for index, parameter := range mir.Parameters {
		function.Param(index).SetName(parameter.Name)
	}

	entry := llvm.AddBasicBlock(function, "entry")
	invalid := llvm.AddBasicBlock(function, "invalid.nullable")
	dispatch := llvm.AddBasicBlock(function, "dispatch.nullish")
	fallback := llvm.AddBasicBlock(function, "bb2.fallback")
	valueBlock := llvm.AddBasicBlock(function, "bb3.value")
	merge := llvm.AddBasicBlock(function, "bb4.merge")

	builder.SetInsertPointAtEnd(entry)
	tag := builder.CreateExtractValue(function.Param(0), 0, "value.tag")
	payload := builder.CreateExtractValue(function.Param(0), 2, "value.payload")
	tagValid := builder.CreateICmp(llvm.IntULE, tag, llvm.ConstInt(i8, uint64(bingo.NullableNumberTagUndefined), false), "tag.canonical")
	nonNumber := builder.CreateICmp(llvm.IntNE, tag, llvm.ConstInt(i8, uint64(bingo.NullableNumberTagNumber), false), "tag.nullish")
	payloadBits := builder.CreateBitCast(payload, ctx.Int64Type(), "payload.bits")
	payloadZero := builder.CreateICmp(llvm.IntEQ, payloadBits, llvm.ConstInt(ctx.Int64Type(), 0, false), "payload.canonical")
	nonCanonicalPayload := builder.CreateAnd(nonNumber, builder.CreateNot(payloadZero, "payload.nonzero"), "nullish.payload.invalid")
	canonical := builder.CreateAnd(tagValid, builder.CreateNot(nonCanonicalPayload, "nullable.canonical"), "nullable.valid")
	builder.CreateCondBr(canonical, dispatch, invalid)

	builder.SetInsertPointAtEnd(invalid)
	trapType := llvm.FunctionType(ctx.VoidType(), nil, false)
	trap := llvm.AddFunction(module, "llvm.trap", trapType)
	trap.AddFunctionAttr(ctx.CreateEnumAttribute(llvm.AttributeKindID("nounwind"), 0))
	trap.AddFunctionAttr(ctx.CreateEnumAttribute(llvm.AttributeKindID("noreturn"), 0))
	builder.CreateCall(trapType, trap, nil, "")
	builder.CreateUnreachable()

	builder.SetInsertPointAtEnd(dispatch)
	builder.CreateCondBr(nonNumber, fallback, valueBlock)
	builder.SetInsertPointAtEnd(fallback)
	builder.CreateBr(merge)
	builder.SetInsertPointAtEnd(valueBlock)
	builder.CreateBr(merge)
	builder.SetInsertPointAtEnd(merge)
	result := builder.CreatePHI(double, "coalesce.result")
	result.AddIncoming([]llvm.Value{function.Param(1), payload}, []llvm.BasicBlock{fallback, valueBlock})
	builder.CreateRet(result)
}

func emitStringLengthLLVM(ctx llvm.Context, builder llvm.Builder, module llvm.Module, mir bingo.FirstSliceMIRFunction) {
	i16 := ctx.Int16Type()
	i64 := ctx.Int64Type()
	pointer := llvm.PointerType(i16, 0)
	view := ctx.StructType([]llvm.Type{pointer, i64}, false)
	double := ctx.DoubleType()
	functionType := llvm.FunctionType(double, []llvm.Type{view}, false)
	function := llvm.AddFunction(module, "stringLength", functionType)
	function.SetFunctionCallConv(llvm.CCallConv)
	function.AddFunctionAttr(ctx.CreateEnumAttribute(llvm.AttributeKindID("nounwind"), 0))
	function.Param(0).SetName(mir.Parameters[0].Name)

	entry := llvm.AddBasicBlock(function, "entry")
	valid := llvm.AddBasicBlock(function, "decode.utf16")
	invalid := llvm.AddBasicBlock(function, "invalid.utf16")
	builder.SetInsertPointAtEnd(entry)
	data := builder.CreateExtractValue(function.Param(0), 0, "value.data")
	length := builder.CreateExtractValue(function.Param(0), 1, "value.length")
	dataNull := builder.CreateIsNull(data, "data.null")
	lengthZero := builder.CreateICmp(llvm.IntEQ, length, llvm.ConstInt(i64, 0, false), "length.zero")
	canonical := builder.CreateICmp(llvm.IntEQ, dataNull, lengthZero, "view.canonical")
	builder.CreateCondBr(canonical, valid, invalid)

	builder.SetInsertPointAtEnd(invalid)
	trapType := llvm.FunctionType(ctx.VoidType(), nil, false)
	trap := llvm.AddFunction(module, "llvm.trap", trapType)
	trap.AddFunctionAttr(ctx.CreateEnumAttribute(llvm.AttributeKindID("nounwind"), 0))
	trap.AddFunctionAttr(ctx.CreateEnumAttribute(llvm.AttributeKindID("noreturn"), 0))
	builder.CreateCall(trapType, trap, nil, "")
	builder.CreateUnreachable()

	builder.SetInsertPointAtEnd(valid)
	builder.CreateRet(builder.CreateUIToFP(length, double, "length.number"))
}
