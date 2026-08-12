package targetcontext

import (
	"bytes"
	"fmt"
	"slices"

	"github.com/microsoft/typescript-go/internal/bingo"
	"github.com/microsoft/typescript-go/internal/buildplan"
	"github.com/microsoft/typescript-go/internal/frontendwire"
	jsonx "github.com/microsoft/typescript-go/internal/json"
	"github.com/microsoft/typescript-go/internal/llvmbackend"
)

const (
	TargetContextSchemaVersion              uint32 = 1
	AvailableCapabilityCatalogSchemaVersion uint32 = 1
)

// AvailableCapability is one runtime implementation available for later MIR binding.
type AvailableCapability struct {
	LogicalName        bingo.RuntimeCapabilityID `json:"logicalName"`
	SymbolName         string                    `json:"symbolName"`
	ABIVersion         string                    `json:"abiVersion"`
	Signature          string                    `json:"signature"`
	Effects            []bingo.PassEffect        `json:"effects"`
	RequiredFeatures   []string                  `json:"requiredFeatures"`
	SignatureHash      string                    `json:"signatureHash"`
	ImplementationHash string                    `json:"implementationHash"`
}

// AvailableCapabilityCatalog is the runtime's available implementation set.
// It is deliberately distinct from the MIR-derived bound capability closure.
type AvailableCapabilityCatalog struct {
	SchemaVersion         uint32                `json:"schemaVersion"`
	RequestHash           string                `json:"requestHash"`
	Runtime               string                `json:"runtime"`
	RuntimeABIVersion     uint32                `json:"runtimeABIVersion"`
	ToolchainManifestHash string                `json:"toolchainManifestHash"`
	RuntimeManifestHash   string                `json:"runtimeManifestHash"`
	Capabilities          []AvailableCapability `json:"capabilities"`
	ContentHash           string                `json:"contentHash"`
}

// TargetContext is the immutable, resolved target identity consumed by every
// representation, MIR, LLVM, object, and linker stage in the first slice.
type TargetContext struct {
	SchemaVersion                  uint32   `json:"schemaVersion"`
	RequestHash                    string   `json:"requestHash"`
	FrontendHash                   string   `json:"frontendHash"`
	Triple                         string   `json:"triple"`
	CPU                            string   `json:"cpu"`
	Features                       []string `json:"features"`
	LLVMMajor                      int      `json:"llvmMajor"`
	LLVMVersion                    string   `json:"llvmVersion"`
	LLVMDataLayout                 string   `json:"llvmDataLayout"`
	DataLayoutHash                 string   `json:"dataLayoutHash"`
	PointerWidth                   uint32   `json:"pointerWidth"`
	Endian                         string   `json:"endian"`
	CCallingConvention             string   `json:"cCallingConvention"`
	ExceptionModel                 string   `json:"exceptionModel"`
	ObjectFormat                   string   `json:"objectFormat"`
	TLSModel                       string   `json:"tlsModel"`
	RelocationModel                string   `json:"relocationModel"`
	CodeModel                      string   `json:"codeModel"`
	OptLevel                       string   `json:"optLevel"`
	Sanitizers                     []string `json:"sanitizers"`
	Profile                        string   `json:"profile"`
	Runtime                        string   `json:"runtime"`
	RuntimeABIVersion              uint32   `json:"runtimeABIVersion"`
	GC                             string   `json:"gc"`
	Overflow                       string   `json:"overflow"`
	BoundsCheck                    string   `json:"boundsCheck"`
	ToolchainManifestHash          string   `json:"toolchainManifestHash"`
	RuntimeManifestHash            string   `json:"runtimeManifestHash"`
	RuntimeSourceHash              string   `json:"runtimeSourceHash"`
	ABISchemaHash                  string   `json:"abiSchemaHash"`
	TargetManifestHash             string   `json:"targetManifestHash"`
	AvailableCapabilityCatalogHash string   `json:"availableCapabilityCatalogHash"`
	ContentHash                    string   `json:"contentHash"`
}

// Resolution groups the immutable target context and its independently typed proofs.
type Resolution struct {
	Context    TargetContext
	DataLayout llvmbackend.DataLayout
	Catalog    AvailableCapabilityCatalog
	Toolchain  llvmbackend.ToolchainManifest
	Runtime    RuntimeManifest
}

