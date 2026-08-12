package bingo

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	jsonx "github.com/microsoft/typescript-go/internal/json"
)

const ClassAccessContractSchemaVersion uint32 = 1
const maxClassAccessContractBytes = 96 << 10

type ClassMemberVisibility string
type ClassAccessMemberKind string

const (
	ClassMemberPublic    ClassMemberVisibility = "public"
	ClassMemberProtected ClassMemberVisibility = "protected"
	ClassMemberPrivate   ClassMemberVisibility = "private"
	ClassAccessField     ClassAccessMemberKind = "field"
	ClassAccessMethod    ClassAccessMemberKind = "method"
)

type ClassAccessClass struct {
	ID              uint32 `json:"id"`
	SymbolKey       string `json:"symbolKey"`
	InstanceTypeKey string `json:"instanceTypeKey"`
	BaseClassID     uint32 `json:"baseClassId,omitempty"`
}

type ClassAccessMember struct {
	ID              uint32                `json:"id"`
	OwnerClassID    uint32                `json:"ownerClassId"`
	SymbolKey       string                `json:"symbolKey"`
	Name            string                `json:"name"`
	Kind            ClassAccessMemberKind `json:"kind"`
	Visibility      ClassMemberVisibility `json:"visibility"`
	PrivateIdentity string                `json:"privateIdentity,omitempty"`
}

type ClassAccessContract struct {
	SchemaVersion uint32              `json:"schemaVersion"`
	Classes       []ClassAccessClass  `json:"classes"`
	Members       []ClassAccessMember `json:"members"`
	ContentHash   string              `json:"contentHash"`
}

type ClassAccessRequest struct {
	AccessingClassID uint32 `json:"accessingClassId,omitempty"`
	ReceiverClassID  uint32 `json:"receiverClassId"`
	MemberID         uint32 `json:"memberId"`
	PrivateIdentity  string `json:"privateIdentity,omitempty"`
}

type ClassAccessDecision struct {
	Allowed bool   `json:"allowed"`
	Reason  string `json:"reason"`
}

const (
	ClassAccessAllowed                       = "class.access.allowed"
	ClassAccessUnknownAccessingClass         = "class.access.unknown_accessing_class"
	ClassAccessUnknownReceiverClass          = "class.access.unknown_receiver_class"
	ClassAccessUnknownMember                 = "class.access.unknown_member"
	ClassAccessReceiverOutsideOwnerFamily    = "class.access.receiver_outside_owner_family"
	ClassAccessPrivateIdentityMismatch       = "class.access.private_identity_mismatch"
	ClassAccessPrivateOutsideOwner           = "class.access.private_outside_owner"
	ClassAccessProtectedOutsideFamily        = "class.access.protected_outside_family"
	ClassAccessProtectedReceiverIncompatible = "class.access.protected_receiver_incompatible"
)

func VerifyClassAccessContract(contract ClassAccessContract) error {
	if contract.SchemaVersion != ClassAccessContractSchemaVersion || len(contract.Classes) == 0 || len(contract.Members) == 0 {
		return fmt.Errorf("invalid class access contract header")
	}
	classes := make(map[uint32]ClassAccessClass, len(contract.Classes))
	symbols := make(map[string]struct{}, len(contract.Classes))
	for index, class := range contract.Classes {
		if class.ID != uint32(index+1) || strings.TrimSpace(class.SymbolKey) == "" || !validSHA256Hex(class.InstanceTypeKey) {
			return fmt.Errorf("invalid class access class %d", index+1)
		}
		if _, exists := symbols[class.SymbolKey]; exists {
			return fmt.Errorf("duplicate class access symbol %q", class.SymbolKey)
		}
		if class.BaseClassID != 0 && classes[class.BaseClassID].InstanceTypeKey == class.InstanceTypeKey {
			return fmt.Errorf("class access derived type key aliases base")
		}
		if class.BaseClassID >= class.ID {
			return fmt.Errorf("class access inheritance is not declaration ordered")
		}
		classes[class.ID] = class
		symbols[class.SymbolKey] = struct{}{}
	}
	memberSymbols := make(map[string]struct{}, len(contract.Members))
	privateIdentities := make(map[string]struct{})
	for index, member := range contract.Members {
		if member.ID != uint32(index+1) || classes[member.OwnerClassID].ID == 0 || strings.TrimSpace(member.SymbolKey) == "" || strings.TrimSpace(member.Name) == "" {
			return fmt.Errorf("invalid class access member %d", index+1)
		}
		if _, exists := memberSymbols[member.SymbolKey]; exists {
			return fmt.Errorf("duplicate class access member symbol %q", member.SymbolKey)
		}
		memberSymbols[member.SymbolKey] = struct{}{}
		if member.Kind != ClassAccessField && member.Kind != ClassAccessMethod {
			return fmt.Errorf("unsupported class access member kind %q", member.Kind)
		}
		switch member.Visibility {
		case ClassMemberPublic, ClassMemberProtected:
			if member.PrivateIdentity != "" {
				return fmt.Errorf("non-private member %q has private identity", member.Name)
			}
		case ClassMemberPrivate:
			if strings.TrimSpace(member.PrivateIdentity) == "" {
				return fmt.Errorf("private member %q has no nominal identity", member.Name)
			}
			if _, exists := privateIdentities[member.PrivateIdentity]; exists {
				return fmt.Errorf("duplicate private member identity %q", member.PrivateIdentity)
			}
			privateIdentities[member.PrivateIdentity] = struct{}{}
		default:
			return fmt.Errorf("unsupported member visibility %q", member.Visibility)
		}
	}
	return nil
}

