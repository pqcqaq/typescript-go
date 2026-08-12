package targetcontext

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"

	"github.com/microsoft/typescript-go/internal/bingo"
)

// BindObjectLayoutCopy closes an explicit-copy MIR over the observed target
// and the exact runtime allocation implementation used by that target.
func BindObjectLayoutCopy(module bingo.ObjectLayoutCopyMIRArtifact, context TargetContext, catalog AvailableCapabilityCatalog) (bingo.ObjectLayoutCopyBoundArtifact, error) {
	if err := bingo.VerifyCanonicalObjectLayoutCopyMIR(module); err != nil {
		return bingo.ObjectLayoutCopyBoundArtifact{}, err
	}
	if err := ValidateTargetContext(context); err != nil {
		return bingo.ObjectLayoutCopyBoundArtifact{}, err
	}
	if err := ValidateAvailableCapabilityCatalog(catalog); err != nil {
		return bingo.ObjectLayoutCopyBoundArtifact{}, err
	}
	if context.AvailableCapabilityCatalogHash != catalog.ContentHash || context.RequestHash != catalog.RequestHash || context.RuntimeManifestHash != catalog.RuntimeManifestHash {
		return bingo.ObjectLayoutCopyBoundArtifact{}, fmt.Errorf("object layout copy target context and capability catalog disagree")
	}
	layoutDigest := sha256.Sum256([]byte(context.LLVMDataLayout))
	if module.TargetTriple != context.Triple || module.DataLayoutHash != hex.EncodeToString(layoutDigest[:]) {
		return bingo.ObjectLayoutCopyBoundArtifact{}, fmt.Errorf("object layout copy MIR is not bound to TargetContext")
	}
	bindings := make([]bingo.BoundCapability, 0, len(bingo.ObjectLayoutCopyCapabilityRequirements()))
	for _, requirement := range bingo.ObjectLayoutCopyCapabilityRequirements() {
		index, ok := slices.BinarySearchFunc(catalog.Capabilities, requirement, func(capability AvailableCapability, logical bingo.RuntimeCapabilityID) int {
			return strings.Compare(string(capability.LogicalName), string(logical))
		})
		if !ok {
			return bingo.ObjectLayoutCopyBoundArtifact{}, fmt.Errorf("runtime capability %q is unavailable", requirement)
		}
		capability := catalog.Capabilities[index]
		bindings = append(bindings, bingo.BoundCapability{LogicalName: capability.LogicalName, SymbolName: capability.SymbolName, SignatureHash: capability.SignatureHash})
	}
	return bingo.NewObjectLayoutCopyBoundArtifact(module, context.ContentHash, catalog.ContentHash, bindings)
}

// BindCheckedObjectCast binds the explicit dynamic-boundary cast to one exact
// target and runtime capability. Until the locked runtime manifest publishes
// rt.object.shape_matches, this function deliberately fails as unavailable.
func BindCheckedObjectCast(cast bingo.CheckedObjectCastContract, context TargetContext, catalog AvailableCapabilityCatalog) (bingo.CheckedObjectCastBoundContract, error) {
	if err := bingo.VerifyCanonicalCheckedObjectCast(cast); err != nil {
		return bingo.CheckedObjectCastBoundContract{}, err
	}
	if err := ValidateTargetContext(context); err != nil {
		return bingo.CheckedObjectCastBoundContract{}, err
	}
	if err := ValidateAvailableCapabilityCatalog(catalog); err != nil {
		return bingo.CheckedObjectCastBoundContract{}, err
	}
	if context.AvailableCapabilityCatalogHash != catalog.ContentHash || context.RequestHash != catalog.RequestHash || context.Runtime != catalog.Runtime || context.RuntimeABIVersion != catalog.RuntimeABIVersion || context.ToolchainManifestHash != catalog.ToolchainManifestHash || context.RuntimeManifestHash != catalog.RuntimeManifestHash {
		return bingo.CheckedObjectCastBoundContract{}, fmt.Errorf("checked object cast target context and capability catalog disagree")
	}
	layoutDigest := sha256.Sum256([]byte(context.LLVMDataLayout))
	if cast.TargetLayout.Target.Triple != context.Triple || cast.TargetLayout.Target.DataLayout != context.LLVMDataLayout || cast.TargetLayout.Target.DataLayoutHash != hex.EncodeToString(layoutDigest[:]) {
		return bingo.CheckedObjectCastBoundContract{}, fmt.Errorf("checked object cast layout is not bound to target context")
	}
	index, ok := slices.BinarySearchFunc(catalog.Capabilities, bingo.CheckedObjectCastCapability, func(capability AvailableCapability, logical bingo.RuntimeCapabilityID) int {
		return strings.Compare(string(capability.LogicalName), string(logical))
	})
	if !ok {
		return bingo.CheckedObjectCastBoundContract{}, fmt.Errorf("runtime capability %q is unavailable", bingo.CheckedObjectCastCapability)
	}
	capability := catalog.Capabilities[index]
	return bingo.NewCheckedObjectCastBound(cast, context.ContentHash, catalog.ContentHash, bingo.BoundCapability{LogicalName: capability.LogicalName, SymbolName: capability.SymbolName, SignatureHash: capability.SignatureHash})
}

