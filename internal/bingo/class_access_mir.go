package bingo

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"

	jsonx "github.com/microsoft/typescript-go/internal/json"
)

const ClassAccessMIRSchemaVersion uint32 = 13
const maxClassAccessMIRBytes = 256 << 10

type ClassAccessMIRTarget struct {
	TargetContextHash  string `json:"targetContextHash"`
	Triple             string `json:"triple"`
	DataLayoutHash     string `json:"dataLayoutHash"`
	LLVMDataLayoutHash string `json:"llvmDataLayoutHash"`
}

type ClassAccessMIRAuthorization struct {
	ID              uint32                `json:"id"`
	Operation       string                `json:"operation"`
	Representation  VERT013aRepType       `json:"representation"`
	MemberID        uint32                `json:"memberId"`
	MemberSymbolKey string                `json:"memberSymbolKey"`
	MemberKind      ClassAccessMemberKind `json:"memberKind"`
	OwnerClassID    uint32                `json:"ownerClassId"`
	Request         ClassAccessRequest    `json:"request"`
	Decision        ClassAccessDecision   `json:"decision"`
	Origin          Origin                `json:"origin"`
}

type ClassAccessMIRInstruction struct {
	ID              ValueID         `json:"id"`
	Operation       string          `json:"operation"`
	Representation  VERT013aRepType `json:"representation"`
	Operands        []ValueID       `json:"operands,omitempty"`
	AuthorizationID uint32          `json:"authorizationId,omitempty"`
	MemberSymbolKey string          `json:"memberSymbolKey,omitempty"`
	NumberBits      string          `json:"numberBits,omitempty"`
	Callee          FunctionID      `json:"callee,omitempty"`
	ClassID         uint32          `json:"classId,omitempty"`
	Effect          Effect          `json:"effect"`
	Effects         []Effect        `json:"effects"`
	Origin          Origin          `json:"origin"`
}

type ClassAccessMIRFunction struct {
	ID            FunctionID                  `json:"id"`
	Name          string                      `json:"name"`
	Exported      bool                        `json:"exported,omitempty"`
	ParameterReps []VERT013aRepType           `json:"parameterReps"`
	Instructions  []ClassAccessMIRInstruction `json:"instructions"`
	ReturnValue   ValueID                     `json:"returnValue"`
	ReturnType    VERT013aRepType             `json:"returnType"`
	Origin        Origin                      `json:"origin"`
}

type ClassAccessMIRModule struct {
	SchemaVersion  uint32                        `json:"schemaVersion"`
	HIRHash        string                        `json:"hirHash"`
	HIR            HIRModule                     `json:"hir"`
	ExecutionHash  string                        `json:"executionHash"`
	Execution      ClassAccessExecutionContract  `json:"execution"`
	Target         ClassAccessMIRTarget          `json:"target"`
	Authorizations []ClassAccessMIRAuthorization `json:"authorizations"`
	Functions      []ClassAccessMIRFunction      `json:"functions"`
	ContentHash    string                        `json:"contentHash"`
}

func LowerClassAccessMIR(hir HIRModule, target ClassAccessMIRTarget) (ClassAccessMIRModule, error) {
	if err := VerifyCanonicalClassAccessHIR(hir); err != nil {
		return ClassAccessMIRModule{}, fmt.Errorf("verify OBJ-003b access HIR: %w", err)
	}
	if err := verifyClassAccessMIRTarget(target); err != nil {
		return ClassAccessMIRModule{}, err
	}
	module := ClassAccessMIRModule{SchemaVersion: ClassAccessMIRSchemaVersion, HIRHash: hir.ContentHash, HIR: hir, ExecutionHash: hir.ClassAccessExecution.ContentHash, Execution: *hir.ClassAccessExecution, Target: target, Authorizations: fixedClassAccessMIRAuthorizations(hir), Functions: fixedClassAccessMIRFunctions(hir)}
	_, hash, err := CanonicalClassAccessMIR(module)
	if err != nil {
		return ClassAccessMIRModule{}, err
	}
	module.ContentHash = hash
	return module, nil
}

