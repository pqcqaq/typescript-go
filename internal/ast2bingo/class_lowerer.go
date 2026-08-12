package ast2bingo

import (
	"fmt"
	"slices"

	"github.com/microsoft/typescript-go/internal/bingo"
)

// LowerVERT013aClassContract reconstructs only the frozen base-class fixture.
// It consumes checker-proven snapshot facts and deliberately admits no derived,
// static, private, computed, or accessor class shape.
func LowerVERT013aClassContract(snapshot ProgramSnapshot) (bingo.ClassContract, error) {
	indexes := indexPrimitiveSnapshot(snapshot)
	classes := make([]NodeSnapshot, 0, 1)
	for _, node := range snapshot.Nodes {
		if node.Kind == "KindClassDeclaration" {
			classes = append(classes, node)
		}
	}
	if len(classes) != 1 {
		return bingo.ClassContract{}, fmt.Errorf("VERT-013a requires exactly one class declaration")
	}
	class := classes[0]
	classSymbol, ok := symbolWithValueDeclaration(indexes, class.ID)
	if !ok || classSymbol.Name != "Counter" || class.NarrowedType == 0 {
		return bingo.ClassContract{}, fmt.Errorf("VERT-013a class identity is invalid")
	}
	instanceType, ok := indexes.Types[class.NarrowedType]
	if !ok || instanceType.Symbol != classSymbol.ID || len(instanceType.BaseTypes) != 0 || len(instanceType.Properties) != 2 || len(instanceType.PropertyFacts) != 2 {
		return bingo.ClassContract{}, fmt.Errorf("VERT-013a instance type is invalid")
	}
	var field, constructor, method NodeSnapshot
	nameCount := 0
	for _, childID := range class.Children {
		child, ok := indexes.Nodes[childID]
		if !ok {
			return bingo.ClassContract{}, fmt.Errorf("VERT-013a class child is missing")
		}
		switch child.Kind {
		case "KindIdentifier":
			if child.SyntaxPayload.Text != "Counter" || child.Symbol != classSymbol.ID || nameCount != 0 {
				return bingo.ClassContract{}, fmt.Errorf("VERT-013a class name is invalid")
			}
			nameCount++
		case "KindPropertyDeclaration":
			if field.ID != "" {
				return bingo.ClassContract{}, fmt.Errorf("VERT-013a has multiple fields")
			}
			field = child
		case "KindConstructor":
			if constructor.ID != "" {
				return bingo.ClassContract{}, fmt.Errorf("VERT-013a has multiple constructors")
			}
			constructor = child
		case "KindMethodDeclaration":
			if method.ID != "" {
				return bingo.ClassContract{}, fmt.Errorf("VERT-013a has multiple methods")
			}
			method = child
		default:
			return bingo.ClassContract{}, fmt.Errorf("VERT-013a rejects class child %s", child.Kind)
		}
	}
	if nameCount != 1 || field.ID == "" || constructor.ID == "" || method.ID == "" || !(field.Span.Start < constructor.Span.Start && constructor.Span.Start < method.Span.Start) {
		return bingo.ClassContract{}, fmt.Errorf("VERT-013a class member order is invalid")
	}
	fieldSymbol, ok := symbolWithValueDeclaration(indexes, field.ID)
	if !ok || fieldSymbol.Name != "value" || fieldSymbol.Parent != classSymbol.ID || bingoTypeMust(fieldSymbol.Type, indexes.Types) != bingo.TypeNumber {
		return bingo.ClassContract{}, fmt.Errorf("VERT-013a field identity is invalid")
	}
	methodSymbol, ok := symbolWithValueDeclaration(indexes, method.ID)
	if !ok || methodSymbol.Name != "increment" || methodSymbol.Parent != classSymbol.ID {
		return bingo.ClassContract{}, fmt.Errorf("VERT-013a method identity is invalid")
	}
	constructorSignature, ok := signatureForDeclaration(indexes, constructor.ID)
	if !ok || !isUnaryNumberConstructor(constructorSignature, class.NarrowedType, indexes) {
		return bingo.ClassContract{}, fmt.Errorf("VERT-013a constructor signature is invalid")
	}
	methodSignature, ok := signatureForDeclaration(indexes, method.ID)
	if !ok || !isZeroArgumentNumberMethod(methodSignature, indexes) {
		return bingo.ClassContract{}, fmt.Errorf("VERT-013a method signature is invalid")
	}
	if !classPropertyFactsMatch(instanceType, fieldSymbol, methodSymbol, methodSignature.ID, indexes) {
		return bingo.ClassContract{}, fmt.Errorf("VERT-013a instance property facts are invalid")
	}
	if err := verifyVERT013aUseSites(snapshot, indexes, class, classSymbol.ID, fieldSymbol.ID, methodSymbol.ID, constructorSignature.ID, methodSignature.ID); err != nil {
		return bingo.ClassContract{}, err
	}
	contract := bingo.ClassContract{SchemaVersion: bingo.ClassContractSchemaVersion, Classes: []bingo.ClassDeclarationContract{{
		ID: 1, SymbolKey: string(classSymbol.ID), InstanceTypeKey: instanceType.CanonicalHash,
		Constructor:    bingo.ClassConstructorContract{SymbolKey: string(constructor.ID), Signature: "cdecl(ptr,f64)->void", AllocatesReceiver: true, ReturnsOwnReceiver: true},
		Fields:         []bingo.ClassFieldContract{{ID: 1, SymbolKey: string(fieldSymbol.ID), Name: "value", Type: bingo.TypeNumber, Visibility: "public", Mutable: true, Storage: bingo.ClassFieldInstanceSlot, SourceOrder: 1}},
		Methods:        []bingo.ClassMethodContract{{ID: 1, SymbolKey: string(methodSymbol.ID), Name: "increment", Signature: "cdecl(ptr)->f64", Visibility: "public", RequiresReceiver: true, SourceOrder: 3}},
		Initialization: []bingo.ClassInitializationStep{{ID: 1, Kind: bingo.ClassInitAllocate}, {ID: 2, Kind: bingo.ClassInitField, FieldID: 1, SourceOrder: 1}, {ID: 3, Kind: bingo.ClassInitBody, SourceOrder: 2}},
	}}}
	_, hash, err := bingo.CanonicalClassContract(contract)
	if err != nil {
		return bingo.ClassContract{}, err
	}
	contract.ContentHash = hash
	return contract, nil
}