// BindVERT010MIR resolves every logical object-MIR requirement to the exact
// implementation in one validated target context catalog.
func BindVERT010MIR(module bingo.VERT010MIRModule, context TargetContext, catalog AvailableCapabilityCatalog) (bingo.VERT010BoundMIR, error) {
	if err := bingo.VerifyCanonicalVERT010MIR(module); err != nil {
		return bingo.VERT010BoundMIR{}, err
	}
	if err := ValidateTargetContext(context); err != nil {
		return bingo.VERT010BoundMIR{}, err
	}
	if err := ValidateAvailableCapabilityCatalog(catalog); err != nil {
		return bingo.VERT010BoundMIR{}, err
	}
	if context.AvailableCapabilityCatalogHash != catalog.ContentHash || context.RequestHash != catalog.RequestHash || context.Runtime != catalog.Runtime || context.RuntimeABIVersion != catalog.RuntimeABIVersion || context.ToolchainManifestHash != catalog.ToolchainManifestHash || context.RuntimeManifestHash != catalog.RuntimeManifestHash {
		return bingo.VERT010BoundMIR{}, fmt.Errorf("VERT-010 target context and capability catalog disagree")
	}
	layoutDigest := sha256.Sum256([]byte(context.LLVMDataLayout))
	if module.Layout.Target.Triple != context.Triple || module.Layout.Target.DataLayout != context.LLVMDataLayout || module.Layout.Target.DataLayoutHash != hex.EncodeToString(layoutDigest[:]) {
		return bingo.VERT010BoundMIR{}, fmt.Errorf("VERT-010 MIR layout is not bound to target context")
	}
	bindings := make([]bingo.BoundCapability, 0, len(module.LogicalCapabilityRequirements))
	for _, requirement := range module.LogicalCapabilityRequirements {
		index, ok := slices.BinarySearchFunc(catalog.Capabilities, requirement, func(capability AvailableCapability, logical bingo.RuntimeCapabilityID) int {
			if capability.LogicalName < logical {
				return -1
			}
			if capability.LogicalName > logical {
				return 1
			}
			return 0
		})
		if !ok {
			return bingo.VERT010BoundMIR{}, fmt.Errorf("runtime capability %q is unavailable", requirement)
		}
		capability := catalog.Capabilities[index]
		bindings = append(bindings, bingo.BoundCapability{LogicalName: requirement, SymbolName: capability.SymbolName, SignatureHash: capability.SignatureHash})
	}
	return bingo.NewVERT010BoundMIR(module, context.ContentHash, catalog.ContentHash, bindings)
}

