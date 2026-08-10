// Package llvmbackend owns the narrow LLVM 20 target-machine boundary used by
// Phase 2A. The package deliberately exposes no HIR or MIR types: callers must
// resolve a target context before handing verified representation to LLVM.
package llvmbackend

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
)

const (
	ToolchainManifestSchemaVersion uint32 = 1
	DataLayoutSchemaVersion        uint32 = 1
	LockedLLVMMajor                       = 20
	LockedLLVMVersion                     = "20.1.8"
	FirstSliceTriple                      = "x86_64-unknown-linux-gnu"
	FirstSliceCPU                         = "generic"
	FirstSliceDataLayout                  = "e-m:e-p270:32:32-p271:32:32-p272:64:64-i64:64-i128:128-f80:128-n8:16:32:64-S128"
)

var ErrLLVMUnavailable = errors.New("LLVM 20 target machine is unavailable; build with -tags=llvm20 on Linux with LLVM 20")

// DataLayout is the structured subset needed by the first representation pass.
// LayoutString is copied from LLVM TargetMachine and is the authoritative value.
type DataLayout struct {
	SchemaVersion uint32 `json:"schemaVersion"`
	Triple        string `json:"triple"`
	LayoutString  string `json:"layoutString"`
	PointerBits   uint32 `json:"pointerBits"`
	LittleEndian  bool   `json:"littleEndian"`
	F64Bits       uint32 `json:"f64Bits"`
	F64ABIAlign   uint32 `json:"f64AbiAlign"`
	ContentHash   string `json:"contentHash"`
}

// ToolchainManifest freezes the toolchain facts required by ResolveTargetContext.
// It is an observed manifest, not a claim that runtime capabilities are bound.
type ToolchainManifest struct {
	SchemaVersion   uint32     `json:"schemaVersion"`
	LLVMVersion     string     `json:"llvmVersion"`
	LLVMMajor       int        `json:"llvmMajor"`
	TargetTriple    string     `json:"targetTriple"`
	CPU             string     `json:"cpu"`
	Features        []string   `json:"features,omitempty"`
	ObjectFormat    string     `json:"objectFormat"`
	RelocationModel string     `json:"relocationModel"`
	CodeModel       string     `json:"codeModel"`
	OptLevel        string     `json:"optLevel"`
	DataLayout      DataLayout `json:"dataLayout"`
	ContentHash     string     `json:"contentHash"`
}

func (m ToolchainManifest) CanonicalBytes() ([]byte, error) {
	if err := ValidateToolchainManifest(m); err != nil {
		return nil, err
	}
	return json.Marshal(m)
}

func (m ToolchainManifest) Digest() string { return m.ContentHash }

func ValidateToolchainManifest(m ToolchainManifest) error {
	if m.SchemaVersion != ToolchainManifestSchemaVersion {
		return fmt.Errorf("unsupported toolchain manifest schema %d", m.SchemaVersion)
	}
	if m.LLVMMajor != LockedLLVMMajor || m.LLVMVersion != LockedLLVMVersion {
		return fmt.Errorf("unsupported LLVM toolchain %s (major %d)", m.LLVMVersion, m.LLVMMajor)
	}
	if m.TargetTriple != FirstSliceTriple || m.CPU != FirstSliceCPU || len(m.Features) != 0 {
		return fmt.Errorf("unsupported first-slice target %q cpu %q features %v", m.TargetTriple, m.CPU, m.Features)
	}
	if m.ObjectFormat != "elf" || m.RelocationModel != "pic" || m.CodeModel != "small" || m.OptLevel != "none" {
		return fmt.Errorf("unsupported target codegen tuple: format=%s reloc=%s codeModel=%s opt=%s", m.ObjectFormat, m.RelocationModel, m.CodeModel, m.OptLevel)
	}
	if err := ValidateDataLayout(m.DataLayout); err != nil {
		return err
	}
	if m.ContentHash == "" {
		return errors.New("toolchain manifest content hash is empty")
	}
	want, err := hashManifest(struct {
		SchemaVersion   uint32     `json:"schemaVersion"`
		LLVMVersion     string     `json:"llvmVersion"`
		LLVMMajor       int        `json:"llvmMajor"`
		TargetTriple    string     `json:"targetTriple"`
		CPU             string     `json:"cpu"`
		Features        []string   `json:"features,omitempty"`
		ObjectFormat    string     `json:"objectFormat"`
		RelocationModel string     `json:"relocationModel"`
		CodeModel       string     `json:"codeModel"`
		OptLevel        string     `json:"optLevel"`
		DataLayout      DataLayout `json:"dataLayout"`
	}{m.SchemaVersion, m.LLVMVersion, m.LLVMMajor, m.TargetTriple, m.CPU, m.Features, m.ObjectFormat, m.RelocationModel, m.CodeModel, m.OptLevel, m.DataLayout})
	if err != nil {
		return fmt.Errorf("hash toolchain manifest: %w", err)
	}
	if m.ContentHash != want {
		return fmt.Errorf("toolchain manifest hash mismatch: got %s want %s", m.ContentHash, want)
	}
	return nil
}