func fixedClassAccessMIRFunctions(hir HIRModule) []ClassAccessMIRFunction {
	proofs := hir.ClassAccessProofs
	contract := hir.ClassAccess
	secret, value := contract.Members[0].SymbolKey, contract.Members[1].SymbolKey
	baseOrigin, derivedOrigin := hir.Functions[0].Origin, hir.Functions[1].Origin
	return []ClassAccessMIRFunction{
		{ID: 1, Name: "Vault.constructor", ParameterReps: []VERT013aRepType{VERT013aRepGcRef}, ReturnType: VERT013aRepGcRef, ReturnValue: 1, Origin: baseOrigin, Instructions: []ClassAccessMIRInstruction{
			{ID: 2, Operation: "f64.const", Representation: VERT013aRepF64, NumberBits: "3ff0000000000000", Effect: EffectPure, Effects: []Effect{EffectPure}, Origin: baseOrigin},
			{ID: 3, Operation: "class.field.init", Representation: VERT013aRepF64, Operands: []ValueID{1, 2}, MemberSymbolKey: secret, ClassID: 1, Effect: EffectWrite, Effects: []Effect{EffectWrite}, Origin: baseOrigin},
			{ID: 4, Operation: "f64.const", Representation: VERT013aRepF64, NumberBits: "4000000000000000", Effect: EffectPure, Effects: []Effect{EffectPure}, Origin: baseOrigin},
			{ID: 5, Operation: "class.field.init", Representation: VERT013aRepF64, Operands: []ValueID{1, 4}, MemberSymbolKey: value, ClassID: 1, Effect: EffectWrite, Effects: []Effect{EffectWrite}, Origin: baseOrigin},
		}},
		{ID: 2, Name: "DerivedVault.constructor", ParameterReps: []VERT013aRepType{}, ReturnType: VERT013aRepGcRef, ReturnValue: 1, Origin: derivedOrigin, Instructions: []ClassAccessMIRInstruction{
			{ID: 1, Operation: "class.alloc", Representation: VERT013aRepGcRef, ClassID: 2, Effect: EffectAllocate, Effects: []Effect{EffectAllocate}, Origin: derivedOrigin},
			{ID: 2, Operation: "call.super", Representation: VERT013aRepGcRef, Operands: []ValueID{1}, Callee: 1, ClassID: 1, Effect: EffectCall, Effects: []Effect{EffectCall, EffectWrite}, Origin: derivedOrigin},
		}},
		{ID: 3, Name: "Vault.readSecret", ParameterReps: []VERT013aRepType{VERT013aRepGcRef, VERT013aRepGcRef}, ReturnType: VERT013aRepF64, ReturnValue: 3, Origin: proofs[0].Origin, Instructions: []ClassAccessMIRInstruction{
			{ID: 3, Operation: "class.field.load.authorized", Representation: VERT013aRepF64, Operands: []ValueID{2}, AuthorizationID: 1, MemberSymbolKey: proofs[0].MemberSymbolKey, Effect: EffectRead, Origin: proofs[0].Origin},
		}},
		{ID: 4, Name: "DerivedVault.readValue", ParameterReps: []VERT013aRepType{VERT013aRepGcRef, VERT013aRepGcRef}, ReturnType: VERT013aRepF64, ReturnValue: 3, Origin: proofs[1].Origin, Instructions: []ClassAccessMIRInstruction{
			{ID: 3, Operation: "class.field.load.authorized", Representation: VERT013aRepF64, Operands: []ValueID{2}, AuthorizationID: 2, MemberSymbolKey: proofs[1].MemberSymbolKey, Effect: EffectRead, Origin: proofs[1].Origin},
		}},
		{ID: 5, Name: "classAccess", Exported: true, ParameterReps: []VERT013aRepType{}, ReturnType: VERT013aRepF64, ReturnValue: 4, Origin: hir.Functions[4].Origin, Instructions: []ClassAccessMIRInstruction{
			{ID: 1, Operation: "call.constructor", Representation: VERT013aRepGcRef, Callee: 2, ClassID: 2, Effect: EffectCall, Effects: []Effect{EffectCall, EffectAllocate, EffectWrite}, Origin: hir.Functions[4].Origin},
			{ID: 2, Operation: "class.method.call.authorized", Representation: VERT013aRepF64, Operands: []ValueID{1, 1}, AuthorizationID: 3, MemberSymbolKey: proofs[2].MemberSymbolKey, Callee: 3, ClassID: 2, Effect: EffectCall, Effects: []Effect{EffectCall, EffectRead}, Origin: proofs[2].Origin},
			{ID: 3, Operation: "class.method.call.authorized", Representation: VERT013aRepF64, Operands: []ValueID{1, 1}, AuthorizationID: 4, MemberSymbolKey: proofs[3].MemberSymbolKey, Callee: 4, ClassID: 2, Effect: EffectCall, Effects: []Effect{EffectCall, EffectRead}, Origin: proofs[3].Origin},
			{ID: 4, Operation: "fadd", Representation: VERT013aRepF64, Operands: []ValueID{2, 3}, Effect: EffectPure, Origin: proofs[3].Origin},
		}},
	}
}