// BindVERT011MIR resolves MIR v8 against the same validated GC capability
// catalog while preserving the independent VERT-011 artifact boundary.
func BindVERT011MIR(module bingo.VERT011MIRModule, context TargetContext, catalog AvailableCapabilityCatalog) (bingo.VERT011BoundMIR, error) {
	if err := bingo.VerifyCanonicalVERT011MIR(module); err != nil {
		return bingo.VERT011BoundMIR{}, err
	}
	if err := ValidateTargetContext(context); err != nil {
		return bingo.VERT011BoundMIR{}, err
	}
	if err := ValidateAvailableCapabilityCatalog(catalog); err != nil {
		return bingo.VERT011BoundMIR{}, err
	}
	if context.AvailableCapabilityCatalogHash != catalog.ContentHash || context.RequestHash != catalog.RequestHash || context.Runtime != catalog.Runtime || context.RuntimeABIVersion != catalog.RuntimeABIVersion || context.ToolchainManifestHash != catalog.ToolchainManifestHash || context.RuntimeManifestHash != catalog.RuntimeManifestHash {
		return bingo.VERT011BoundMIR{}, fmt.Errorf("VERT-011 target context and capability catalog disagree")
	}
	layoutDigest := sha256.Sum256([]byte(context.LLVMDataLayout))
	if module.Layout.Target.Triple != context.Triple || module.Layout.Target.DataLayout != context.LLVMDataLayout || module.Layout.Target.DataLayoutHash != hex.EncodeToString(layoutDigest[:]) {
		return bingo.VERT011BoundMIR{}, fmt.Errorf("VERT-011 MIR layout is not bound to target context")
	}
	bindings := make([]bingo.BoundCapability, 0, len(module.LogicalCapabilityRequirements))
	for _, requirement := range module.LogicalCapabilityRequirements {
		index, ok := slices.BinarySearchFunc(catalog.Capabilities, requirement, func(capability AvailableCapability, logical bingo.RuntimeCapabilityID) int {
			if capability.LogicalName < logical {
				return -1
			}
			if capability.LogicalName > logical {
				return 1
			}
			return 0
		})
		if !ok {
			return bingo.VERT011BoundMIR{}, fmt.Errorf("runtime capability %q is unavailable", requirement)
		}
		capability := catalog.Capabilities[index]
		bindings = append(bindings, bingo.BoundCapability{LogicalName: requirement, SymbolName: capability.SymbolName, SignatureHash: capability.SignatureHash})
	}
	return bingo.NewVERT011BoundMIR(module, context.ContentHash, catalog.ContentHash, bindings)
}

// BindVERT012MIR resolves closure MIR against the exact target context and runtime catalog.
func BindVERT012MIR(module bingo.VERT012MIRModule, context TargetContext, catalog AvailableCapabilityCatalog) (bingo.VERT012BoundMIR, error) {
	if err := bingo.VerifyCanonicalVERT012MIR(module); err != nil {
		return bingo.VERT012BoundMIR{}, err
	}
	if err := ValidateTargetContext(context); err != nil {
		return bingo.VERT012BoundMIR{}, err
	}
	if err := ValidateAvailableCapabilityCatalog(catalog); err != nil {
		return bingo.VERT012BoundMIR{}, err
	}
	if context.AvailableCapabilityCatalogHash != catalog.ContentHash || context.RequestHash != catalog.RequestHash || context.Runtime != catalog.Runtime || context.RuntimeABIVersion != catalog.RuntimeABIVersion || context.ToolchainManifestHash != catalog.ToolchainManifestHash || context.RuntimeManifestHash != catalog.RuntimeManifestHash {
		return bingo.VERT012BoundMIR{}, fmt.Errorf("VERT-012 target context and capability catalog disagree")
	}
	layoutDigest := sha256.Sum256([]byte(context.LLVMDataLayout))
	for _, layout := range module.Layouts {
		if layout.Contract.Target.Triple != context.Triple || layout.Contract.Target.DataLayout != context.LLVMDataLayout || layout.Contract.Target.DataLayoutHash != hex.EncodeToString(layoutDigest[:]) {
			return bingo.VERT012BoundMIR{}, fmt.Errorf("VERT-012 MIR layout is not bound to target context")
		}
	}
	bindings := make([]bingo.BoundCapability, 0, len(module.LogicalCapabilityRequirements))
	for _, requirement := range module.LogicalCapabilityRequirements {
		index, ok := slices.BinarySearchFunc(catalog.Capabilities, requirement, func(capability AvailableCapability, logical bingo.RuntimeCapabilityID) int {
			if capability.LogicalName < logical {
				return -1
			}
			if capability.LogicalName > logical {
				return 1
			}
			return 0
		})
		if !ok {
			return bingo.VERT012BoundMIR{}, fmt.Errorf("runtime capability %q is unavailable", requirement)
		}
		capability := catalog.Capabilities[index]
		bindings = append(bindings, bingo.BoundCapability{LogicalName: requirement, SymbolName: capability.SymbolName, SignatureHash: capability.SignatureHash})
	}
	return bingo.NewVERT012BoundMIR(module, context.ContentHash, catalog.ContentHash, bindings)
}

