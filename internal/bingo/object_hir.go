package bingo

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strconv"
	"strings"

	jsonx "github.com/microsoft/typescript-go/internal/json"
)

// VERT010HIRSchemaVersion is the first owned-object HIR reader major.
const VERT010HIRSchemaVersion uint32 = 9

var vert010Capabilities = []RuntimeCapabilityID{
	"rt.gc.alloc",
	"rt.gc.frame.link",
	"rt.gc.frame.unlink",
	"rt.gc.root.clear",
	"rt.gc.root.publish",
	"rt.gc.root.reload",
	"rt.gc.root.store",
	"rt.gc.safepoint",
}

// VERT010LogicalCapabilities returns the exact sorted runtime requirement set
// for the first owned-object slice. Callers receive an independent copy.
func VERT010LogicalCapabilities() []RuntimeCapabilityID {
	return slices.Clone(vert010Capabilities)
}

// VerifyVERT010ObjectTypes verifies the target-independent object table used
// by object HIR operations. It intentionally accepts only the first closed
// shape; expanding the language surface requires a new vertical slice.
func VerifyVERT010ObjectTypes(types []HIRObjectType) error {
	if len(types) != 1 {
		return fmt.Errorf("VERT-010 requires exactly one object type, got %d", len(types))
	}
	typ := types[0]
	if !validObjectSemanticTypeKey(typ.TypeKey) {
		return fmt.Errorf("VERT-010 object type key %q is invalid", typ.TypeKey)
	}
	if !validSHA256Hex(typ.SemanticContractHash) {
		return fmt.Errorf("VERT-010 semantic contract hash is invalid")
	}
	if len(typ.Properties) != 1 {
		return fmt.Errorf("VERT-010 requires exactly one property, got %d", len(typ.Properties))
	}
	property := typ.Properties[0]
	if property.Key != "value" || strings.TrimSpace(property.SymbolKey) == "" || !validSHA256Hex(property.SourceTypeKey) || property.Type != TypeNumber || !property.Mutable || !property.Required {
		return fmt.Errorf("VERT-010 property contract is invalid")
	}
	wantSemanticHash, err := VERT010ObjectSemanticContractHash(typ)
	if err != nil || typ.SemanticContractHash != wantSemanticHash {
		return fmt.Errorf("VERT-010 semantic contract hash mismatch")
	}
	return nil
}

// VERT010ObjectSemanticContractHash reconstructs the frozen semantic contract
// from HIR-visible fields so readers do not trust an opaque contract digest.
func VERT010ObjectSemanticContractHash(typ HIRObjectType) (string, error) {
	if len(typ.Properties) != 1 {
		return "", fmt.Errorf("VERT-010 requires exactly one property")
	}
	property := typ.Properties[0]
	contract := ObjectSemanticContract{
		SchemaVersion: ObjectSemanticContractSchemaVersion,
		TypeKey:       typ.TypeKey,
		Identity:      ObjectIdentityReference,
		Equality:      ObjectEqualityReference,
		Properties: []ObjectPropertyContract{{
			Key: property.Key, Kind: ObjectPropertyData, ReadTypeKey: property.SourceTypeKey, WriteTypeKey: property.SourceTypeKey, Visibility: "public",
		}},
	}
	_, hash, err := CanonicalObjectSemanticContract(contract)
	return hash, err
}

// VerifyVERT010ObjectOperationShape verifies object operation metadata without
// trusting operand definitions. The enclosing HIR verifier remains responsible
// for dominance, operand types, ordering, and exact capability closure.
func VerifyVERT010ObjectOperationShape(operation HIROp, objectTypes []HIRObjectType) error {
	if err := VerifyVERT010ObjectTypes(objectTypes); err != nil {
		return err
	}
	typ := objectTypes[0]
	property := typ.Properties[0]
	if operation.ObjectTypeKey != typ.TypeKey {
		return fmt.Errorf("object operation %d type key mismatch", operation.ID)
	}
	if operation.NumberBits != "" || operation.UTF16CodeUnits != "" || operation.Callee != 0 || len(operation.IncomingBlocks) != 0 || operation.Operator != "" {
		return fmt.Errorf("object operation %d carries unrelated primitive metadata", operation.ID)
	}
	switch operation.Kind {
	case "object.alloc":
		if operation.Type != TypeObject || len(operation.Operands) != 0 || operation.Effect != EffectAllocate || !slices.Equal(operation.LogicalCapabilityRequirements, []RuntimeCapabilityID{"rt.gc.alloc"}) || operation.PropertySymbolKey != "" {
			return fmt.Errorf("object allocation operation %d is invalid", operation.ID)
		}
	case "object.field.init", "object.field.store":
		if operation.Type != TypeObject || len(operation.Operands) != 2 || operation.Effect != EffectWrite || len(operation.LogicalCapabilityRequirements) != 0 || operation.PropertySymbolKey != property.SymbolKey {
			return fmt.Errorf("object field write operation %d is invalid", operation.ID)
		}
	case "object.alias":
		if operation.Type != TypeObject || len(operation.Operands) != 1 || operation.Effect != EffectPure || len(operation.LogicalCapabilityRequirements) != 0 || operation.PropertySymbolKey != "" {
			return fmt.Errorf("object alias operation %d is invalid", operation.ID)
		}
	case "object.field.load":
		if operation.Type != property.Type || len(operation.Operands) != 1 || operation.Effect != EffectRead || len(operation.LogicalCapabilityRequirements) != 0 || operation.PropertySymbolKey != property.SymbolKey {
			return fmt.Errorf("object field load operation %d is invalid", operation.ID)
		}
	default:
		return fmt.Errorf("operation %d is not a VERT-010 object operation", operation.ID)
	}
	return nil
}

