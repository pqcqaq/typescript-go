// Package bingo contains the target-independent primitive IR contracts.
// Frontend facts are consumed as values; this package never imports the live
// TypeScript checker or AST.
package bingo

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"slices"
)

const (
	// HIRSchemaVersion is the serialized typed-HIR contract.
	HIRSchemaVersion uint32 = 8
	// HIRFrontendSnapshotSchemaVersion is the only frontend snapshot schema
	// accepted by HIR v8. Supporting another major requires an explicit reader.
	HIRFrontendSnapshotSchemaVersion uint32 = 2
	// MIRSchemaVersion is the serialized target-aware MIR contract.
	MIRSchemaVersion uint32 = 3
)

// ValueID, BlockID and FunctionID are dense IDs local to one artifact.
type ValueID uint32
type BlockID uint32
type FunctionID uint32

// RuntimeCapabilityID names a target-independent logical runtime requirement.
// Availability and concrete ABI binding are resolved only after HIR.
type RuntimeCapabilityID string

const logicalCapabilityRequirementsSchema = "bingo-logical-capabilities-v1"

// TypeKind is intentionally small for the first vertical slice.
type TypeKind string

const (
	TypeNumber  TypeKind = "number"
	TypeBoolean TypeKind = "boolean"
	TypeString  TypeKind = "string"
	// TypeNullableNumber is the canonical three-state static union used by the
	// Phase 2B nullish slice. The target ABI preserves null and undefined tags.
	TypeNullableNumber TypeKind = "number|null|undefined"
	TypeVoid           TypeKind = "void"
)

// CompilerBuildIdentity identifies the exact fork checkout used for lowering
// while retaining the upstream commit from which that fork was derived.
type CompilerBuildIdentity struct {
	UpstreamCommit string `json:"upstreamCommit"`
	ForkCommit     string `json:"forkCommit"`
	LoweringSchema string `json:"loweringSchema"`
	LoweringHash   string `json:"loweringHash"`
}

// HIRProvenance binds a typed-HIR artifact to the exact frontend snapshot,
// compiler build, and logical capability contract that produced it.
type HIRProvenance struct {
	FrontendSnapshotSchemaVersion       uint32                `json:"frontendSnapshotSchemaVersion"`
	FrontendSnapshotHash                string                `json:"frontendSnapshotHash"`
	SourceContentHash                   string                `json:"sourceContentHash"`
	CompilerBuildIdentity               CompilerBuildIdentity `json:"compilerBuildIdentity"`
	StandardLibraryHash                 string                `json:"standardLibraryHash"`
	KindManifestHash                    string                `json:"kindManifestHash"`
	LogicalCapabilityRequirementsDigest string                `json:"logicalCapabilityRequirementsDigest"`
}

// Effect records the observable behavior of an operation.
type Effect string

const (
	EffectPure  Effect = "pure"
	EffectRead  Effect = "read"
	EffectWrite Effect = "write"
	EffectCall  Effect = "call"
)

// Origin identifies the source span that produced an IR value or operation.
type Origin struct {
	File  string `json:"file"`
	Start int    `json:"start"`
	End   int    `json:"end"`
}

// HIRModule is the canonical typed-HIR artifact.
type HIRModule struct {
	SchemaVersion                 uint32                `json:"schemaVersion"`
	Provenance                    HIRProvenance         `json:"provenance"`
	LogicalCapabilityRequirements []RuntimeCapabilityID `json:"logicalCapabilityRequirements"`
	Functions                     []HIRFunction         `json:"functions"`
	ContentHash                   string                `json:"contentHash"`
}

type HIRFunction struct {
	ID         FunctionID     `json:"id"`
	Name       string         `json:"name"`
	Exported   bool           `json:"exported,omitempty"`
	Parameters []HIRParameter `json:"parameters"`
	Blocks     []HIRBlock     `json:"blocks"`
	ReturnType TypeKind       `json:"returnType"`
	Origin     Origin         `json:"origin"`
}

type HIRParameter struct {
	Name   string   `json:"name"`
	Value  ValueID  `json:"value"`
	Type   TypeKind `json:"type"`
	Origin Origin   `json:"origin"`
}

type HIRBlock struct {
	ID         BlockID       `json:"id"`
	Operations []HIROp       `json:"operations"`
	Terminator HIRTerminator `json:"terminator"`
}

