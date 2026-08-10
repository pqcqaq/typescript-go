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

	"github.com/microsoft/typescript-go/internal/bingo"
	jsonx "github.com/microsoft/typescript-go/internal/json"
)

const (
	ToolchainManifestSchemaVersion  uint32 = 1
	DataLayoutSchemaVersion         uint32 = 1
	FirstSliceEmissionSchemaVersion uint32 = 1
	LockedLLVMMajor                        = 20
	LockedLLVMVersion                      = "20.1.8"
	FirstSliceTriple                       = "x86_64-unknown-linux-gnu"
	FirstSliceCPU                          = "generic"
	FirstSliceDataLayout                   = "e-m:e-p270:32:32-p271:32:32-p272:64:64-i64:64-i128:128-f80:128-n8:16:32:64-S128"
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

// CanonicalBytes returns the validated serialized layout artifact.
func (layout DataLayout) CanonicalBytes() ([]byte, error) {
	if err := ValidateDataLayout(layout); err != nil {
		return nil, err
	}
	return json.Marshal(layout)
}

// Digest returns the validated layout identity embedded in the manifest.
func (layout DataLayout) Digest() string { return layout.ContentHash }

// DecodeDataLayout strictly decodes and validates a serialized layout.
func DecodeDataLayout(data []byte) (*DataLayout, error) {
	var layout DataLayout
	if err := jsonx.Unmarshal(data, &layout, jsonx.RejectUnknownMembers(true)); err != nil {
		return nil, fmt.Errorf("decode data layout: %w", err)
	}
	if err := ValidateDataLayout(layout); err != nil {
		return nil, err
	}
	return &layout, nil
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

// DecodeToolchainManifest strictly decodes and validates a serialized manifest.
func DecodeToolchainManifest(data []byte) (*ToolchainManifest, error) {
	var manifest ToolchainManifest
	if err := jsonx.Unmarshal(data, &manifest, jsonx.RejectUnknownMembers(true)); err != nil {
		return nil, fmt.Errorf("decode toolchain manifest: %w", err)
	}
	if err := ValidateToolchainManifest(manifest); err != nil {
		return nil, err
	}
	return &manifest, nil
}

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
	manifest       ToolchainManifest
	emit           func() ([]byte, error)
	emitFirstSlice func(bingo.FirstSliceMIRArtifact) (FirstSliceEmission, error)
	dispose        func()
}

// FirstSliceEmission binds verified MIR and the observed target machine to
// the exact LLVM text and object bytes produced by BE-002a.
type FirstSliceEmission struct {
	SchemaVersion         uint32 `json:"schemaVersion"`
	MIRContentHash        string `json:"mirContentHash"`
	ToolchainManifestHash string `json:"toolchainManifestHash"`
	TargetTriple          string `json:"targetTriple"`
	DataLayoutHash        string `json:"dataLayoutHash"`
	LLVMIRHash            string `json:"llvmIRHash"`
	ObjectHash            string `json:"objectHash"`
	ContentHash           string `json:"contentHash"`
	LLVMIR                []byte `json:"-"`
	Object                []byte `json:"-"`
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

// EmitFirstSliceObject accepts only final verified, capability-bound MIR and
// rejects any target/toolchain provenance that does not match this machine.
func (m *TargetMachine) EmitFirstSliceObject(module bingo.FirstSliceMIRArtifact) (FirstSliceEmission, error) {
	if m == nil || m.emitFirstSlice == nil {
		return FirstSliceEmission{}, ErrLLVMUnavailable
	}
	if err := bingo.VerifyBoundFirstSliceMIR(module); err != nil {
		return FirstSliceEmission{}, fmt.Errorf("verify final MIR before LLVM lowering: %w", err)
	}
	if module.Provenance.ToolchainManifestHash != m.manifest.ContentHash ||
		module.Provenance.DataLayoutHash != m.manifest.DataLayout.ContentHash {
		return FirstSliceEmission{}, fmt.Errorf("MIR target provenance does not match observed TargetMachine")
	}
	if !supportedPrimitiveFunctionSet(module.Functions) {
		return FirstSliceEmission{}, fmt.Errorf("primitive C ABI requires a supported verified function set")
	}
	emission, err := m.emitFirstSlice(module)
	if err != nil {
		return FirstSliceEmission{}, err
	}
	if err := VerifyFirstSliceEmission(emission); err != nil {
		return FirstSliceEmission{}, err
	}
	return emission, nil
}

func supportedPrimitiveFunctionSet(functions []bingo.FirstSliceMIRFunction) bool {
	if len(functions) == 1 {
		return functions[0].Name == "add" || functions[0].Name == "choose"
	}
	return len(functions) == 2 && functions[0].Name == "add" && functions[1].Name == "compute" && !functions[0].Exported && functions[1].Exported
}

func (emission FirstSliceEmission) CanonicalBytes() ([]byte, error) {
	if err := VerifyFirstSliceEmission(emission); err != nil {
		return nil, err
	}
	return json.Marshal(struct {
		SchemaVersion         uint32 `json:"schemaVersion"`
		MIRContentHash        string `json:"mirContentHash"`
		ToolchainManifestHash string `json:"toolchainManifestHash"`
		TargetTriple          string `json:"targetTriple"`
		DataLayoutHash        string `json:"dataLayoutHash"`
		LLVMIRHash            string `json:"llvmIRHash"`
		ObjectHash            string `json:"objectHash"`
		ContentHash           string `json:"contentHash"`
	}{emission.SchemaVersion, emission.MIRContentHash, emission.ToolchainManifestHash, emission.TargetTriple, emission.DataLayoutHash, emission.LLVMIRHash, emission.ObjectHash, emission.ContentHash})
}

func VerifyFirstSliceEmission(emission FirstSliceEmission) error {
	if emission.SchemaVersion != FirstSliceEmissionSchemaVersion || emission.TargetTriple != FirstSliceTriple {
		return fmt.Errorf("unsupported first-slice emission identity")
	}
	for _, item := range []struct {
		name   string
		digest string
	}{
		{name: "MIR", digest: emission.MIRContentHash},
		{name: "toolchain", digest: emission.ToolchainManifestHash},
		{name: "data layout", digest: emission.DataLayoutHash},
		{name: "LLVM IR", digest: emission.LLVMIRHash},
		{name: "object", digest: emission.ObjectHash},
		{name: "content", digest: emission.ContentHash},
	} {
		if !validDigest(item.digest) {
			return fmt.Errorf("invalid %s digest %q", item.name, item.digest)
		}
	}
	if len(emission.LLVMIR) == 0 || hashBytes(emission.LLVMIR) != emission.LLVMIRHash {
		return fmt.Errorf("LLVM IR bytes do not match emission identity")
	}
	if len(emission.Object) == 0 || hashBytes(emission.Object) != emission.ObjectHash {
		return fmt.Errorf("object bytes do not match emission identity")
	}
	want, err := firstSliceEmissionContentHash(emission)
	if err != nil {
		return err
	}
	if emission.ContentHash != want {
		return fmt.Errorf("first-slice emission content hash mismatch: got %s want %s", emission.ContentHash, want)
	}
	return nil
}

func newFirstSliceEmission(module bingo.FirstSliceMIRArtifact, manifest ToolchainManifest, llvmIR, object []byte) (FirstSliceEmission, error) {
	emission := FirstSliceEmission{
		SchemaVersion:         FirstSliceEmissionSchemaVersion,
		MIRContentHash:        module.ContentHash,
		ToolchainManifestHash: manifest.ContentHash,
		TargetTriple:          manifest.TargetTriple,
		DataLayoutHash:        manifest.DataLayout.ContentHash,
		LLVMIRHash:            hashBytes(llvmIR),
		ObjectHash:            hashBytes(object),
		LLVMIR:                slices.Clone(llvmIR),
		Object:                slices.Clone(object),
	}
	var err error
	emission.ContentHash, err = firstSliceEmissionContentHash(emission)
	if err != nil {
		return FirstSliceEmission{}, err
	}
	if err := VerifyFirstSliceEmission(emission); err != nil {
		return FirstSliceEmission{}, err
	}
	return emission, nil
}

func firstSliceEmissionContentHash(emission FirstSliceEmission) (string, error) {
	return hashManifest(struct {
		SchemaVersion         uint32 `json:"schemaVersion"`
		MIRContentHash        string `json:"mirContentHash"`
		ToolchainManifestHash string `json:"toolchainManifestHash"`
		TargetTriple          string `json:"targetTriple"`
		DataLayoutHash        string `json:"dataLayoutHash"`
		LLVMIRHash            string `json:"llvmIRHash"`
		ObjectHash            string `json:"objectHash"`
	}{emission.SchemaVersion, emission.MIRContentHash, emission.ToolchainManifestHash, emission.TargetTriple, emission.DataLayoutHash, emission.LLVMIRHash, emission.ObjectHash})
}

func hashBytes(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func validDigest(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	for _, char := range value {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}

func (m *TargetMachine) Close() {
	if m != nil && m.dispose != nil {
		m.dispose()
		m.dispose = nil
		m.emit = nil
		m.emitFirstSlice = nil
	}
}

func OpenFirstSliceTargetMachine() (*TargetMachine, error) { return openFirstSliceTargetMachine() }