// ResolveTargetContext binds an unresolved BuildPlan to an observed LLVM
// TargetMachine and one locked runtime manifest.
func ResolveTargetContext(plan buildplan.Plan, machine *llvmbackend.TargetMachine, runtimeManifest []byte) (Resolution, error) {
	if machine == nil {
		return Resolution{}, fmt.Errorf("target machine is nil")
	}
	manifest := machine.Manifest()
	if err := llvmbackend.ValidateToolchainManifest(manifest); err != nil {
		return Resolution{}, fmt.Errorf("observe LLVM target machine: %w", err)
	}
	return resolveTargetContext(plan, manifest, runtimeManifest)
}

func resolveTargetContext(plan buildplan.Plan, toolchain llvmbackend.ToolchainManifest, runtimeBytes []byte) (Resolution, error) {
	if err := buildplan.Validate(plan); err != nil {
		return Resolution{}, fmt.Errorf("validate build plan: %w", err)
	}
	if err := llvmbackend.ValidateToolchainManifest(toolchain); err != nil {
		return Resolution{}, fmt.Errorf("validate toolchain manifest: %w", err)
	}
	runtimeManifest, err := DecodeRuntimeManifest(runtimeBytes)
	if err != nil {
		return Resolution{}, err
	}
	if err := validateRequestedImplementation(plan, toolchain, *runtimeManifest); err != nil {
		return Resolution{}, err
	}
	catalog, err := newAvailableCapabilityCatalog(plan, toolchain, *runtimeManifest)
	if err != nil {
		return Resolution{}, err
	}
	context, err := newTargetContext(plan, toolchain, *runtimeManifest, catalog)
	if err != nil {
		return Resolution{}, err
	}
	return Resolution{Context: context, DataLayout: toolchain.DataLayout, Catalog: catalog, Toolchain: toolchain, Runtime: *runtimeManifest}, nil
}

func validateRequestedImplementation(plan buildplan.Plan, toolchain llvmbackend.ToolchainManifest, runtime RuntimeManifest) error {
	request := plan.Backend
	if (plan.Profile != frontendwire.ProfileStatic && plan.Profile != frontendwire.ProfileInterop) || request.Target != llvmbackend.FirstSliceTriple || request.CPU != llvmbackend.FirstSliceCPU ||
		len(request.Features) != 0 || request.Runtime != LockedRuntimeName ||
		request.GC != frontendwire.GCTracing || request.Exceptions != frontendwire.ExceptionsNone ||
		request.Overflow != frontendwire.OverflowJSNumber || request.BoundsCheck != frontendwire.BoundsCheckOn ||
		request.LLVMMajor != llvmbackend.LockedLLVMMajor {
		return fmt.Errorf("build plan is unavailable for the first-slice target: profile=%s target=%s cpu=%s features=%v runtime=%s gc=%s exceptions=%s overflow=%s bounds=%s llvm=%d", plan.Profile, request.Target, request.CPU, request.Features, request.Runtime, request.GC, request.Exceptions, request.Overflow, request.BoundsCheck, request.LLVMMajor)
	}
	if request.Target != toolchain.TargetTriple || request.CPU != toolchain.CPU || !slices.Equal(request.Features, toolchain.Features) || request.LLVMMajor != toolchain.LLVMMajor {
		return fmt.Errorf("build plan does not match observed toolchain manifest")
	}
	if request.Target != runtime.Target.Triple || request.CPU != runtime.Target.CPU || !slices.Equal(request.Features, runtime.Target.Features) ||
		request.Runtime != runtime.Runtime || string(plan.Profile) != runtime.Profile || string(request.GC) != runtime.GC || string(request.Exceptions) != runtime.Exceptions ||
		toolchain.ObjectFormat != runtime.Target.ObjectFormat {
		return fmt.Errorf("build plan or toolchain does not match runtime manifest")
	}
	if toolchain.DataLayout.Triple != request.Target {
		return fmt.Errorf("LLVM data layout triple does not match build plan")
	}
	return nil
}