type HIROp struct {
	ID                            ValueID               `json:"id"`
	Kind                          string                `json:"kind"`
	Type                          TypeKind              `json:"type"`
	Operands                      []ValueID             `json:"operands,omitempty"`
	IncomingBlocks                []BlockID             `json:"incomingBlocks,omitempty"`
	Operator                      string                `json:"operator,omitempty"`
	NumberBits                    string                `json:"numberBits,omitempty"`
	UTF16CodeUnits                string                `json:"utf16CodeUnits,omitempty"`
	Callee                        FunctionID            `json:"callee,omitempty"`
	Effect                        Effect                `json:"effect"`
	LogicalCapabilityRequirements []RuntimeCapabilityID `json:"logicalCapabilityRequirements"`
	Origin                        Origin                `json:"origin"`
}

type HIRTerminator struct {
	Kind       string    `json:"kind"`
	Value      ValueID   `json:"value,omitempty"`
	Successors []BlockID `json:"successors,omitempty"`
	Origin     Origin    `json:"origin"`
}

// MIRModule is the post-CFG/SSA primitive artifact.
type MIRModule struct {
	SchemaVersion uint32        `json:"schemaVersion"`
	Functions     []MIRFunction `json:"functions"`
	ContentHash   string        `json:"contentHash"`
}

type MIRFunction struct {
	ID         FunctionID     `json:"id"`
	Name       string         `json:"name"`
	Exported   bool           `json:"exported,omitempty"`
	Parameters []MIRParameter `json:"parameters"`
	Blocks     []MIRBlock     `json:"blocks"`
	ReturnType TypeKind       `json:"returnType"`
	Origin     Origin         `json:"origin"`
}

type MIRParameter struct {
	Name   string   `json:"name"`
	Value  ValueID  `json:"value"`
	Type   TypeKind `json:"type"`
	Origin Origin   `json:"origin"`
}

type MIRBlock struct {
	ID           BlockID          `json:"id"`
	Instructions []MIRInstruction `json:"instructions"`
	Terminator   MIRTerminator    `json:"terminator"`
}

type MIRInstruction struct {
	ID       ValueID    `json:"id"`
	Kind     string     `json:"kind"`
	Type     TypeKind   `json:"type"`
	Operands []ValueID  `json:"operands,omitempty"`
	Operator string     `json:"operator,omitempty"`
	Callee   FunctionID `json:"callee,omitempty"`
	Effect   Effect     `json:"effect"`
	Origin   Origin     `json:"origin"`
}

type MIRTerminator struct {
	Kind       string    `json:"kind"`
	Value      ValueID   `json:"value,omitempty"`
	Successors []BlockID `json:"successors,omitempty"`
	Origin     Origin    `json:"origin"`
}

// VerifyHIR validates IDs, CFG reachability/dominance, operation signatures,
// type agreement, provenance, and terminator structure. It is intentionally
// strict for malformed input because no backend may repair an invalid HIR.
func VerifyHIR(module HIRModule) error {
	if module.SchemaVersion != HIRSchemaVersion {
		return fmt.Errorf("unsupported HIR schema %d", module.SchemaVersion)
	}
	if err := validateHIRProvenance(module.Provenance); err != nil {
		return fmt.Errorf("invalid HIR provenance: %w", err)
	}
	requirementsDigest, err := LogicalCapabilityRequirementsDigest(module.LogicalCapabilityRequirements)
	if err != nil {
		return fmt.Errorf("invalid HIR logical capability requirements: %w", err)
	}
	if module.Provenance.LogicalCapabilityRequirementsDigest != requirementsDigest {
		return fmt.Errorf(
			"HIR logical capability requirements digest mismatch: got %q, want %q",
			module.Provenance.LogicalCapabilityRequirementsDigest,
			requirementsDigest,
		)
	}
	if len(module.Functions) != 1 {
		return fmt.Errorf("first-slice HIR requires exactly one function, got %d", len(module.Functions))
	}
	if len(module.LogicalCapabilityRequirements) != 0 {
		return fmt.Errorf("first-slice HIR does not bind runtime capabilities")
	}
	functions := make(map[FunctionID]struct{}, len(module.Functions))
	for functionIndex, function := range module.Functions {
		if function.ID == 0 || function.Name == "" || len(function.Blocks) == 0 {
			return fmt.Errorf("function %d is incomplete", function.ID)
		}
		if function.ID != FunctionID(functionIndex+1) {
			return fmt.Errorf("function ID %d is not canonical dense ID %d", function.ID, functionIndex+1)
		}
		if _, duplicate := functions[function.ID]; duplicate {
			return fmt.Errorf("duplicate function %d", function.ID)
		}
		functions[function.ID] = struct{}{}
		if err := verifyHIRFunction(function); err != nil {
			return fmt.Errorf("function %s: %w", function.Name, err)
		}
	}
	actualRequirements := make([]RuntimeCapabilityID, 0)
	seenRequirements := make(map[RuntimeCapabilityID]struct{})
	for _, function := range module.Functions {
		for _, block := range function.Blocks {
			for _, operation := range block.Operations {
				for _, requirement := range operation.LogicalCapabilityRequirements {
					if _, seen := seenRequirements[requirement]; seen {
						continue
					}
					seenRequirements[requirement] = struct{}{}
					actualRequirements = append(actualRequirements, requirement)
				}
			}
		}
	}
	slices.Sort(actualRequirements)
	if !slices.Equal(module.LogicalCapabilityRequirements, actualRequirements) {
		return fmt.Errorf(
			"HIR logical capability requirements %v do not match operation requirements %v",
			module.LogicalCapabilityRequirements,
			actualRequirements,
		)
	}
	return nil
}

