//go:build !llvm20 || !cgo || !linux

package llvmbackend

func openFirstSliceTargetMachine() (*TargetMachine, error) { return nil, ErrLLVMUnavailable }