func newAvailableCapabilityCatalog(plan buildplan.Plan, toolchain llvmbackend.ToolchainManifest, runtime RuntimeManifest) (AvailableCapabilityCatalog, error) {
	capabilities := make([]AvailableCapability, len(runtime.Capabilities))
	for index, capability := range runtime.Capabilities {
		capabilities[index] = AvailableCapability{
			LogicalName:        capability.LogicalName,
			SymbolName:         capability.SymbolName,
			ABIVersion:         capability.ABIVersion,
			Signature:          capability.Signature,
			Effects:            slices.Clone(capability.Effects),
			RequiredFeatures:   slices.Clone(capability.RequiredFeatures),
			SignatureHash:      capability.SignatureHash,
			ImplementationHash: capability.ImplementationHash,
		}
	}
	catalog := AvailableCapabilityCatalog{
		SchemaVersion:         AvailableCapabilityCatalogSchemaVersion,
		RequestHash:           plan.ContentHash,
		Runtime:               runtime.Runtime,
		RuntimeABIVersion:     runtime.RuntimeABIVersion,
		ToolchainManifestHash: toolchain.ContentHash,
		RuntimeManifestHash:   runtime.ContentHash,
		Capabilities:          capabilities,
	}
	digest, err := availableCapabilityCatalogContentHash(catalog)
	if err != nil {
		return AvailableCapabilityCatalog{}, fmt.Errorf("hash available capability catalog: %w", err)
	}
	catalog.ContentHash = digest
	if err := ValidateAvailableCapabilityCatalog(catalog); err != nil {
		return AvailableCapabilityCatalog{}, err
	}
	return catalog, nil
}

func newTargetContext(plan buildplan.Plan, toolchain llvmbackend.ToolchainManifest, runtime RuntimeManifest, catalog AvailableCapabilityCatalog) (TargetContext, error) {
	context := TargetContext{
		SchemaVersion:                  TargetContextSchemaVersion,
		RequestHash:                    plan.ContentHash,
		FrontendHash:                   plan.FrontendHash,
		Triple:                         toolchain.TargetTriple,
		CPU:                            toolchain.CPU,
		Features:                       slices.Clone(toolchain.Features),
		LLVMMajor:                      toolchain.LLVMMajor,
		LLVMVersion:                    toolchain.LLVMVersion,
		LLVMDataLayout:                 toolchain.DataLayout.LayoutString,
		DataLayoutHash:                 toolchain.DataLayout.ContentHash,
		PointerWidth:                   toolchain.DataLayout.PointerBits,
		Endian:                         "little",
		CCallingConvention:             "sysv64",
		ExceptionModel:                 string(plan.Backend.Exceptions),
		ObjectFormat:                   toolchain.ObjectFormat,
		TLSModel:                       "none",
		RelocationModel:                toolchain.RelocationModel,
		CodeModel:                      toolchain.CodeModel,
		OptLevel:                       toolchain.OptLevel,
		Sanitizers:                     []string{},
		Profile:                        string(plan.Profile),
		Runtime:                        runtime.Runtime,
		RuntimeABIVersion:              runtime.RuntimeABIVersion,
		GC:                             string(plan.Backend.GC),
		Overflow:                       string(plan.Backend.Overflow),
		BoundsCheck:                    string(plan.Backend.BoundsCheck),
		ToolchainManifestHash:          toolchain.ContentHash,
		RuntimeManifestHash:            runtime.ContentHash,
		RuntimeSourceHash:              runtime.SourceHash,
		ABISchemaHash:                  runtime.ABISchemaHash,
		TargetManifestHash:             runtime.TargetManifestHash,
		AvailableCapabilityCatalogHash: catalog.ContentHash,
	}
	digest, err := targetContextContentHash(context)
	if err != nil {
		return TargetContext{}, fmt.Errorf("hash target context: %w", err)
	}
	context.ContentHash = digest
	if err := ValidateTargetContext(context); err != nil {
		return TargetContext{}, err
	}
	return context, nil
}

// DecodeAvailableCapabilityCatalog strictly decodes and validates a catalog.
func DecodeAvailableCapabilityCatalog(data []byte) (*AvailableCapabilityCatalog, error) {
	var catalog AvailableCapabilityCatalog
	if err := jsonx.Unmarshal(data, &catalog, jsonx.RejectUnknownMembers(true)); err != nil {
		return nil, fmt.Errorf("decode available capability catalog: %w", err)
	}
	if err := ValidateAvailableCapabilityCatalog(catalog); err != nil {
		return nil, err
	}
	return &catalog, nil
}

// CanonicalBytes returns the validated catalog wire representation.
func (catalog AvailableCapabilityCatalog) CanonicalBytes() ([]byte, error) {
	if err := ValidateAvailableCapabilityCatalog(catalog); err != nil {
		return nil, err
	}
	return canonicalJSON(catalog)
}