type valueDefinition struct {
	typ      TypeKind
	block    int
	position int
}

func verifyHIRFunction(function HIRFunction) error {
	if !validType(function.ReturnType) || !validOrigin(function.Origin) {
		return fmt.Errorf("function has invalid return type or origin")
	}
	if function.ReturnType != TypeNumber {
		return fmt.Errorf("first-slice function return type is %q, want %q", function.ReturnType, TypeNumber)
	}
	if len(function.Parameters) != 2 {
		return fmt.Errorf("first-slice function requires two parameters, got %d", len(function.Parameters))
	}
	if len(function.Blocks) != 1 {
		return fmt.Errorf("first-slice function requires one block, got %d", len(function.Blocks))
	}
	blocks := make(map[BlockID]int, len(function.Blocks))
	values := make(map[ValueID]valueDefinition)
	for parameterIndex, parameter := range function.Parameters {
		if parameter.Value == 0 || parameter.Name == "" || !validType(parameter.Type) || !validOrigin(parameter.Origin) {
			return fmt.Errorf("invalid parameter %q", parameter.Name)
		}
		if parameter.Type != TypeNumber {
			return fmt.Errorf("first-slice parameter %q has type %q, want %q", parameter.Name, parameter.Type, TypeNumber)
		}
		if parameter.Value != ValueID(parameterIndex+1) {
			return fmt.Errorf("parameter value ID %d is not canonical dense ID %d", parameter.Value, parameterIndex+1)
		}
		if _, duplicate := values[parameter.Value]; duplicate {
			return fmt.Errorf("duplicate value %d", parameter.Value)
		}
		values[parameter.Value] = valueDefinition{typ: parameter.Type, block: -1, position: -1}
	}
	for blockIndex, block := range function.Blocks {
		if block.ID == 0 {
			return fmt.Errorf("block %d has zero ID", blockIndex)
		}
		if _, duplicate := blocks[block.ID]; duplicate {
			return fmt.Errorf("duplicate block %d", block.ID)
		}
		if block.ID != BlockID(blockIndex+1) {
			return fmt.Errorf("block ID %d is not canonical dense ID %d", block.ID, blockIndex+1)
		}
		if len(block.Operations) != 1 {
			return fmt.Errorf("first-slice block requires one operation, got %d", len(block.Operations))
		}
		blocks[block.ID] = blockIndex
		for operationIndex, op := range block.Operations {
			if err := validateHIROperationShape(op); err != nil {
				return fmt.Errorf("block %d operation %d: %w", block.ID, operationIndex, err)
			}
			if _, duplicate := values[op.ID]; duplicate {
				return fmt.Errorf("duplicate value %d", op.ID)
			}
			expectedValueID := ValueID(len(function.Parameters) + operationIndex + 1)
			if op.ID != expectedValueID {
				return fmt.Errorf("operation value ID %d is not canonical dense ID %d", op.ID, expectedValueID)
			}
			values[op.ID] = valueDefinition{typ: op.Type, block: blockIndex, position: operationIndex}
		}
		if err := validateHIRTerminatorShape(block.Terminator); err != nil {
			return fmt.Errorf("block %d: %w", block.ID, err)
		}
	}

	predicates := make([][]int, len(function.Blocks))
	for blockIndex, block := range function.Blocks {
		for _, successor := range block.Terminator.Successors {
			successorIndex, ok := blocks[successor]
			if !ok {
				return fmt.Errorf("block %d targets missing block %d", block.ID, successor)
			}
			predicates[successorIndex] = append(predicates[successorIndex], blockIndex)
		}
	}
	reachable := make([]bool, len(function.Blocks))
	var visit func(int)
	visit = func(blockIndex int) {
		if reachable[blockIndex] {
			return
		}
		reachable[blockIndex] = true
		for _, successor := range function.Blocks[blockIndex].Terminator.Successors {
			visit(blocks[successor])
		}
	}
	visit(0)
	for blockIndex, isReachable := range reachable {
		if !isReachable {
			return fmt.Errorf("block %d is unreachable", function.Blocks[blockIndex].ID)
		}
	}
	dominators := computeDominators(len(function.Blocks), predicates)
	for blockIndex, block := range function.Blocks {
		for operationIndex, op := range block.Operations {
			for _, operand := range op.Operands {
				if err := validateValueUse(operand, blockIndex, operationIndex, values, dominators); err != nil {
					return fmt.Errorf("operation %d: %w", op.ID, err)
				}
			}
			if op.Kind == "binary" {
				left, right := values[op.Operands[0]], values[op.Operands[1]]
				if left.typ != right.typ || left.typ != op.Type {
					return fmt.Errorf("binary operation %d has operand types %q/%q, result %q", op.ID, left.typ, right.typ, op.Type)
				}
			}
		}
		terminator := block.Terminator
		if terminator.Kind == "return" {
			if function.ReturnType == TypeVoid && terminator.Value != 0 {
				return fmt.Errorf("void return has value")
			}
			if function.ReturnType != TypeVoid && terminator.Value == 0 {
				return fmt.Errorf("non-void return has no value")
			}
		}
		if terminator.Kind == "return" && terminator.Value != 0 {
			if err := validateValueUse(terminator.Value, blockIndex, len(block.Operations), values, dominators); err != nil {
				return fmt.Errorf("return terminator: %w", err)
			}
			valueType := values[terminator.Value].typ
			if valueType != function.ReturnType {
				return fmt.Errorf("return value %d has type %q, want %q", terminator.Value, valueType, function.ReturnType)
			}
		} else if terminator.Kind == "condbranch" || terminator.Kind == "switch" {
			if err := validateValueUse(terminator.Value, blockIndex, len(block.Operations), values, dominators); err != nil {
				return fmt.Errorf("%s terminator: %w", terminator.Kind, err)
			}
			if terminator.Kind == "condbranch" && values[terminator.Value].typ != TypeBoolean {
				return fmt.Errorf("conditional branch value %d has type %q, want %q", terminator.Value, values[terminator.Value].typ, TypeBoolean)
			}
		}
	}
	return nil
}

