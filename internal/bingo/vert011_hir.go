package bingo

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"

	jsonx "github.com/microsoft/typescript-go/internal/json"
)

// VERT011HIRSchemaVersion is the independent HIR reader major for property
// places and their explicit optional/logical-assignment control flow.
const VERT011HIRSchemaVersion uint32 = 10

// VerifyVERT011PlaceHIR verifies the closed computed-accessor `??=` shape.
// A later source lowerer may only construct this graph after it has proved the
// same receiver/key/place identities from the frontend snapshot.
func VerifyVERT011PlaceHIR(module HIRModule) error {
	if module.SchemaVersion != VERT011HIRSchemaVersion {
		return fmt.Errorf("unsupported VERT-011 HIR schema %d", module.SchemaVersion)
	}
	if module.ClassAccess != nil || len(module.ClassAccessProofs) != 0 || module.ClassAccessExecution != nil {
		return fmt.Errorf("VERT-011 HIR must not carry class access proofs")
	}
	if err := validateHIRProvenance(module.Provenance); err != nil {
		return fmt.Errorf("invalid VERT-011 HIR provenance: %w", err)
	}
	digest, err := LogicalCapabilityRequirementsDigest(module.LogicalCapabilityRequirements)
	if err != nil || module.Provenance.LogicalCapabilityRequirementsDigest != digest || !slices.Equal(module.LogicalCapabilityRequirements, VERT010LogicalCapabilities()) {
		return fmt.Errorf("VERT-011 logical capability closure is invalid")
	}
	if len(module.ObjectTypes) != 0 || module.PlaceRefs == nil {
		return fmt.Errorf("VERT-011 requires only a canonical PlaceRef contract")
	}
	if err := VerifyCanonicalPlaceRefContract(*module.PlaceRefs); err != nil {
		return fmt.Errorf("VERT-011 PlaceRef contract: %w", err)
	}
	if len(module.PlaceRefs.Places) != 1 {
		return fmt.Errorf("VERT-011 requires exactly one property place")
	}
	place := module.PlaceRefs.Places[0]
	if place.ID != 1 || place.AccessSyntax != PlaceAccessComputed || place.AccessPlan != PlaceAccessAccessor || place.ReadType != TypeNullableNumber || place.WriteType != TypeNumber || place.Mutability != PlaceMutable {
		return fmt.Errorf("VERT-011 property place contract is not the nullable computed accessor subset")
	}
	if len(module.Functions) != 1 {
		return fmt.Errorf("VERT-011 requires one function")
	}
	function := module.Functions[0]
	if function.ID != 1 || function.Name != "propertyNullishAssign" || !function.Exported || function.ReturnType != TypeNumber || len(function.Parameters) != 1 || len(function.Blocks) != 4 || !validOrigin(function.Origin) {
		return fmt.Errorf("VERT-011 function contract is invalid")
	}
	parameter := function.Parameters[0]
	if parameter.Name != "value" || parameter.Value != 1 || parameter.Type != TypeNullableNumber || !validOrigin(parameter.Origin) {
		return fmt.Errorf("VERT-011 value parameter contract is invalid")
	}
	for index, block := range function.Blocks {
		if block.ID != BlockID(index+1) || !validOrigin(block.Terminator.Origin) {
			return fmt.Errorf("VERT-011 block %d is invalid", index+1)
		}
	}
	blocks := function.Blocks
	if !slices.Equal(module.PlaceRefs.EvaluationOrder, []ValueID{3, 4}) || place.Receiver != 3 || place.Key != 4 {
		return fmt.Errorf("VERT-011 receiver/key evaluation binding is invalid")
	}
	if err := verifyVERT011Operations(blocks[0].Operations, []vert011Operation{
		{id: 2, kind: "object.alloc", typ: TypeObject, effect: EffectAllocate, effects: []Effect{EffectAllocate}, objectTypeKey: place.ObjectTypeKey, requirements: []RuntimeCapabilityID{"rt.gc.alloc"}},
		{id: 3, kind: "object.field.init", typ: TypeObject, operands: []ValueID{2, 1}, effect: EffectWrite, effects: []Effect{EffectWrite}, objectTypeKey: place.ObjectTypeKey, propertySymbolKey: place.BackingPropertySymbolKey},
		{id: 4, kind: "evaluate.key", typ: TypeString, effect: EffectPure, effects: []Effect{EffectPure}},
		{id: 5, kind: "place.make", typ: TypeVoid, operands: []ValueID{3, 4}, place: 1, effect: EffectPure, effects: []Effect{EffectPure}},
		{id: 6, kind: "place.load", typ: TypeNullableNumber, place: 1, effect: EffectCall, effects: place.LoadEffects},
		{id: 7, kind: "is_nullish", typ: TypeBoolean, operands: []ValueID{6}, effect: EffectPure, effects: []Effect{EffectPure}},
	}); err != nil {
		return fmt.Errorf("VERT-011 entry: %w", err)
	}
	if blocks[0].Terminator.Kind != "condbranch" || blocks[0].Terminator.Value != 7 || !slices.Equal(blocks[0].Terminator.Successors, []BlockID{2, 3}) {
		return fmt.Errorf("VERT-011 nullish branch is invalid")
	}
	if err := verifyVERT011Operations(blocks[1].Operations, []vert011Operation{
		{id: 8, kind: "number.constant", typ: TypeNumber, effect: EffectPure, effects: []Effect{EffectPure}, numberBits: "3ff0000000000000"},
		{id: 9, kind: "place.store", typ: TypeNumber, operands: []ValueID{8}, place: 1, effect: EffectCall, effects: place.StoreEffects},
	}); err != nil {
		return fmt.Errorf("VERT-011 assigning edge: %w", err)
	}
	if blocks[1].Terminator.Kind != "branch" || blocks[1].Terminator.Value != 0 || !slices.Equal(blocks[1].Terminator.Successors, []BlockID{4}) {
		return fmt.Errorf("VERT-011 assigning edge terminator is invalid")
	}
	if err := verifyVERT011Operations(blocks[2].Operations, []vert011Operation{{id: 10, kind: "unwrap_nullable", typ: TypeNumber, operands: []ValueID{6}, effect: EffectPure, effects: []Effect{EffectPure}}}); err != nil {
		return fmt.Errorf("VERT-011 non-assigning edge: %w", err)
	}
	if blocks[2].Terminator.Kind != "branch" || blocks[2].Terminator.Value != 0 || !slices.Equal(blocks[2].Terminator.Successors, []BlockID{4}) {
		return fmt.Errorf("VERT-011 non-assigning edge terminator is invalid")
	}
	if err := verifyVERT011Operations(blocks[3].Operations, []vert011Operation{{id: 11, kind: "phi", typ: TypeNumber, operands: []ValueID{9, 10}, effect: EffectPure, effects: []Effect{EffectPure}, incoming: []BlockID{2, 3}}}); err != nil {
		return fmt.Errorf("VERT-011 merge: %w", err)
	}
	if blocks[3].Terminator.Kind != "return" || blocks[3].Terminator.Value != 11 || len(blocks[3].Terminator.Successors) != 0 {
		return fmt.Errorf("VERT-011 return is invalid")
	}
	return nil
}

