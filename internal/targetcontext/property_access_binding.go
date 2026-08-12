package targetcontext

import (
	"fmt"
	"slices"
	"strings"

	"github.com/microsoft/typescript-go/internal/bingo"
)

func BindPropertyAccessMIR(module bingo.PropertyAccessMIRArtifact, context TargetContext, catalog AvailableCapabilityCatalog) (bingo.PropertyAccessBoundMIR, error) {
	if err := bingo.VerifyCanonicalPropertyAccessMIR(module); err != nil {
		return bingo.PropertyAccessBoundMIR{}, err
	}
	if err := ValidateTargetContext(context); err != nil {
		return bingo.PropertyAccessBoundMIR{}, err
	}
	if err := ValidateAvailableCapabilityCatalog(catalog); err != nil {
		return bingo.PropertyAccessBoundMIR{}, err
	}
	if context.AvailableCapabilityCatalogHash != catalog.ContentHash || context.RequestHash != catalog.RequestHash || context.RuntimeManifestHash != catalog.RuntimeManifestHash || context.ToolchainManifestHash != catalog.ToolchainManifestHash {
		return bingo.PropertyAccessBoundMIR{}, fmt.Errorf("property access target context and catalog disagree")
	}
	if module.TargetTriple != context.Triple || module.DataLayoutHash != context.DataLayoutHash {
		return bingo.PropertyAccessBoundMIR{}, fmt.Errorf("property access MIR target does not match TargetContext")
	}
	index, ok := slices.BinarySearchFunc(catalog.Capabilities, bingo.DynamicPropertyLoadCapability, func(capability AvailableCapability, logical bingo.RuntimeCapabilityID) int {
		return strings.Compare(string(capability.LogicalName), string(logical))
	})
	if !ok {
		return bingo.PropertyAccessBoundMIR{}, fmt.Errorf("runtime capability %q is unavailable", bingo.DynamicPropertyLoadCapability)
	}
	capability := catalog.Capabilities[index]
	if capability.SymbolName != bingo.DynamicPropertyLoadSymbol {
		return bingo.PropertyAccessBoundMIR{}, fmt.Errorf("dynamic property load symbol mismatch")
	}
	if capability.SignatureHash != module.DynamicABI.SignatureHash {
		return bingo.PropertyAccessBoundMIR{}, fmt.Errorf("dynamic property load signature mismatch")
	}
	return bingo.NewPropertyAccessBoundMIR(module, context.ContentHash, catalog.ContentHash, bingo.BoundCapability{LogicalName: capability.LogicalName, SymbolName: capability.SymbolName, SignatureHash: capability.SignatureHash})
}