func validateHIROperationShape(op HIROp) error {
	if op.ID == 0 || op.Kind == "" || !validType(op.Type) || !validEffect(op.Effect) || !validOrigin(op.Origin) {
		return fmt.Errorf("invalid operation %d", op.ID)
	}
	if err := validateLogicalCapabilityRequirements(op.LogicalCapabilityRequirements); err != nil {
		return fmt.Errorf("operation %d has invalid logical capability requirements: %w", op.ID, err)
	}
	switch op.Kind {
	case "binary":
		if len(op.Operands) != 2 || op.Operator == "" || op.NumberBits != "" {
			return fmt.Errorf("binary operation %d has invalid arity/operator", op.ID)
		}
		if op.Effect != EffectPure || op.Operator != "+" {
			return fmt.Errorf("binary operation %d has invalid effect/operator", op.ID)
		}
		if len(op.LogicalCapabilityRequirements) != 0 {
			return fmt.Errorf("binary operation %d cannot require runtime capabilities", op.ID)
		}
	default:
		return fmt.Errorf("operation kind %q is outside first-slice HIR", op.Kind)
	}
	return nil
}

func validateHIRTerminatorShape(terminator HIRTerminator) error {
	if terminator.Kind == "" || !validOrigin(terminator.Origin) {
		return fmt.Errorf("invalid terminator")
	}
	switch terminator.Kind {
	case "return":
		if len(terminator.Successors) != 0 {
			return fmt.Errorf("return terminator has successors")
		}
	default:
		return fmt.Errorf("terminator kind %q is outside first-slice HIR", terminator.Kind)
	}
	return nil
}