type vert011Operation struct {
	id                ValueID
	kind              string
	typ               TypeKind
	operands          []ValueID
	place             PlaceID
	effect            Effect
	effects           []Effect
	incoming          []BlockID
	numberBits        string
	objectTypeKey     string
	propertySymbolKey string
	requirements      []RuntimeCapabilityID
}

func verifyVERT011Operations(operations []HIROp, want []vert011Operation) error {
	if len(operations) != len(want) {
		return fmt.Errorf("operation count %d, want %d", len(operations), len(want))
	}
	for index, expected := range want {
		operation := operations[index]
		if operation.ID != expected.id || operation.Kind != expected.kind || operation.Type != expected.typ || !slices.Equal(operation.Operands, expected.operands) || operation.PlaceID != expected.place || operation.Effect != expected.effect || !slices.Equal(operation.Effects, expected.effects) || !slices.Equal(operation.IncomingBlocks, expected.incoming) || operation.Operator != "" || operation.NumberBits != expected.numberBits || operation.UTF16CodeUnits != "" || operation.Callee != 0 || operation.ObjectTypeKey != expected.objectTypeKey || operation.PropertySymbolKey != expected.propertySymbolKey || !slices.Equal(operation.LogicalCapabilityRequirements, expected.requirements) || !validOrigin(operation.Origin) {
			return fmt.Errorf("operation %d is invalid", expected.id)
		}
	}
	return nil
}

// CanonicalVERT011PlaceHIR verifies and serializes a VERT-011 HIR module.
func CanonicalVERT011PlaceHIR(module HIRModule) ([]byte, string, error) {
	module.ContentHash = ""
	if err := VerifyVERT011PlaceHIR(module); err != nil {
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

// VerifyCanonicalVERT011PlaceHIR verifies VERT-011 structure and content hash.
func VerifyCanonicalVERT011PlaceHIR(module HIRModule) error {
	claimed := module.ContentHash
	_, want, err := CanonicalVERT011PlaceHIR(module)
	if err != nil {
		return err
	}
	if claimed == "" || claimed != want {
		return fmt.Errorf("VERT-011 HIR content hash mismatch: got %q, want %q", claimed, want)
	}
	return nil
}

// DecodeVERT011PlaceHIR strictly decodes the VERT-011 reader major.
func DecodeVERT011PlaceHIR(data []byte) (*HIRModule, error) {
	var module HIRModule
	if err := jsonx.Unmarshal(data, &module, jsonx.RejectUnknownMembers(true)); err != nil {
		return nil, fmt.Errorf("decode VERT-011 HIR: %w", err)
	}
	if err := VerifyCanonicalVERT011PlaceHIR(module); err != nil {
		return nil, err
	}
	return &module, nil
}