// BindVERT013aMIR resolves the base-class instance layout and exact GC runtime closure.
func BindVERT013aMIR(module bingo.VERT013aMIRModule, context TargetContext, catalog AvailableCapabilityCatalog) (bingo.VERT013aBoundMIR, error) {
	if err := bingo.VerifyCanonicalVERT013aMIR(module); err != nil {
		return bingo.VERT013aBoundMIR{}, err
	}
	if err := ValidateTargetContext(context); err != nil {
		return bingo.VERT013aBoundMIR{}, err
	}
	if err := ValidateAvailableCapabilityCatalog(catalog); err != nil {
		return bingo.VERT013aBoundMIR{}, err
	}
	if context.AvailableCapabilityCatalogHash != catalog.ContentHash || context.RequestHash != catalog.RequestHash || context.Runtime != catalog.Runtime || context.RuntimeABIVersion != catalog.RuntimeABIVersion || context.ToolchainManifestHash != catalog.ToolchainManifestHash || context.RuntimeManifestHash != catalog.RuntimeManifestHash {
		return bingo.VERT013aBoundMIR{}, fmt.Errorf("VERT-013a target context and capability catalog disagree")
	}
	layoutDigest := sha256.Sum256([]byte(context.LLVMDataLayout))
	if module.Layout.Target.Triple != context.Triple || module.Layout.Target.DataLayout != context.LLVMDataLayout || module.Layout.Target.DataLayoutHash != hex.EncodeToString(layoutDigest[:]) {
		return bingo.VERT013aBoundMIR{}, fmt.Errorf("VERT-013a MIR layout is not bound to target context")
	}
	bindings := make([]bingo.BoundCapability, 0, len(module.LogicalCapabilityRequirements))
	for _, requirement := range module.LogicalCapabilityRequirements {
		index, ok := slices.BinarySearchFunc(catalog.Capabilities, requirement, func(capability AvailableCapability, logical bingo.RuntimeCapabilityID) int {
			if capability.LogicalName < logical {
				return -1
			}
			if capability.LogicalName > logical {
				return 1
			}
			return 0
		})
		if !ok {
			return bingo.VERT013aBoundMIR{}, fmt.Errorf("runtime capability %q is unavailable", requirement)
		}
		capability := catalog.Capabilities[index]
		bindings = append(bindings, bingo.BoundCapability{LogicalName: requirement, SymbolName: capability.SymbolName, SignatureHash: capability.SignatureHash})
	}
	return bingo.NewVERT013aBoundMIR(module, context.ContentHash, catalog.ContentHash, bindings)
}