func ValidateDataLayout(layout DataLayout) error {
	if layout.SchemaVersion != DataLayoutSchemaVersion || layout.Triple != FirstSliceTriple || layout.LayoutString != FirstSliceDataLayout {
		return fmt.Errorf("unexpected LLVM data layout: %#v", layout)
	}
	if layout.PointerBits != 64 || !layout.LittleEndian || layout.F64Bits != 64 || layout.F64ABIAlign != 8 {
		return fmt.Errorf("unsupported first-slice data layout facts: %#v", layout)
	}
	if layout.ContentHash == "" {
		return errors.New("data layout content hash is empty")
	}
	want, err := hashManifest(struct {
		SchemaVersion uint32 `json:"schemaVersion"`
		Triple        string `json:"triple"`
		LayoutString  string `json:"layoutString"`
		PointerBits   uint32 `json:"pointerBits"`
		LittleEndian  bool   `json:"littleEndian"`
		F64Bits       uint32 `json:"f64Bits"`
		F64ABIAlign   uint32 `json:"f64AbiAlign"`
	}{layout.SchemaVersion, layout.Triple, layout.LayoutString, layout.PointerBits, layout.LittleEndian, layout.F64Bits, layout.F64ABIAlign})
	if err != nil {
		return fmt.Errorf("hash data layout: %w", err)
	}
	if layout.ContentHash != want {
		return fmt.Errorf("data layout hash mismatch: got %s want %s", layout.ContentHash, want)
	}
	return nil
}

func newDataLayout(triple, layout string, pointerBits, f64Bits, f64Align uint32, littleEndian bool) DataLayout {
	result := DataLayout{SchemaVersion: DataLayoutSchemaVersion, Triple: triple, LayoutString: layout, PointerBits: pointerBits, LittleEndian: littleEndian, F64Bits: f64Bits, F64ABIAlign: f64Align}
	result.ContentHash, _ = hashManifest(struct {
		SchemaVersion uint32 `json:"schemaVersion"`
		Triple        string `json:"triple"`
		LayoutString  string `json:"layoutString"`
		PointerBits   uint32 `json:"pointerBits"`
		LittleEndian  bool   `json:"littleEndian"`
		F64Bits       uint32 `json:"f64Bits"`
		F64ABIAlign   uint32 `json:"f64AbiAlign"`
	}{result.SchemaVersion, result.Triple, result.LayoutString, result.PointerBits, result.LittleEndian, result.F64Bits, result.F64ABIAlign})
	return result
}

func newToolchainManifest(layout DataLayout) ToolchainManifest {
	result := ToolchainManifest{SchemaVersion: ToolchainManifestSchemaVersion, LLVMVersion: LockedLLVMVersion, LLVMMajor: LockedLLVMMajor, TargetTriple: FirstSliceTriple, CPU: FirstSliceCPU, Features: []string{}, ObjectFormat: "elf", RelocationModel: "pic", CodeModel: "small", OptLevel: "none", DataLayout: layout}
	result.ContentHash, _ = hashManifest(struct {
		SchemaVersion   uint32     `json:"schemaVersion"`
		LLVMVersion     string     `json:"llvmVersion"`
		LLVMMajor       int        `json:"llvmMajor"`
		TargetTriple    string     `json:"targetTriple"`
		CPU             string     `json:"cpu"`
		Features        []string   `json:"features,omitempty"`
		ObjectFormat    string     `json:"objectFormat"`
		RelocationModel string     `json:"relocationModel"`
		CodeModel       string     `json:"codeModel"`
		OptLevel        string     `json:"optLevel"`
		DataLayout      DataLayout `json:"dataLayout"`
	}{result.SchemaVersion, result.LLVMVersion, result.LLVMMajor, result.TargetTriple, result.CPU, result.Features, result.ObjectFormat, result.RelocationModel, result.CodeModel, result.OptLevel, result.DataLayout})
	return result
}

func hashManifest(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:]), nil
}

// TargetMachine is the only backend handle capable of emitting a first-slice
// object. It is intentionally opaque to the rest of the compiler.
type TargetMachine struct {
	manifest ToolchainManifest
	emit     func() ([]byte, error)
	dispose  func()
}

func (m *TargetMachine) Manifest() ToolchainManifest {
	if m == nil {
		return ToolchainManifest{}
	}
	result := m.manifest
	result.Features = slices.Clone(result.Features)
	return result
}

func (m *TargetMachine) EmitProbeObject() ([]byte, error) {
	if m == nil || m.emit == nil {
		return nil, ErrLLVMUnavailable
	}
	return m.emit()
}

func (m *TargetMachine) Close() {
	if m != nil && m.dispose != nil {
		m.dispose()
		m.dispose = nil
		m.emit = nil
	}
}

func OpenFirstSliceTargetMachine() (*TargetMachine, error) { return openFirstSliceTargetMachine() }