// VerifyVERT010ObjectHIR verifies the complete first object function rather
// than accepting individually well-shaped operations in an unsafe order.
func VerifyVERT010ObjectHIR(module HIRModule) error {
	if module.SchemaVersion != VERT010HIRSchemaVersion {
		return fmt.Errorf("unsupported VERT-010 HIR schema %d", module.SchemaVersion)
	}
	if module.ClassAccess != nil || len(module.ClassAccessProofs) != 0 || module.ClassAccessExecution != nil {
		return fmt.Errorf("VERT-010 HIR must not carry class access proofs")
	}
	if module.PlaceRefs != nil {
		return fmt.Errorf("VERT-010 HIR must not carry PlaceRefs")
	}
	if err := validateHIRProvenance(module.Provenance); err != nil {
		return fmt.Errorf("invalid VERT-010 HIR provenance: %w", err)
	}
	digest, err := LogicalCapabilityRequirementsDigest(module.LogicalCapabilityRequirements)
	if err != nil || module.Provenance.LogicalCapabilityRequirementsDigest != digest {
		return fmt.Errorf("VERT-010 logical capability digest mismatch")
	}
	if err := VerifyVERT010ObjectTypes(module.ObjectTypes); err != nil {
		return err
	}
	if !slices.Equal(module.LogicalCapabilityRequirements, VERT010LogicalCapabilities()) {
		return fmt.Errorf("VERT-010 capability closure mismatch: got %v", module.LogicalCapabilityRequirements)
	}
	if len(module.Functions) != 1 {
		return fmt.Errorf("VERT-010 requires exactly one function")
	}
	function := module.Functions[0]
	if function.ID != 1 || function.Name != "objectAlias" || !function.Exported || function.ReturnType != TypeNumber || len(function.Parameters) != 1 || len(function.Blocks) != 1 || !validOrigin(function.Origin) {
		return fmt.Errorf("VERT-010 function contract is invalid")
	}
	parameter := function.Parameters[0]
	if parameter.Name != "value" || parameter.Value != 1 || parameter.Type != TypeNumber || !validOrigin(parameter.Origin) {
		return fmt.Errorf("VERT-010 parameter contract is invalid")
	}
	block := function.Blocks[0]
	if block.ID != 1 || len(block.Operations) != 8 {
		return fmt.Errorf("VERT-010 requires one canonical eight-operation block")
	}
	for index, operation := range block.Operations {
		if operation.ID != ValueID(index+2) || !validOrigin(operation.Origin) {
			return fmt.Errorf("VERT-010 operation %d is not canonical dense value %d", operation.ID, index+2)
		}
		if operation.PlaceID != 0 || len(operation.Effects) != 0 {
			return fmt.Errorf("VERT-010 operation %d carries VERT-011 metadata", operation.ID)
		}
	}
	typeKey := module.ObjectTypes[0].TypeKey
	propertyKey := module.ObjectTypes[0].Properties[0].SymbolKey
	wantObject := func(operation HIROp, kind string, operands []ValueID, effect Effect, property bool) error {
		if operation.Kind != kind || operation.Type != TypeObject || !slices.Equal(operation.Operands, operands) || operation.Effect != effect || operation.ObjectTypeKey != typeKey {
			return fmt.Errorf("VERT-010 %s operation %d is invalid", kind, operation.ID)
		}
		if property != (operation.PropertySymbolKey == propertyKey) {
			return fmt.Errorf("VERT-010 %s operation %d property binding is invalid", kind, operation.ID)
		}
		return VerifyVERT010ObjectOperationShape(operation, module.ObjectTypes)
	}
	if err := wantObject(block.Operations[0], "object.alloc", nil, EffectAllocate, false); err != nil {
		return err
	}
	if err := wantObject(block.Operations[1], "object.field.init", []ValueID{2, 1}, EffectWrite, true); err != nil {
		return err
	}
	if err := wantObject(block.Operations[2], "object.alias", []ValueID{3}, EffectPure, false); err != nil {
		return err
	}
	loadAlias := block.Operations[3]
	if loadAlias.Kind != "object.field.load" || loadAlias.Type != TypeNumber || !slices.Equal(loadAlias.Operands, []ValueID{4}) || loadAlias.Effect != EffectRead || loadAlias.ObjectTypeKey != typeKey || loadAlias.PropertySymbolKey != propertyKey {
		return fmt.Errorf("VERT-010 alias field load is invalid")
	}
	if err := VerifyVERT010ObjectOperationShape(loadAlias, module.ObjectTypes); err != nil {
		return err
	}
	constant := block.Operations[4]
	bits, err := strconv.ParseUint(constant.NumberBits, 16, 64)
	if err != nil || bits != 0x3ff0000000000000 || constant.Kind != "number.constant" || constant.Type != TypeNumber || len(constant.Operands) != 0 || constant.Effect != EffectPure || constant.ObjectTypeKey != "" || constant.PropertySymbolKey != "" || len(constant.LogicalCapabilityRequirements) != 0 {
		return fmt.Errorf("VERT-010 increment constant is invalid")
	}
	add := block.Operations[5]
	if add.Kind != "binary" || add.Type != TypeNumber || !slices.Equal(add.Operands, []ValueID{5, 6}) || add.Operator != "+" || add.Effect != EffectPure || add.ObjectTypeKey != "" || add.PropertySymbolKey != "" || len(add.LogicalCapabilityRequirements) != 0 {
		return fmt.Errorf("VERT-010 increment operation is invalid")
	}
	if err := wantObject(block.Operations[6], "object.field.store", []ValueID{4, 7}, EffectWrite, true); err != nil {
		return err
	}
	loadOriginal := block.Operations[7]
	if loadOriginal.Kind != "object.field.load" || loadOriginal.Type != TypeNumber || !slices.Equal(loadOriginal.Operands, []ValueID{3}) || loadOriginal.Effect != EffectRead || loadOriginal.ObjectTypeKey != typeKey || loadOriginal.PropertySymbolKey != propertyKey {
		return fmt.Errorf("VERT-010 original field load is invalid")
	}
	if err := VerifyVERT010ObjectOperationShape(loadOriginal, module.ObjectTypes); err != nil {
		return err
	}
	if block.Terminator.Kind != "return" || block.Terminator.Value != 9 || len(block.Terminator.Successors) != 0 || !validOrigin(block.Terminator.Origin) {
		return fmt.Errorf("VERT-010 return must observe the original alias identity")
	}
	return nil
}

