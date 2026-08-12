//go:build llvm20 && cgo && linux

package llvmbackend

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/bingo"
	llvm "tinygo.org/x/go-llvm"
)

func TestObjectLayoutABIUsesTwoAuthoritativeLLVMDataLayouts(t *testing.T) {
	llvm.InitializeAllTargetInfos()
	llvm.InitializeAllTargets()
	llvm.InitializeAllTargetMCs()

	for _, triple := range []string{bingo.ObjectLayoutX8664Triple, bingo.ObjectLayoutAArch64Triple} {
		t.Run(triple, func(t *testing.T) {
			target, err := llvm.GetTargetFromTriple(triple)
			if err != nil {
				t.Fatal(err)
			}
			machine := target.CreateTargetMachine(triple, "generic", "", llvm.CodeGenLevelNone, llvm.RelocPIC, llvm.CodeModelSmall)
			defer machine.Dispose()
			data := machine.CreateTargetData()
			defer data.Dispose()
			wantTarget, err := bingo.CanonicalObjectLayoutTarget(triple)
			if err != nil {
				t.Fatal(err)
			}
			if data.String() != wantTarget.DataLayout {
				t.Fatalf("observed DataLayout = %q, want %q", data.String(), wantTarget.DataLayout)
			}

			ctx := llvm.NewContext()
			defer ctx.Dispose()
			pointer := llvm.PointerType(ctx.Int8Type(), 0)
			u8, u16, u32, usize := ctx.Int8Type(), ctx.Int16Type(), ctx.Int32Type(), ctx.Int64Type()
			blocks := []struct {
				name    string
				typeDef llvm.Type
				size    uint64
				align   int
				offsets []uint64
			}{
				{"header", ctx.StructType([]llvm.Type{pointer, usize, usize}, false), 24, 8, []uint64{0, 8, 16}},
				{"shape", ctx.StructType([]llvm.Type{u32, u32, usize, usize, u32, u32, pointer, pointer}, false), 48, 8, []uint64{0, 4, 8, 16, 24, 28, 32, 40}},
				{"property", ctx.StructType([]llvm.Type{pointer, u8, u8, u16, u32, u32, u32, u32, pointer}, false), 40, 8, []uint64{0, 8, 9, 10, 12, 16, 20, 24, 32}},
				{"trace", ctx.StructType([]llvm.Type{u32, u32, usize, u32, u32, pointer, pointer}, false), 40, 8, []uint64{0, 4, 8, 16, 20, 24, 32}},
			}
			for _, block := range blocks {
				if size, align := data.TypeAllocSize(block.typeDef), data.ABITypeAlignment(block.typeDef); size != block.size || align != block.align {
					t.Fatalf("%s extent = %d/%d, want %d/%d", block.name, size, align, block.size, block.align)
				}
				for index, want := range block.offsets {
					if got := data.ElementOffset(block.typeDef, index); got != want {
						t.Fatalf("%s field %d offset = %d, want %d", block.name, index, got, want)
					}
				}
			}
		})
	}
}