// BindVERT013bMIR binds both base and derived instance layouts plus the exact
// runtime capability closure to one validated target context.
func BindVERT013bMIR(module bingo.VERT013bMIRModule, context TargetContext, catalog AvailableCapabilityCatalog) (bingo.VERT013bBoundMIR, error) {
	if err := bingo.VerifyCanonicalVERT013bMIR(module); err != nil {
		return bingo.VERT013bBoundMIR{}, err
	}
	if err := ValidateTargetContext(context); err != nil {
		return bingo.VERT013bBoundMIR{}, err
	}
	if err := ValidateAvailableCapabilityCatalog(catalog); err != nil {
		return bingo.VERT013bBoundMIR{}, err
	}
	if context.AvailableCapabilityCatalogHash != catalog.ContentHash || context.RequestHash != catalog.RequestHash || context.Runtime != catalog.Runtime || context.RuntimeABIVersion != catalog.RuntimeABIVersion || context.ToolchainManifestHash != catalog.ToolchainManifestHash || context.RuntimeManifestHash != catalog.RuntimeManifestHash {
		return bingo.VERT013bBoundMIR{}, fmt.Errorf("VERT-013b target context and capability catalog disagree")
	}
	layoutDigest := sha256.Sum256([]byte(context.LLVMDataLayout))
	wantLayoutHash := hex.EncodeToString(layoutDigest[:])
	for _, layout := range []bingo.ObjectLayoutContract{module.Layout.Base, module.Layout.Derived} {
		if layout.Target.Triple != context.Triple || layout.Target.DataLayout != context.LLVMDataLayout || layout.Target.DataLayoutHash != wantLayoutHash {
			return bingo.VERT013bBoundMIR{}, fmt.Errorf("VERT-013b MIR layout is not bound to target context")
		}
	}
	bindings := make([]bingo.BoundCapability, 0, len(module.LogicalCapabilityRequirements))
	for _, requirement := range module.LogicalCapabilityRequirements {
		index, ok := slices.BinarySearchFunc(catalog.Capabilities, requirement, func(capability AvailableCapability, logical bingo.RuntimeCapabilityID) int {
			if capability.LogicalName < logical {
				return -1
			}
			if capability.LogicalName > logical {
				return 1
			}
			return 0
		})
		if !ok {
			return bingo.VERT013bBoundMIR{}, fmt.Errorf("runtime capability %q is unavailable", requirement)
		}
		capability := catalog.Capabilities[index]
		bindings = append(bindings, bingo.BoundCapability{LogicalName: requirement, SymbolName: capability.SymbolName, SignatureHash: capability.SignatureHash})
	}
	return bingo.NewVERT013bBoundMIR(module, context.ContentHash, catalog.ContentHash, bindings)
}

// BindClassAccessMIR closes structural MIR v13 over the post-layout GC plan
// and exact runtime capability catalog. Visibility itself has no runtime ABI.
func BindClassAccessMIR(layout bingo.ClassAccessLayoutContract, context TargetContext, catalog AvailableCapabilityCatalog) (bingo.ClassAccessBoundMIR, error) {
	if err := bingo.VerifyCanonicalClassAccessLayout(layout); err != nil {
		return bingo.ClassAccessBoundMIR{}, err
	}
	if err := ValidateTargetContext(context); err != nil {
		return bingo.ClassAccessBoundMIR{}, err
	}
	if err := ValidateAvailableCapabilityCatalog(catalog); err != nil {
		return bingo.ClassAccessBoundMIR{}, err
	}
	if layout.MIR.Target.TargetContextHash != context.ContentHash || context.AvailableCapabilityCatalogHash != catalog.ContentHash || context.RequestHash != catalog.RequestHash || context.Runtime != catalog.Runtime || context.RuntimeABIVersion != catalog.RuntimeABIVersion || context.ToolchainManifestHash != catalog.ToolchainManifestHash || context.RuntimeManifestHash != catalog.RuntimeManifestHash {
		return bingo.ClassAccessBoundMIR{}, fmt.Errorf("OBJ-003b target context and capability catalog disagree")
	}
	layoutDigest := sha256.Sum256([]byte(context.LLVMDataLayout))
	wantLayoutHash := hex.EncodeToString(layoutDigest[:])
	for _, objectLayout := range []bingo.ObjectLayoutContract{layout.Base, layout.Derived} {
		if objectLayout.Target.Triple != context.Triple || objectLayout.Target.DataLayout != context.LLVMDataLayout || objectLayout.Target.DataLayoutHash != wantLayoutHash {
			return bingo.ClassAccessBoundMIR{}, fmt.Errorf("OBJ-003b layout is not bound to target context")
		}
	}
	requirements := layout.MIR.HIR.LogicalCapabilityRequirements
	bindings := make([]bingo.BoundCapability, 0, len(requirements))
	for _, requirement := range requirements {
		index, ok := slices.BinarySearchFunc(catalog.Capabilities, requirement, func(capability AvailableCapability, logical bingo.RuntimeCapabilityID) int {
			if capability.LogicalName < logical {
				return -1
			}
			if capability.LogicalName > logical {
				return 1
			}
			return 0
		})
		if !ok {
			return bingo.ClassAccessBoundMIR{}, fmt.Errorf("runtime capability %q is unavailable", requirement)
		}
		capability := catalog.Capabilities[index]
		bindings = append(bindings, bingo.BoundCapability{LogicalName: requirement, SymbolName: capability.SymbolName, SignatureHash: capability.SignatureHash})
	}
	return bingo.NewClassAccessBoundMIR(layout, context.ContentHash, catalog.ContentHash, bindings)
}