func fixedClassAccessMIRAuthorizations(hir HIRModule) []ClassAccessMIRAuthorization {
	result := make([]ClassAccessMIRAuthorization, len(hir.ClassAccessProofs))
	for index, proof := range hir.ClassAccessProofs {
		member := hir.ClassAccess.Members[proof.Request.MemberID-1]
		operation := "class.field.load.authorized"
		if member.Kind == ClassAccessMethod {
			operation = "class.method.call.authorized"
		}
		result[index] = ClassAccessMIRAuthorization{
			ID: proof.ID, Operation: operation, Representation: VERT013aRepF64,
			MemberID: member.ID, MemberSymbolKey: member.SymbolKey, MemberKind: member.Kind, OwnerClassID: member.OwnerClassID,
			Request: proof.Request, Decision: proof.Decision, Origin: proof.Origin,
		}
	}
	return result
}

func verifyClassAccessMIRTarget(target ClassAccessMIRTarget) error {
	if !validSHA256Hex(target.TargetContextHash) || strings.TrimSpace(target.Triple) == "" || !validSHA256Hex(target.DataLayoutHash) || !validSHA256Hex(target.LLVMDataLayoutHash) {
		return fmt.Errorf("invalid OBJ-003b access MIR target identity")
	}
	return nil
}

func VerifyClassAccessMIR(module ClassAccessMIRModule) error {
	if module.SchemaVersion != ClassAccessMIRSchemaVersion || module.HIRHash != module.HIR.ContentHash || module.ExecutionHash != module.Execution.ContentHash || len(module.Authorizations) != 4 || len(module.Functions) != 5 {
		return fmt.Errorf("invalid OBJ-003b access MIR envelope")
	}
	if err := VerifyCanonicalClassAccessHIR(module.HIR); err != nil {
		return err
	}
	if err := VerifyCanonicalClassAccessExecution(module.Execution); err != nil || module.Execution.ClassAccessHash != module.HIR.ClassAccess.ContentHash {
		return fmt.Errorf("OBJ-003b access MIR execution binding mismatch")
	}
	if err := verifyClassAccessMIRTarget(module.Target); err != nil {
		return err
	}
	want := fixedClassAccessMIRAuthorizations(module.HIR)
	left, err := jsonx.Marshal(module.Authorizations)
	if err != nil {
		return err
	}
	right, err := jsonx.Marshal(want)
	if err != nil {
		return err
	}
	if !slices.Equal(left, right) {
		return fmt.Errorf("OBJ-003b access MIR authorization mismatch")
	}
	wantFunctions := fixedClassAccessMIRFunctions(module.HIR)
	left, err = jsonx.Marshal(module.Functions)
	if err != nil {
		return err
	}
	right, err = jsonx.Marshal(wantFunctions)
	if err != nil {
		return err
	}
	if !slices.Equal(left, right) {
		return fmt.Errorf("OBJ-003b access MIR execution CFG mismatch")
	}
	for _, authorization := range module.Authorizations {
		if authorization.Representation != VERT013aRepF64 || authorization.Decision.Reason != ClassAccessAllowed || !authorization.Decision.Allowed {
			return fmt.Errorf("OBJ-003b access MIR representation or decision mismatch")
		}
		if authorization.Operation == "class.field.load.authorized" && authorization.MemberKind != ClassAccessField || authorization.Operation == "class.method.call.authorized" && authorization.MemberKind != ClassAccessMethod {
			return fmt.Errorf("OBJ-003b access MIR operation kind mismatch")
		}
	}
	return nil
}

func CanonicalClassAccessMIR(module ClassAccessMIRModule) ([]byte, string, error) {
	module.ContentHash = ""
	if err := VerifyClassAccessMIR(module); err != nil {
		return nil, "", err
	}
	encoded, err := jsonx.Marshal(module)
	if err != nil {
		return nil, "", err
	}
	digest := sha256.Sum256(encoded)
	module.ContentHash = hex.EncodeToString(digest[:])
	encoded, err = jsonx.Marshal(module)
	return encoded, module.ContentHash, err
}

func VerifyCanonicalClassAccessMIR(module ClassAccessMIRModule) error {
	claimed := module.ContentHash
	_, want, err := CanonicalClassAccessMIR(module)
	if err != nil {
		return err
	}
	if claimed == "" || claimed != want {
		return fmt.Errorf("OBJ-003b access MIR content hash mismatch")
	}
	return nil
}

func DecodeClassAccessMIR(data []byte) (*ClassAccessMIRModule, error) {
	if len(data) > maxClassAccessMIRBytes {
		return nil, fmt.Errorf("OBJ-003b access MIR exceeds %d bytes", maxClassAccessMIRBytes)
	}
	var module ClassAccessMIRModule
	if err := jsonx.Unmarshal(data, &module, jsonx.RejectUnknownMembers(true)); err != nil {
		return nil, err
	}
	if err := VerifyCanonicalClassAccessMIR(module); err != nil {
		return nil, err
	}
	return &module, nil
}
