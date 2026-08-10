package bingo

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
)

// VerifyPhase2HIR validates the Phase 2B primitive CFG subset. The Phase 2A
// VerifyHIR entry point remains intentionally frozen to the number-add slice.
func VerifyPhase2HIR(module HIRModule) error {
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
		return fmt.Errorf("HIR logical capability requirements digest mismatch: got %q, want %q", module.Provenance.LogicalCapabilityRequirementsDigest, requirementsDigest)
	}
	if len(module.LogicalCapabilityRequirements) != 0 {
		return fmt.Errorf("Phase 2B primitive HIR does not bind runtime capabilities")
	}
	if len(module.Functions) == 0 {
		return fmt.Errorf("Phase 2B primitive HIR has no functions")
	}
	functions := make(map[FunctionID]HIRFunction, len(module.Functions))
	names := make(map[string]struct{}, len(module.Functions))
	exported := 0
	for index, function := range module.Functions {
		wantID := FunctionID(index + 1)
		if function.ID != wantID || function.Name == "" || !validPhase2HIRType(function.ReturnType) || !validOrigin(function.Origin) || len(function.Blocks) == 0 {
			return fmt.Errorf("Phase 2B primitive function %d is incomplete or not canonical dense ID %d", function.ID, wantID)
		}
		if _, duplicate := names[function.Name]; duplicate {
			return fmt.Errorf("Phase 2B primitive function name %q is duplicated", function.Name)
		}
		names[function.Name] = struct{}{}
		functions[function.ID] = function
		if function.Exported {
			exported++
		}
	}
	if exported != 1 {
		return fmt.Errorf("Phase 2B primitive HIR requires exactly one exported function, got %d", exported)
	}
	for _, function := range module.Functions {
		if err := verifyPhase2HIRFunction(function, functions); err != nil {
			return fmt.Errorf("function %s: %w", function.Name, err)
		}
	}
	return nil
}