func PlanClassMemberAccess(contract ClassAccessContract, request ClassAccessRequest) (ClassAccessDecision, error) {
	if err := VerifyClassAccessContract(contract); err != nil {
		return ClassAccessDecision{}, err
	}
	classes := make(map[uint32]ClassAccessClass, len(contract.Classes))
	for _, class := range contract.Classes {
		classes[class.ID] = class
	}
	if request.AccessingClassID != 0 && classes[request.AccessingClassID].ID == 0 {
		return deniedClassAccess(ClassAccessUnknownAccessingClass), nil
	}
	if classes[request.ReceiverClassID].ID == 0 {
		return deniedClassAccess(ClassAccessUnknownReceiverClass), nil
	}
	if request.MemberID == 0 || int(request.MemberID) > len(contract.Members) {
		return deniedClassAccess(ClassAccessUnknownMember), nil
	}
	member := contract.Members[request.MemberID-1]
	if !classAccessIsDescendant(classes, request.ReceiverClassID, member.OwnerClassID) {
		return deniedClassAccess(ClassAccessReceiverOutsideOwnerFamily), nil
	}
	switch member.Visibility {
	case ClassMemberPublic:
		if request.PrivateIdentity != "" {
			return deniedClassAccess(ClassAccessPrivateIdentityMismatch), nil
		}
	case ClassMemberPrivate:
		if request.PrivateIdentity != member.PrivateIdentity {
			return deniedClassAccess(ClassAccessPrivateIdentityMismatch), nil
		}
		if request.AccessingClassID != member.OwnerClassID {
			return deniedClassAccess(ClassAccessPrivateOutsideOwner), nil
		}
	case ClassMemberProtected:
		if request.PrivateIdentity != "" {
			return deniedClassAccess(ClassAccessPrivateIdentityMismatch), nil
		}
		if request.AccessingClassID == 0 || !classAccessIsDescendant(classes, request.AccessingClassID, member.OwnerClassID) {
			return deniedClassAccess(ClassAccessProtectedOutsideFamily), nil
		}
		if !classAccessIsDescendant(classes, request.ReceiverClassID, request.AccessingClassID) {
			return deniedClassAccess(ClassAccessProtectedReceiverIncompatible), nil
		}
	}
	return ClassAccessDecision{Allowed: true, Reason: ClassAccessAllowed}, nil
}

func deniedClassAccess(reason string) ClassAccessDecision {
	return ClassAccessDecision{Reason: reason}
}

func classAccessIsDescendant(classes map[uint32]ClassAccessClass, classID, ancestorID uint32) bool {
	for classID != 0 {
		if classID == ancestorID {
			return true
		}
		classID = classes[classID].BaseClassID
	}
	return false
}

func CanonicalClassAccessContract(contract ClassAccessContract) ([]byte, string, error) {
	contract.ContentHash = ""
	if err := VerifyClassAccessContract(contract); err != nil {
		return nil, "", err
	}
	encoded, err := jsonx.Marshal(contract)
	if err != nil {
		return nil, "", err
	}
	digest := sha256.Sum256(encoded)
	contract.ContentHash = hex.EncodeToString(digest[:])
	encoded, err = jsonx.Marshal(contract)
	return encoded, contract.ContentHash, err
}

func VerifyCanonicalClassAccessContract(contract ClassAccessContract) error {
	claimed := contract.ContentHash
	_, want, err := CanonicalClassAccessContract(contract)
	if err != nil {
		return err
	}
	if claimed == "" || claimed != want {
		return fmt.Errorf("class access contract content hash mismatch")
	}
	return nil
}

func DecodeClassAccessContract(data []byte) (*ClassAccessContract, error) {
	if len(data) > maxClassAccessContractBytes {
		return nil, fmt.Errorf("class access contract exceeds %d bytes", maxClassAccessContractBytes)
	}
	var contract ClassAccessContract
	if err := jsonx.Unmarshal(data, &contract, jsonx.RejectUnknownMembers(true)); err != nil {
		return nil, fmt.Errorf("decode class access contract: %w", err)
	}
	if err := VerifyCanonicalClassAccessContract(contract); err != nil {
		return nil, err
	}
	return &contract, nil
}