func validateValueUse(value ValueID, blockIndex, position int, values map[ValueID]valueDefinition, dominators []map[int]struct{}) error {
	if value == 0 {
		return fmt.Errorf("value is zero")
	}
	definition, ok := values[value]
	if !ok {
		return fmt.Errorf("uses undefined value %d", value)
	}
	if definition.block == -1 {
		return nil
	}
	if definition.block == blockIndex && definition.position >= position {
		return fmt.Errorf("value %d is not defined before use", value)
	}
	if definition.block != blockIndex {
		if _, dominates := dominators[blockIndex][definition.block]; !dominates {
			return fmt.Errorf("value %d is not dominated by its definition", value)
		}
	}
	return nil
}

func computeDominators(blockCount int, predecessors [][]int) []map[int]struct{} {
	all := make(map[int]struct{}, blockCount)
	for index := 0; index < blockCount; index++ {
		all[index] = struct{}{}
	}
	dominators := make([]map[int]struct{}, blockCount)
	for index := range dominators {
		if index == 0 {
			dominators[index] = map[int]struct{}{0: {}}
		} else {
			dominators[index] = mapsClone(all)
		}
	}
	changed := true
	for changed {
		changed = false
		for index := 1; index < blockCount; index++ {
			if len(predecessors[index]) == 0 {
				continue
			}
			intersection := mapsClone(dominators[predecessors[index][0]])
			for _, predecessor := range predecessors[index][1:] {
				for candidate := range intersection {
					if _, ok := dominators[predecessor][candidate]; !ok {
						delete(intersection, candidate)
					}
				}
			}
			intersection[index] = struct{}{}
			if !mapsEqual(dominators[index], intersection) {
				dominators[index] = intersection
				changed = true
			}
		}
	}
	return dominators
}

func mapsClone(input map[int]struct{}) map[int]struct{} {
	result := make(map[int]struct{}, len(input))
	for key := range input {
		result[key] = struct{}{}
	}
	return result
}

func mapsEqual(left, right map[int]struct{}) bool {
	if len(left) != len(right) {
		return false
	}
	for key := range left {
		if _, ok := right[key]; !ok {
			return false
		}
	}
	return true
}

func validOrigin(origin Origin) bool {
	return origin.File != "" && origin.Start >= 0 && origin.End >= origin.Start
}

func validateHIRProvenance(provenance HIRProvenance) error {
	if provenance.FrontendSnapshotSchemaVersion != HIRFrontendSnapshotSchemaVersion {
		return fmt.Errorf(
			"unsupported frontend snapshot schema %d, want %d",
			provenance.FrontendSnapshotSchemaVersion,
			HIRFrontendSnapshotSchemaVersion,
		)
	}
	digests := []struct {
		name  string
		value string
	}{
		{name: "frontend snapshot", value: provenance.FrontendSnapshotHash},
		{name: "source content", value: provenance.SourceContentHash},
		{name: "standard library", value: provenance.StandardLibraryHash},
		{name: "kind manifest", value: provenance.KindManifestHash},
		{name: "logical capability requirements", value: provenance.LogicalCapabilityRequirementsDigest},
	}
	for _, digest := range digests {
		if len(digest.value) != sha256.Size*2 || !isLowerHex(digest.value) {
			return fmt.Errorf("%s hash %q is not a lowercase SHA-256 digest", digest.name, digest.value)
		}
	}
	if err := ValidateCompilerBuildIdentity(provenance.CompilerBuildIdentity); err != nil {
		return fmt.Errorf("invalid compiler build identity: %w", err)
	}
	return nil
}