func verifyPhase2HIRFunction(function HIRFunction, functions map[FunctionID]HIRFunction) error {
	blocks := make(map[BlockID]int, len(function.Blocks))
	values := make(map[ValueID]valueDefinition)
	nextValue := ValueID(1)
	for index, parameter := range function.Parameters {
		if parameter.Name == "" || parameter.Value != nextValue || !validPhase2HIRValueType(parameter.Type) || !validOrigin(parameter.Origin) {
			return fmt.Errorf("parameter %d is invalid or not canonical dense value %d", index, nextValue)
		}
		values[parameter.Value] = valueDefinition{typ: parameter.Type, block: -1, position: -1}
		nextValue++
	}
	for blockIndex, block := range function.Blocks {
		expectedBlockID := BlockID(blockIndex + 1)
		if block.ID != expectedBlockID {
			return fmt.Errorf("block ID %d is not canonical dense ID %d", block.ID, expectedBlockID)
		}
		if block.Operations == nil {
			return fmt.Errorf("block %d operations are missing", block.ID)
		}
		blocks[block.ID] = blockIndex
		seenNonPhi := false
		for operationIndex, operation := range block.Operations {
			if operation.ID != nextValue {
				return fmt.Errorf("operation value ID %d is not canonical dense ID %d", operation.ID, nextValue)
			}
			if err := validatePhase2HIROperationShape(operation); err != nil {
				return fmt.Errorf("block %d operation %d: %w", block.ID, operationIndex, err)
			}
			if _, duplicate := values[operation.ID]; duplicate {
				return fmt.Errorf("duplicate value %d", operation.ID)
			}
			if operation.Kind == "phi" {
				if seenNonPhi {
					return fmt.Errorf("block %d phi operation %d appears after a non-phi operation", block.ID, operation.ID)
				}
			} else {
				seenNonPhi = true
			}
			values[operation.ID] = valueDefinition{typ: operation.Type, block: blockIndex, position: operationIndex}
			nextValue++
		}
		if err := validatePhase2HIRTerminatorShape(block.Terminator); err != nil {
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
		predecessorIDs := make([]BlockID, len(predecessors[blockIndex]))
		for index, predecessor := range predecessors[blockIndex] {
			predecessorIDs[index] = function.Blocks[predecessor].ID
		}
		for operationIndex, operation := range block.Operations {
			switch operation.Kind {
			case "binary":
				for _, operand := range operation.Operands {
					if err := validateValueUse(operand, blockIndex, operationIndex, values, dominators); err != nil {
						return fmt.Errorf("operation %d: %w", operation.ID, err)
					}
				}
				left, right := values[operation.Operands[0]], values[operation.Operands[1]]
				if left.typ != TypeNumber || right.typ != TypeNumber || operation.Type != TypeNumber {
					return fmt.Errorf("binary operation %d requires number operands and result", operation.ID)
				}
			case "compare":
				for _, operand := range operation.Operands {
					if err := validateValueUse(operand, blockIndex, operationIndex, values, dominators); err != nil {
						return fmt.Errorf("operation %d: %w", operation.ID, err)
					}
				}
				left, right := values[operation.Operands[0]], values[operation.Operands[1]]
				if left.typ != TypeNumber || right.typ != TypeNumber || operation.Type != TypeBoolean {
					return fmt.Errorf("comparison operation %d requires number operands and boolean result", operation.ID)
				}
			case "phi":
				if !slices.Equal(operation.IncomingBlocks, predecessorIDs) {
					return fmt.Errorf("phi operation %d incoming blocks %v do not match canonical predecessors %v", operation.ID, operation.IncomingBlocks, predecessorIDs)
				}
				for index, operand := range operation.Operands {
					predecessor := predecessors[blockIndex][index]
					if err := validateValueUse(operand, predecessor, len(function.Blocks[predecessor].Operations), values, dominators); err != nil {
						return fmt.Errorf("phi operation %d incoming block %d: %w", operation.ID, predecessorIDs[index], err)
					}
					if values[operand].typ != operation.Type {
						return fmt.Errorf("phi operation %d incoming value %d has type %q, want %q", operation.ID, operand, values[operand].typ, operation.Type)
					}
				}
			case "call":
				for _, operand := range operation.Operands {
					if err := validateValueUse(operand, blockIndex, operationIndex, values, dominators); err != nil {
						return fmt.Errorf("operation %d: %w", operation.ID, err)
					}
				}
				callee, ok := functions[operation.Callee]
				if !ok {
					return fmt.Errorf("call operation %d targets missing function %d", operation.ID, operation.Callee)
				}
				if callee.ID >= function.ID {
					return fmt.Errorf("call operation %d must target an earlier non-recursive function", operation.ID)
				}
				if len(operation.Operands) != len(callee.Parameters) {
					return fmt.Errorf("call operation %d has %d arguments, want %d", operation.ID, len(operation.Operands), len(callee.Parameters))
				}
				for index, operand := range operation.Operands {
					if values[operand].typ != callee.Parameters[index].Type {
						return fmt.Errorf("call operation %d argument %d has type %q, want %q", operation.ID, index, values[operand].typ, callee.Parameters[index].Type)
					}
				}
				if operation.Type != callee.ReturnType {
					return fmt.Errorf("call operation %d result type %q disagrees with callee return type %q", operation.ID, operation.Type, callee.ReturnType)
				}
			case "is_nullish":
				if err := validateValueUse(operation.Operands[0], blockIndex, operationIndex, values, dominators); err != nil {
					return fmt.Errorf("operation %d: %w", operation.ID, err)
				}
				if values[operation.Operands[0]].typ != TypeNullableNumber || operation.Type != TypeBoolean {
					return fmt.Errorf("is_nullish operation %d requires nullable-number input and boolean result", operation.ID)
				}
			case "unwrap_nullable":
				if err := validateValueUse(operation.Operands[0], blockIndex, operationIndex, values, dominators); err != nil {
					return fmt.Errorf("operation %d: %w", operation.ID, err)
				}
				if values[operation.Operands[0]].typ != TypeNullableNumber || operation.Type != TypeNumber {
					return fmt.Errorf("unwrap_nullable operation %d requires nullable-number input and number result", operation.ID)
				}
			}
		}
		if err := verifyNullableUnwrapGuard(function, blockIndex, block, values); err != nil {
			return err
		}
		terminator := block.Terminator
		switch terminator.Kind {
		case "return":
			if err := validateValueUse(terminator.Value, blockIndex, len(block.Operations), values, dominators); err != nil {
				return fmt.Errorf("return terminator: %w", err)
			}
			if values[terminator.Value].typ != function.ReturnType {
				return fmt.Errorf("return value %d has type %q, want %q", terminator.Value, values[terminator.Value].typ, function.ReturnType)
			}
		case "condbranch":
			if err := validateValueUse(terminator.Value, blockIndex, len(block.Operations), values, dominators); err != nil {
				return fmt.Errorf("condbranch terminator: %w", err)
			}
			if values[terminator.Value].typ != TypeBoolean {
				return fmt.Errorf("conditional branch value %d has type %q, want %q", terminator.Value, values[terminator.Value].typ, TypeBoolean)
			}
		}
	}
	return nil
}

func validatePhase2HIROperationShape(operation HIROp) error {
	if operation.ID == 0 || operation.Kind == "" || !validPhase2HIRValueType(operation.Type) || !validEffect(operation.Effect) || !validOrigin(operation.Origin) {
		return fmt.Errorf("invalid operation %d", operation.ID)
	}
	if err := validateLogicalCapabilityRequirements(operation.LogicalCapabilityRequirements); err != nil {
		return fmt.Errorf("operation %d has invalid logical capability requirements: %w", operation.ID, err)
	}
	if len(operation.LogicalCapabilityRequirements) != 0 {
		return fmt.Errorf("operation %d is outside the Phase 2B primitive operation subset", operation.ID)
	}
	switch operation.Kind {
	case "binary":
		if len(operation.Operands) != 2 || len(operation.IncomingBlocks) != 0 || operation.Operator != "+" || operation.Callee != 0 || operation.Effect != EffectPure {
			return fmt.Errorf("operation %d is outside the Phase 2B primitive operation subset", operation.ID)
		}
	case "compare":
		if len(operation.Operands) != 2 || len(operation.IncomingBlocks) != 0 || operation.Operator != "<" || operation.Callee != 0 || operation.Effect != EffectPure {
			return fmt.Errorf("operation %d is outside the Phase 2B primitive operation subset", operation.ID)
		}
	case "phi":
		if len(operation.Operands) < 2 || len(operation.Operands) != len(operation.IncomingBlocks) || operation.Operator != "" || operation.Callee != 0 || operation.Effect != EffectPure {
			return fmt.Errorf("operation %d is outside the Phase 2B primitive operation subset", operation.ID)
		}
	case "call":
		if len(operation.Operands) == 0 || len(operation.IncomingBlocks) != 0 || operation.Operator != "" || operation.Callee == 0 || operation.Effect != EffectCall {
			return fmt.Errorf("operation %d is outside the Phase 2B primitive operation subset", operation.ID)
		}
	case "is_nullish":
		if len(operation.Operands) != 1 || len(operation.IncomingBlocks) != 0 || operation.Operator != "" || operation.Callee != 0 || operation.Effect != EffectPure {
			return fmt.Errorf("operation %d is outside the Phase 2B primitive operation subset", operation.ID)
		}
	case "unwrap_nullable":
		if len(operation.Operands) != 1 || len(operation.IncomingBlocks) != 0 || operation.Operator != "" || operation.Callee != 0 || operation.Effect != EffectPure {
			return fmt.Errorf("operation %d is outside the Phase 2B primitive operation subset", operation.ID)
		}
	default:
		return fmt.Errorf("operation %d is outside the Phase 2B primitive operation subset", operation.ID)
	}
	return nil
}

func validatePhase2HIRTerminatorShape(terminator HIRTerminator) error {
	if terminator.Kind == "" || !validOrigin(terminator.Origin) {
		return fmt.Errorf("invalid terminator")
	}
	switch terminator.Kind {
	case "return":
		if terminator.Value == 0 || len(terminator.Successors) != 0 {
			return fmt.Errorf("return terminator requires one value and no successors")
		}
	case "branch":
		if terminator.Value != 0 || len(terminator.Successors) != 1 {
			return fmt.Errorf("branch terminator requires exactly one successor")
		}
	case "condbranch":
		if terminator.Value == 0 || len(terminator.Successors) != 2 || terminator.Successors[0] == terminator.Successors[1] {
			return fmt.Errorf("condbranch terminator requires a value and two distinct successors")
		}
	default:
		return fmt.Errorf("terminator kind %q is outside the Phase 2B primitive CFG subset", terminator.Kind)
	}
	return nil
}

func validPhase2HIRType(value TypeKind) bool {
	return value == TypeNumber || value == TypeBoolean
}

func validPhase2HIRValueType(value TypeKind) bool {
	return value == TypeNumber || value == TypeBoolean || value == TypeNullableNumber
}

// verifyNullableUnwrapGuard prevents a nullable payload from being observed
// without the matching nullish branch proof. The first slice intentionally
// accepts one canonical coalesce shape rather than trusting an optimizer to
// reconstruct this source-level safety condition.
func verifyNullableUnwrapGuard(function HIRFunction, blockIndex int, block HIRBlock, values map[ValueID]valueDefinition) error {
	for _, operation := range block.Operations {
		if operation.Kind != "unwrap_nullable" {
			continue
		}
		if (function.Name != "coalesce" && function.Name != "coalesceAssign") || len(function.Blocks) != 4 || blockIndex != 2 {
			return fmt.Errorf("unwrap_nullable operation %d is outside the canonical coalesce CFG", operation.ID)
		}
		entry := function.Blocks[0]
		if len(entry.Operations) != 1 || entry.Operations[0].Kind != "is_nullish" || entry.Operations[0].Operands[0] != operation.Operands[0] ||
			entry.Terminator.Kind != "condbranch" || !slices.Equal(entry.Terminator.Successors, []BlockID{2, 3}) ||
			entry.Terminator.Value != entry.Operations[0].ID {
			return fmt.Errorf("unwrap_nullable operation %d lacks a matching non-nullish branch proof", operation.ID)
		}
		if values[operation.Operands[0]].typ != TypeNullableNumber {
			return fmt.Errorf("unwrap_nullable operation %d has non-nullable input", operation.ID)
		}
	}
	return nil
}

func CanonicalPhase2HIR(module HIRModule) ([]byte, string, error) {
	module.ContentHash = ""
	if err := VerifyPhase2HIR(module); err != nil {
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

func VerifyCanonicalPhase2HIR(module HIRModule) error {
	claimed := module.ContentHash
	_, want, err := CanonicalPhase2HIR(module)
	if err != nil {
		return err
	}
	if claimed == "" || claimed != want {
		return fmt.Errorf("Phase 2B HIR content hash mismatch: got %q, want %q", claimed, want)
	}
	return nil
}