// ValidateAvailableCapabilityCatalog checks catalog structure and provenance.
func ValidateAvailableCapabilityCatalog(catalog AvailableCapabilityCatalog) error {
	profile, ok := lockedRuntimeProfileForManifestHash(catalog.RuntimeManifestHash)
	if !ok {
		return fmt.Errorf("available capability catalog does not bind a published runtime profile")
	}
	if catalog.SchemaVersion != AvailableCapabilityCatalogSchemaVersion || !isLowerDigest(catalog.RequestHash) ||
		catalog.Runtime != LockedRuntimeName || catalog.RuntimeABIVersion != LockedRuntimeABIVersion ||
		!isLowerDigest(catalog.ToolchainManifestHash) {
		return fmt.Errorf("invalid available capability catalog provenance")
	}
	wantedCapabilities, _ := runtimeCapabilitiesForProfile(profile)
	if len(catalog.Capabilities) != len(wantedCapabilities) {
		return fmt.Errorf("available capability count is %d, want %d", len(catalog.Capabilities), len(wantedCapabilities))
	}
	for index, capability := range catalog.Capabilities {
		want := wantedCapabilities[index]
		if capability.LogicalName != want.logicalName || capability.SymbolName != want.symbolName || capability.ABIVersion != "1.0.0" ||
			capability.Signature != want.signature || !slices.Equal(capability.Effects, want.effects) ||
			capability.RequiredFeatures == nil || len(capability.RequiredFeatures) != 0 ||
			!isLowerDigest(capability.SignatureHash) || !isLowerDigest(capability.ImplementationHash) {
			return fmt.Errorf("invalid available capability %d: %#v", index, capability)
		}
	}
	want, err := availableCapabilityCatalogContentHash(catalog)
	if err != nil {
		return err
	}
	if catalog.ContentHash != want {
		return fmt.Errorf("available capability catalog hash mismatch: got %s want %s", catalog.ContentHash, want)
	}
	return nil
}

// DecodeTargetContext strictly decodes and validates a target context.
func DecodeTargetContext(data []byte) (*TargetContext, error) {
	var context TargetContext
	if err := jsonx.Unmarshal(data, &context, jsonx.RejectUnknownMembers(true)); err != nil {
		return nil, fmt.Errorf("decode target context: %w", err)
	}
	if err := ValidateTargetContext(context); err != nil {
		return nil, err
	}
	return &context, nil
}

// CanonicalBytes returns the validated target context wire representation.
func (context TargetContext) CanonicalBytes() ([]byte, error) {
	if err := ValidateTargetContext(context); err != nil {
		return nil, err
	}
	return canonicalJSON(context)
}

// Digest returns the target context's validated content identity.
func (context TargetContext) Digest() string { return context.ContentHash }

// ValidateTargetContext checks the locked target and all manifest provenance.
func ValidateTargetContext(context TargetContext) error {
	if context.SchemaVersion != TargetContextSchemaVersion || !isLowerDigest(context.RequestHash) || !isLowerDigest(context.FrontendHash) {
		return fmt.Errorf("invalid target context request provenance")
	}
	if context.Triple != llvmbackend.FirstSliceTriple || context.CPU != llvmbackend.FirstSliceCPU || context.Features == nil || len(context.Features) != 0 ||
		context.LLVMMajor != llvmbackend.LockedLLVMMajor || context.LLVMVersion != llvmbackend.LockedLLVMVersion ||
		context.LLVMDataLayout != llvmbackend.FirstSliceDataLayout || !isLowerDigest(context.DataLayoutHash) || context.PointerWidth != 64 ||
		context.Endian != "little" || context.CCallingConvention != "sysv64" || context.ExceptionModel != "none" ||
		context.ObjectFormat != "elf" || context.TLSModel != "none" || context.RelocationModel != "pic" || context.CodeModel != "small" || context.OptLevel != "none" ||
		context.Sanitizers == nil || len(context.Sanitizers) != 0 {
		return fmt.Errorf("unsupported target context codegen identity")
	}
	if !isSupportedRuntimeProfile(context.Profile) || context.Runtime != LockedRuntimeName || context.RuntimeABIVersion != LockedRuntimeABIVersion ||
		context.GC != "tracing" || context.Overflow != "js-number" || context.BoundsCheck != "on" {
		return fmt.Errorf("unsupported target context runtime profile")
	}
	lockedProfile, published := lockedRuntimeProfileForManifestHash(context.RuntimeManifestHash)
	if !published || context.Profile != lockedProfile {
		return fmt.Errorf("target context does not bind a published runtime profile identity")
	}
	if !isLowerDigest(context.ToolchainManifestHash) ||
		context.RuntimeSourceHash != LockedRuntimeSourceHash || context.ABISchemaHash != LockedABISchemaHash || context.TargetManifestHash != LockedTargetManifestHash ||
		!isLowerDigest(context.AvailableCapabilityCatalogHash) {
		return fmt.Errorf("invalid target context manifest provenance")
	}
	want, err := targetContextContentHash(context)
	if err != nil {
		return err
	}
	if context.ContentHash != want {
		return fmt.Errorf("target context hash mismatch: got %s want %s", context.ContentHash, want)
	}
	return nil
}

