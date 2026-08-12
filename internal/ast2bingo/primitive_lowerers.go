package ast2bingo

import (
	"fmt"

	"github.com/microsoft/typescript-go/internal/bingo"
)

// primitiveFunctionLoweringInput groups the read-only state assembled while
// replaying one function. Lowerers must not mutate the snapshot indexes or
// the partially constructed function shared by callers.
type primitiveFunctionLoweringInput struct {
	bodyID          NodeID
	function        bingo.HIRFunction
	events          []LoweringEvent
	parameterValues map[SymbolID]bingo.ValueID
	parameterTypes  map[bingo.ValueID]bingo.TypeKind
	functionNode    NodeSnapshot
	nodes           map[NodeID]NodeSnapshot
	types           map[TypeID]TypeSnapshot
	symbols         map[SymbolID]SymbolSnapshot
	signatures      map[SignatureID]SignatureSnapshot
	functionIDs     map[NodeID]bingo.FunctionID
}

type primitiveFunctionLowerer struct {
	name  string
	lower func(primitiveFunctionLoweringInput) (bingo.HIRFunction, []LoweringEvent, bool, error)
}

var primitiveFunctionLowerers = [...]primitiveFunctionLowerer{
	{name: "string-length", lower: lowerPrimitiveStringLength},
	{name: "local-call", lower: lowerPrimitiveLocalCall},
	{name: "classify", lower: lowerPrimitiveClassify},
	{name: "loop", lower: lowerPrimitiveLoop},
	{name: "coalesce-assign", lower: lowerPrimitiveCoalesceAssign},
	{name: "coalesce", lower: lowerPrimitiveCoalesce},
	{name: "choose", lower: lowerPrimitiveChoose},
}

func lowerPrimitiveFunction(input primitiveFunctionLoweringInput, lowerers []primitiveFunctionLowerer) (bingo.HIRFunction, []LoweringEvent, bool, error) {
	var function bingo.HIRFunction
	var events []LoweringEvent
	matchedName := ""
	for _, lowerer := range lowerers {
		lowered, loweredEvents, matched, err := lowerer.lower(input)
		if err != nil {
			return bingo.HIRFunction{}, nil, false, fmt.Errorf("%s lowering: %w", lowerer.name, err)
		}
		if !matched {
			continue
		}
		if matchedName != "" {
			return bingo.HIRFunction{}, nil, false, fmt.Errorf("primitive function matches multiple lowerers: %s and %s", matchedName, lowerer.name)
		}
		function, events, matchedName = lowered, loweredEvents, lowerer.name
	}
	if matchedName == "" {
		return bingo.HIRFunction{}, nil, false, nil
	}
	return function, events, true, nil
}

func lowerPrimitiveStringLength(ctx primitiveFunctionLoweringInput) (bingo.HIRFunction, []LoweringEvent, bool, error) {
	source, matched, err := findPrimitiveStringLength(ctx.bodyID, ctx.nodes)
	if err != nil || !matched {
		return bingo.HIRFunction{}, nil, matched, err
	}
	function, events, err := replayStringLengthFunction(ctx.function, ctx.events, source, ctx.parameterValues, ctx.parameterTypes, ctx.functionNode, ctx.nodes, ctx.types, ctx.symbols, ctx.signatures)
	return function, events, true, err
}

func lowerPrimitiveLocalCall(ctx primitiveFunctionLoweringInput) (bingo.HIRFunction, []LoweringEvent, bool, error) {
	source, matched := findPrimitiveLocalCall(ctx.bodyID, ctx.nodes)
	if !matched {
		return bingo.HIRFunction{}, nil, false, nil
	}
	function, events, err := replayLocalCallFunction(ctx.function, ctx.events, source, ctx.parameterValues, ctx.parameterTypes, ctx.functionNode, ctx.nodes, ctx.types, ctx.symbols, ctx.signatures, ctx.functionIDs)
	return function, events, true, err
}

func lowerPrimitiveClassify(ctx primitiveFunctionLoweringInput) (bingo.HIRFunction, []LoweringEvent, bool, error) {
	source, matched, err := findPrimitiveClassify(ctx.bodyID, ctx.nodes)
	if err != nil || !matched {
		return bingo.HIRFunction{}, nil, matched, err
	}
	function, events, err := replayClassifyFunction(ctx.function, ctx.events, source, ctx.parameterValues, ctx.parameterTypes, ctx.functionNode, ctx.nodes, ctx.types, ctx.symbols, ctx.signatures)
	return function, events, true, err
}

func lowerPrimitiveLoop(ctx primitiveFunctionLoweringInput) (bingo.HIRFunction, []LoweringEvent, bool, error) {
	source, matched, err := findPrimitiveLoop(ctx.bodyID, ctx.nodes)
	if err != nil || !matched {
		return bingo.HIRFunction{}, nil, matched, err
	}
	function, events, err := replayLoopFunction(ctx.function, ctx.events, source, ctx.parameterValues, ctx.parameterTypes, ctx.functionNode, ctx.nodes, ctx.types, ctx.symbols, ctx.signatures)
	return function, events, true, err
}

func lowerPrimitiveCoalesceAssign(ctx primitiveFunctionLoweringInput) (bingo.HIRFunction, []LoweringEvent, bool, error) {
	source, matched, err := findPrimitiveCoalesceAssign(ctx.bodyID, ctx.nodes)
	if err != nil || !matched {
		return bingo.HIRFunction{}, nil, matched, err
	}
	function, events, err := replayCoalesceAssignFunction(ctx.function, ctx.events, source, ctx.parameterValues, ctx.parameterTypes, ctx.functionNode, ctx.nodes, ctx.types, ctx.symbols, ctx.signatures)
	return function, events, true, err
}

func lowerPrimitiveCoalesce(ctx primitiveFunctionLoweringInput) (bingo.HIRFunction, []LoweringEvent, bool, error) {
	source, matched, err := findPrimitiveCoalesce(ctx.bodyID, ctx.nodes)
	if err != nil || !matched {
		return bingo.HIRFunction{}, nil, matched, err
	}
	function, events, err := replayCoalesceFunction(ctx.function, ctx.events, source, ctx.parameterValues, ctx.parameterTypes, ctx.functionNode, ctx.nodes, ctx.types, ctx.symbols, ctx.signatures)
	return function, events, true, err
}

func lowerPrimitiveChoose(ctx primitiveFunctionLoweringInput) (bingo.HIRFunction, []LoweringEvent, bool, error) {
	source, matched, err := findPrimitiveChoose(ctx.bodyID, ctx.nodes)
	if err != nil || !matched {
		return bingo.HIRFunction{}, nil, matched, err
	}
	function, events, err := replayChooseFunction(ctx.function, ctx.events, source, ctx.parameterValues, ctx.parameterTypes, ctx.functionNode, ctx.nodes, ctx.types, ctx.symbols, ctx.signatures)
	return function, events, true, err
}