func symbolWithValueDeclaration(indexes snapshotSemanticFactIndexes, declaration NodeID) (SymbolSnapshot, bool) {
	var found SymbolSnapshot
	for _, symbol := range indexes.Symbols {
		if symbol.ValueDeclaration != declaration {
			continue
		}
		if found.ID != "" {
			return SymbolSnapshot{}, false
		}
		found = symbol
	}
	return found, found.ID != ""
}

func signatureForDeclaration(indexes snapshotSemanticFactIndexes, declaration NodeID) (SignatureSnapshot, bool) {
	var found SignatureSnapshot
	for _, signature := range indexes.Signatures {
		if signature.Declaration != declaration {
			continue
		}
		if found.ID != 0 {
			return SignatureSnapshot{}, false
		}
		found = signature
	}
	return found, found.ID != 0
}

func bingoTypeMust(id TypeID, types map[TypeID]TypeSnapshot) bingo.TypeKind {
	value, err := bingoType(id, types)
	if err != nil {
		return ""
	}
	return value
}

func isUnaryNumberConstructor(signature SignatureSnapshot, instanceType TypeID, indexes snapshotSemanticFactIndexes) bool {
	return signature.CallingConventionClass == "construct" && signature.ThisParameter == "" && len(signature.Parameters) == 1 && len(signature.ParameterFacts) == 1 && signature.MinArgumentCount == 1 && !signature.HasRest && signature.ReturnType == instanceType && bingoTypeMust(signature.ParameterFacts[0].Type, indexes.Types) == bingo.TypeNumber
}

func isZeroArgumentNumberMethod(signature SignatureSnapshot, indexes snapshotSemanticFactIndexes) bool {
	return signature.CallingConventionClass == "call" && signature.ThisParameter == "" && len(signature.Parameters) == 0 && len(signature.ParameterFacts) == 0 && signature.MinArgumentCount == 0 && !signature.HasRest && bingoTypeMust(signature.ReturnType, indexes.Types) == bingo.TypeNumber && slices.Equal(signature.Effects, []string{"read", "write"})
}

func classPropertyFactsMatch(typ TypeSnapshot, field, method SymbolSnapshot, methodSignature SignatureID, indexes snapshotSemanticFactIndexes) bool {
	if !slices.Equal(typ.Properties, []SymbolID{method.ID, field.ID}) {
		return false
	}
	if len(typ.PropertyFacts) != 2 {
		return false
	}
	methodFact, fieldFact := typ.PropertyFacts[0], typ.PropertyFacts[1]
	methodType, ok := indexes.Types[methodFact.ReadType]
	return methodFact.Symbol == method.ID && methodFact.ReadType == method.Type && methodFact.WriteType == method.Type && methodFact.Optional == false && methodFact.Readonly == false && methodFact.Visibility == "public" && ok && slices.Equal(methodType.CallSignatures, []SignatureID{methodSignature}) &&
		fieldFact.Symbol == field.ID && fieldFact.Optional == false && !fieldFact.Readonly && fieldFact.Visibility == "public" && bingoTypeMust(fieldFact.ReadType, indexes.Types) == bingo.TypeNumber && bingoTypeMust(fieldFact.WriteType, indexes.Types) == bingo.TypeNumber
}

func verifyVERT013aUseSites(snapshot ProgramSnapshot, indexes snapshotSemanticFactIndexes, class NodeSnapshot, classSymbol, fieldSymbol, methodSymbol SymbolID, constructorSignature, methodSignature SignatureID) error {
	newCount, callCount := 0, 0
	for _, node := range snapshot.Nodes {
		if node.Kind == "KindNewExpression" {
			if node.SelectedSignature != constructorSignature || node.NarrowedType != class.NarrowedType || len(node.Children) != 2 {
				return fmt.Errorf("VERT-013a new expression is invalid")
			}
			newCount++
		}
		if node.Kind == "KindCallExpression" && node.SelectedSignature == methodSignature {
			if len(node.Children) != 1 {
				return fmt.Errorf("VERT-013a method call is invalid")
			}
			callee, ok := indexes.Nodes[node.Children[0]]
			if !ok || callee.Kind != "KindPropertyAccessExpression" || callee.Symbol != methodSymbol {
				return fmt.Errorf("VERT-013a method receiver binding is invalid")
			}
			callCount++
		}
	}
	if classSymbol == "" || fieldSymbol == "" || newCount != 1 || callCount != 2 {
		return fmt.Errorf("VERT-013a requires one allocation and two method calls")
	}
	return nil
}