func lockedRuntimeProfileForManifestHash(hash string) (string, bool) {
	if hash == LockedRuntimeManifestHash {
		return string(frontendwire.ProfileStatic), true
	}
	return "", false
}

func availableCapabilityCatalogContentHash(catalog AvailableCapabilityCatalog) (string, error) {
	return canonicalHash(struct {
		SchemaVersion         uint32                `json:"schemaVersion"`
		RequestHash           string                `json:"requestHash"`
		Runtime               string                `json:"runtime"`
		RuntimeABIVersion     uint32                `json:"runtimeABIVersion"`
		ToolchainManifestHash string                `json:"toolchainManifestHash"`
		RuntimeManifestHash   string                `json:"runtimeManifestHash"`
		Capabilities          []AvailableCapability `json:"capabilities"`
	}{catalog.SchemaVersion, catalog.RequestHash, catalog.Runtime, catalog.RuntimeABIVersion, catalog.ToolchainManifestHash, catalog.RuntimeManifestHash, catalog.Capabilities})
}

func targetContextContentHash(context TargetContext) (string, error) {
	return canonicalHash(struct {
		SchemaVersion                  uint32   `json:"schemaVersion"`
		RequestHash                    string   `json:"requestHash"`
		FrontendHash                   string   `json:"frontendHash"`
		Triple                         string   `json:"triple"`
		CPU                            string   `json:"cpu"`
		Features                       []string `json:"features"`
		LLVMMajor                      int      `json:"llvmMajor"`
		LLVMVersion                    string   `json:"llvmVersion"`
		LLVMDataLayout                 string   `json:"llvmDataLayout"`
		DataLayoutHash                 string   `json:"dataLayoutHash"`
		PointerWidth                   uint32   `json:"pointerWidth"`
		Endian                         string   `json:"endian"`
		CCallingConvention             string   `json:"cCallingConvention"`
		ExceptionModel                 string   `json:"exceptionModel"`
		ObjectFormat                   string   `json:"objectFormat"`
		TLSModel                       string   `json:"tlsModel"`
		RelocationModel                string   `json:"relocationModel"`
		CodeModel                      string   `json:"codeModel"`
		OptLevel                       string   `json:"optLevel"`
		Sanitizers                     []string `json:"sanitizers"`
		Profile                        string   `json:"profile"`
		Runtime                        string   `json:"runtime"`
		RuntimeABIVersion              uint32   `json:"runtimeABIVersion"`
		GC                             string   `json:"gc"`
		Overflow                       string   `json:"overflow"`
		BoundsCheck                    string   `json:"boundsCheck"`
		ToolchainManifestHash          string   `json:"toolchainManifestHash"`
		RuntimeManifestHash            string   `json:"runtimeManifestHash"`
		RuntimeSourceHash              string   `json:"runtimeSourceHash"`
		ABISchemaHash                  string   `json:"abiSchemaHash"`
		TargetManifestHash             string   `json:"targetManifestHash"`
		AvailableCapabilityCatalogHash string   `json:"availableCapabilityCatalogHash"`
	}{context.SchemaVersion, context.RequestHash, context.FrontendHash, context.Triple, context.CPU, context.Features, context.LLVMMajor, context.LLVMVersion, context.LLVMDataLayout, context.DataLayoutHash, context.PointerWidth, context.Endian, context.CCallingConvention, context.ExceptionModel, context.ObjectFormat, context.TLSModel, context.RelocationModel, context.CodeModel, context.OptLevel, context.Sanitizers, context.Profile, context.Runtime, context.RuntimeABIVersion, context.GC, context.Overflow, context.BoundsCheck, context.ToolchainManifestHash, context.RuntimeManifestHash, context.RuntimeSourceHash, context.ABISchemaHash, context.TargetManifestHash, context.AvailableCapabilityCatalogHash})
}

func equalCanonical(left, right any) bool {
	leftBytes, leftErr := canonicalJSON(left)
	rightBytes, rightErr := canonicalJSON(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftBytes, rightBytes)
}