// ValidateCompilerBuildIdentity rejects incomplete or non-canonical compiler
// provenance before it can enter an HIR artifact or cache key.
func ValidateCompilerBuildIdentity(identity CompilerBuildIdentity) error {
	commits := []struct {
		name  string
		value string
	}{
		{name: "upstream", value: identity.UpstreamCommit},
		{name: "fork", value: identity.ForkCommit},
	}
	for _, commit := range commits {
		if len(commit.value) != 40 || !isLowerHex(commit.value) {
			return fmt.Errorf("%s commit %q is not a lowercase commit", commit.name, commit.value)
		}
	}
	if !validLoweringSchema(identity.LoweringSchema) {
		return fmt.Errorf("lowering schema %q is not canonical", identity.LoweringSchema)
	}
	if len(identity.LoweringHash) != sha256.Size*2 || !isLowerHex(identity.LoweringHash) {
		return fmt.Errorf("lowering hash %q is not a lowercase digest", identity.LoweringHash)
	}
	return nil
}

// LogicalCapabilityRequirementsDigest returns the domain-separated digest of
// an explicit, sorted, unique logical requirement list.
func LogicalCapabilityRequirementsDigest(requirements []RuntimeCapabilityID) (string, error) {
	if err := validateLogicalCapabilityRequirements(requirements); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(struct {
		Schema       string                `json:"schema"`
		Requirements []RuntimeCapabilityID `json:"requirements"`
	}{Schema: logicalCapabilityRequirementsSchema, Requirements: requirements})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func validateLogicalCapabilityRequirements(requirements []RuntimeCapabilityID) error {
	if requirements == nil {
		return fmt.Errorf("requirements are missing")
	}
	for index, requirement := range requirements {
		value := string(requirement)
		if !validRuntimeCapabilityID(value) {
			return fmt.Errorf("requirement %d %q is not canonical", index, value)
		}
		if index > 0 && string(requirements[index-1]) >= value {
			return fmt.Errorf("requirements are not sorted and unique at %q", value)
		}
	}
	return nil
}

func validRuntimeCapabilityID(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for index, char := range value {
		if char >= 'a' && char <= 'z' || char >= '0' && char <= '9' {
			continue
		}
		if index > 0 && (char == '.' || char == ':' || char == '/' || char == '-' || char == '_') {
			continue
		}
		return false
	}
	return true
}

func validLoweringSchema(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for index, char := range value {
		if char >= 'a' && char <= 'z' || char >= '0' && char <= '9' {
			continue
		}
		if index > 0 && (char == '.' || char == '-' || char == '_') {
			continue
		}
		return false
	}
	return true
}

func isLowerHex(value string) bool {
	for _, char := range value {
		if !(char >= '0' && char <= '9') && !(char >= 'a' && char <= 'f') {
			return false
		}
	}
	return true
}

func validCanonicalNumberBits(value string) bool {
	if len(value) != 16 || !isLowerHex(value) {
		return false
	}
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != 8 {
		return false
	}
	bits := binary.BigEndian.Uint64(decoded)
	return !math.IsNaN(math.Float64frombits(bits)) || NumberABIBits(bits) == CanonicalNumberNaNBits
}

// VerifyMIR applies the same structural checks to the target-aware form.
func VerifyMIR(module MIRModule) error {
	if module.SchemaVersion != MIRSchemaVersion {
		return fmt.Errorf("unsupported MIR schema %d", module.SchemaVersion)
	}
	for _, function := range module.Functions {
		if function.ID == 0 || function.Name == "" || len(function.Blocks) == 0 {
			return fmt.Errorf("function %d is incomplete", function.ID)
		}
		if err := verifyMIRFunction(function); err != nil {
			return fmt.Errorf("function %s: %w", function.Name, err)
		}
	}
	return nil
}

func verifyMIRFunction(function MIRFunction) error {
	// MIR remains on its pre-release v1 schema while HIR v3 deliberately
	// accepts only the number-add first slice. Keep the v1 structural verifier
	// independent so tightening HIR cannot silently change an unchanged MIR
	// schema's accepted CFG, primitive-type, or instruction surface.
	if !validMIRV1Type(function.ReturnType) || !validOrigin(function.Origin) {
		return fmt.Errorf("function has invalid return type or origin")
	}
	blocks := make(map[BlockID]int, len(function.Blocks))
	values := make(map[ValueID]valueDefinition)
	for _, parameter := range function.Parameters {
		if parameter.Value == 0 || parameter.Name == "" || !validMIRV1Type(parameter.Type) || !validOrigin(parameter.Origin) {
			return fmt.Errorf("invalid parameter %q", parameter.Name)
		}
		if _, duplicate := values[parameter.Value]; duplicate {
			return fmt.Errorf("duplicate value %d", parameter.Value)
		}
		values[parameter.Value] = valueDefinition{typ: parameter.Type, block: -1, position: -1}
	}
	for blockIndex, block := range function.Blocks {
		if block.ID == 0 {
			return fmt.Errorf("block %d has zero ID", blockIndex)
		}
		if _, duplicate := blocks[block.ID]; duplicate {
			return fmt.Errorf("duplicate block %d", block.ID)
		}
		blocks[block.ID] = blockIndex
		for instructionIndex, instruction := range block.Instructions {
			if err := validateMIRV1InstructionShape(instruction); err != nil {
				return fmt.Errorf("block %d instruction %d: %w", block.ID, instructionIndex, err)
			}
			if _, duplicate := values[instruction.ID]; duplicate {
				return fmt.Errorf("duplicate value %d", instruction.ID)
			}
			values[instruction.ID] = valueDefinition{typ: instruction.Type, block: blockIndex, position: instructionIndex}
		}
		if err := validateMIRV1TerminatorShape(block.Terminator); err != nil {
			return fmt.Errorf("block %d: %w", block.ID, err)
		}
	}

	predecessors := make([][]int, len(function.Blocks))
	for blockIndex, block := range function.Blocks {
		for _, successor := range block.Terminator.Successors {
			successorIndex, ok := blocks[successor]
			if !ok {
				return fmt.Errorf("block %d targets missing block %d", block.ID, successor)
			}
			predecessors[successorIndex] = append(predecessors[successorIndex], blockIndex)
		}
	}
	reachable := make([]bool, len(function.Blocks))
	var visit func(int)
	visit = func(blockIndex int) {
		if reachable[blockIndex] {
			return
		}
		reachable[blockIndex] = true
		for _, successor := range function.Blocks[blockIndex].Terminator.Successors {
			visit(blocks[successor])
		}
	}
	visit(0)
	for blockIndex, isReachable := range reachable {
		if !isReachable {
			return fmt.Errorf("block %d is unreachable", function.Blocks[blockIndex].ID)
		}
	}
	dominators := computeDominators(len(function.Blocks), predecessors)
	for blockIndex, block := range function.Blocks {
		for instructionIndex, instruction := range block.Instructions {
			for _, operand := range instruction.Operands {
				if err := validateValueUse(operand, blockIndex, instructionIndex, values, dominators); err != nil {
					return fmt.Errorf("instruction %d: %w", instruction.ID, err)
				}
			}
			if instruction.Kind == "binary" {
				left, right := values[instruction.Operands[0]], values[instruction.Operands[1]]
				if left.typ != right.typ || left.typ != instruction.Type {
					return fmt.Errorf(
						"binary instruction %d has operand types %q/%q, result %q",
						instruction.ID,
						left.typ,
						right.typ,
						instruction.Type,
					)
				}
			}
		}
		terminator := block.Terminator
		if terminator.Kind == "return" {
			if function.ReturnType == TypeVoid && terminator.Value != 0 {
				return fmt.Errorf("void return has value")
			}
			if function.ReturnType != TypeVoid && terminator.Value == 0 {
				return fmt.Errorf("non-void return has no value")
			}
		}
		if terminator.Kind == "return" && terminator.Value != 0 {
			if err := validateValueUse(terminator.Value, blockIndex, len(block.Instructions), values, dominators); err != nil {
				return fmt.Errorf("return terminator: %w", err)
			}
			valueType := values[terminator.Value].typ
			if valueType != function.ReturnType {
				return fmt.Errorf("return value %d has type %q, want %q", terminator.Value, valueType, function.ReturnType)
			}
		} else if terminator.Kind == "condbranch" || terminator.Kind == "switch" {
			if err := validateValueUse(terminator.Value, blockIndex, len(block.Instructions), values, dominators); err != nil {
				return fmt.Errorf("%s terminator: %w", terminator.Kind, err)
			}
			if terminator.Kind == "condbranch" && values[terminator.Value].typ != TypeBoolean {
				return fmt.Errorf(
					"conditional branch value %d has type %q, want %q",
					terminator.Value,
					values[terminator.Value].typ,
					TypeBoolean,
				)
			}
		}
	}
	return nil
}

func validMIRV1Type(value TypeKind) bool {
	switch value {
	case TypeNumber, TypeBoolean, TypeString, TypeVoid:
		return true
	default:
		return false
	}
}

func validateMIRV1InstructionShape(instruction MIRInstruction) error {
	if instruction.ID == 0 || instruction.Kind == "" || !validMIRV1Type(instruction.Type) || !validEffect(instruction.Effect) || !validOrigin(instruction.Origin) {
		return fmt.Errorf("invalid instruction %d", instruction.ID)
	}
	switch instruction.Kind {
	case "binary":
		if len(instruction.Operands) != 2 || instruction.Operator == "" {
			return fmt.Errorf("binary instruction %d has invalid arity/operator", instruction.ID)
		}
		if instruction.Effect != EffectPure || !slices.Contains([]string{"+", "-", "*", "/"}, instruction.Operator) {
			return fmt.Errorf("binary instruction %d has invalid effect/operator", instruction.ID)
		}
	case "literal", "phi":
		if instruction.Kind == "literal" && len(instruction.Operands) != 0 || instruction.Kind == "phi" && len(instruction.Operands) == 0 {
			return fmt.Errorf("%s instruction %d has invalid arity", instruction.Kind, instruction.ID)
		}
		if instruction.Effect != EffectPure {
			return fmt.Errorf("%s instruction %d must be pure", instruction.Kind, instruction.ID)
		}
	case "call":
		if instruction.Effect != EffectCall {
			return fmt.Errorf("call instruction %d must have call effect", instruction.ID)
		}
	case "load":
		if instruction.Effect != EffectRead {
			return fmt.Errorf("load instruction %d must have read effect", instruction.ID)
		}
	case "store":
		if instruction.Effect != EffectWrite {
			return fmt.Errorf("store instruction %d must have write effect", instruction.ID)
		}
	default:
		return fmt.Errorf("unknown instruction kind %q", instruction.Kind)
	}
	return nil
}

func validateMIRV1TerminatorShape(terminator MIRTerminator) error {
	if terminator.Kind == "" || !validOrigin(terminator.Origin) {
		return fmt.Errorf("invalid terminator")
	}
	switch terminator.Kind {
	case "return":
		if len(terminator.Successors) != 0 {
			return fmt.Errorf("return terminator has successors")
		}
	case "branch":
		if terminator.Value != 0 || len(terminator.Successors) != 1 {
			return fmt.Errorf("branch terminator has invalid value/successors")
		}
	case "condbranch":
		if terminator.Value == 0 || len(terminator.Successors) != 2 {
			return fmt.Errorf("conditional branch has invalid value/successors")
		}
	case "switch":
		if terminator.Value == 0 || len(terminator.Successors) == 0 {
			return fmt.Errorf("switch terminator has invalid value/successors")
		}
	default:
		return fmt.Errorf("unknown terminator kind %q", terminator.Kind)
	}
	return nil
}

// CanonicalHIR returns deterministic bytes and a content hash.
func CanonicalHIR(module HIRModule) ([]byte, string, error) {
	module.ContentHash = ""
	if err := VerifyHIR(module); err != nil {
		return nil, "", err
	}
	encoded, err := json.Marshal(module)
	if err != nil {
		return nil, "", err
	}
	digest := sha256.Sum256(encoded)
	hash := hex.EncodeToString(digest[:])
	module.ContentHash = hash
	encoded, err = json.Marshal(module)
	if err != nil {
		return nil, "", err
	}
	return encoded, hash, nil
}

// VerifyCanonicalHIR verifies both the HIR structure and its serialized
// content identity. VerifyHIR intentionally accepts an unhashed in-memory
// module; persisted pass artifacts must use this stronger boundary.
func VerifyCanonicalHIR(module HIRModule) error {
	claimed := module.ContentHash
	_, want, err := CanonicalHIR(module)
	if err != nil {
		return err
	}
	if claimed == "" || claimed != want {
		return fmt.Errorf("HIR content hash mismatch: got %q, want %q", claimed, want)
	}
	return nil
}

func validType(value TypeKind) bool {
	switch value {
	case TypeNumber, TypeVoid:
		return true
	default:
		return false
	}
}

func validEffect(value Effect) bool {
	switch value {
	case EffectPure, EffectRead, EffectWrite, EffectCall:
		return true
	default:
		return false
	}
}