// LowerClassAccessMIR binds HIR v15 execution and authorization proofs to one
// validated target identity without selecting a physical field offset.
func LowerClassAccessMIR(hir bingo.HIRModule, context TargetContext) (bingo.ClassAccessMIRModule, error) {
	if err := bingo.VerifyCanonicalClassAccessHIR(hir); err != nil {
		return bingo.ClassAccessMIRModule{}, err
	}
	if err := ValidateTargetContext(context); err != nil {
		return bingo.ClassAccessMIRModule{}, err
	}
	if context.FrontendHash != hir.Provenance.FrontendSnapshotHash {
		return bingo.ClassAccessMIRModule{}, fmt.Errorf("OBJ-003b access HIR is not bound to target context frontend")
	}
	layoutDigest := sha256.Sum256([]byte(context.LLVMDataLayout))
	return bingo.LowerClassAccessMIR(hir, bingo.ClassAccessMIRTarget{
		TargetContextHash:  context.ContentHash,
		Triple:             context.Triple,
		DataLayoutHash:     context.DataLayoutHash,
		LLVMDataLayoutHash: hex.EncodeToString(layoutDigest[:]),
	})
}

// PlanClassAccessLayout joins an authorized MIR with the exact canonical
// object target observed by TargetContext before any field offset is consumed.
func PlanClassAccessLayout(module bingo.ClassAccessMIRModule, context TargetContext) (bingo.ClassAccessLayoutContract, error) {
	if err := bingo.VerifyCanonicalClassAccessMIR(module); err != nil {
		return bingo.ClassAccessLayoutContract{}, err
	}
	if err := ValidateTargetContext(context); err != nil {
		return bingo.ClassAccessLayoutContract{}, err
	}
	layoutDigest := sha256.Sum256([]byte(context.LLVMDataLayout))
	llvmLayoutHash := hex.EncodeToString(layoutDigest[:])
	if module.Target.TargetContextHash != context.ContentHash || module.Target.Triple != context.Triple || module.Target.DataLayoutHash != context.DataLayoutHash || module.Target.LLVMDataLayoutHash != llvmLayoutHash {
		return bingo.ClassAccessLayoutContract{}, fmt.Errorf("OBJ-003b access MIR target does not match TargetContext")
	}
	target, err := bingo.CanonicalObjectLayoutTarget(context.Triple)
	if err != nil {
		return bingo.ClassAccessLayoutContract{}, err
	}
	if target.DataLayout != context.LLVMDataLayout || target.DataLayoutHash != llvmLayoutHash || target.PointerBits != context.PointerWidth || target.LittleEndian != (context.Endian == "little") {
		return bingo.ClassAccessLayoutContract{}, fmt.Errorf("OBJ-003b canonical object target does not match TargetContext")
	}
	return bingo.PlanClassAccessLayout(module, target)
}