// CanonicalVERT010ObjectHIR verifies and hashes a VERT-010 HIR module.
func CanonicalVERT010ObjectHIR(module HIRModule) ([]byte, string, error) {
	module.ContentHash = ""
	if err := VerifyVERT010ObjectHIR(module); err != nil {
		return nil, "", err
	}
	encoded, err := jsonx.Marshal(module)
	if err != nil {
		return nil, "", err
	}
	digest := sha256.Sum256(encoded)
	hash := hex.EncodeToString(digest[:])
	module.ContentHash = hash
	encoded, err = jsonx.Marshal(module)
	if err != nil {
		return nil, "", err
	}
	return encoded, hash, nil
}

// VerifyCanonicalVERT010ObjectHIR verifies structure and the claimed hash.
func VerifyCanonicalVERT010ObjectHIR(module HIRModule) error {
	claimed := module.ContentHash
	_, want, err := CanonicalVERT010ObjectHIR(module)
	if err != nil {
		return err
	}
	if claimed == "" || claimed != want {
		return fmt.Errorf("VERT-010 HIR content hash mismatch: got %q, want %q", claimed, want)
	}
	return nil
}

// DecodeVERT010ObjectHIR strictly decodes the VERT-010 reader major.
func DecodeVERT010ObjectHIR(data []byte) (*HIRModule, error) {
	var module HIRModule
	if err := jsonx.Unmarshal(data, &module, jsonx.RejectUnknownMembers(true)); err != nil {
		return nil, fmt.Errorf("decode VERT-010 HIR: %w", err)
	}
	if err := VerifyCanonicalVERT010ObjectHIR(module); err != nil {
		return nil, err
	}
	return &module, nil
}

func validSHA256Hex(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, digit := range value {
		if !((digit >= '0' && digit <= '9') || (digit >= 'a' && digit <= 'f')) {
			return false
		}
	}
	return true
}
