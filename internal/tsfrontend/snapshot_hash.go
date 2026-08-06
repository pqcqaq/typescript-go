package tsfrontend

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

const (
	semanticTypePrefix      = "type\x00"
	semanticSignaturePrefix = "signature\x00"
)

type semanticHashNode struct {
	base  string
	edges []semanticHashEdge
}

type semanticHashEdge struct {
	label  string
	target string
}

// canonicalSemanticHashes computes type and signature identities together.
// The temporary capture keys are used only to join graph edges; they never
// enter a descriptor or digest. Strongly connected components are refined
// locally, so recursive types are stable and unrelated graph additions do not
// renumber an existing type's identity.
func canonicalSemanticHashes(
	types map[string]*pendingType,
	signatures map[string]*pendingSignature,
	symbols map[SymbolID]*pendingSymbol,
) (map[string]string, map[string]string) {
	nodes := make(map[string]semanticHashNode, len(types)+len(signatures))
	for key, record := range types {
		nodes[semanticTypePrefix+key] = semanticTypeHashNode(record, symbols)
	}
	for key, record := range signatures {
		nodes[semanticSignaturePrefix+key] = semanticSignatureHashNode(record)
	}

	components, componentByKey := semanticSCCs(nodes)
	hashes := make(map[string]string, len(nodes))
	state := make([]uint8, len(components))
	var hashComponent func(int)
	hashComponent = func(componentIndex int) {
		switch state[componentIndex] {
		case 2:
			return
		case 1:
			panic("semantic hash SCC condensation graph contains a cycle")
		}
		state[componentIndex] = 1
		component := components[componentIndex]
		for _, key := range component {
			for _, edge := range nodes[key].edges {
				dependency, ok := componentByKey[edge.target]
				if ok && dependency != componentIndex {
					hashComponent(dependency)
				}
			}
		}
		colors := refineSemanticComponent(component, componentIndex, nodes, componentByKey, hashes)
		for _, key := range component {
			descriptor := semanticDescriptor(key, componentIndex, nodes, componentByKey, hashes, colors)
			hashes[key] = digestString(descriptor)
		}
		state[componentIndex] = 2
	}
	for componentIndex := range components {
		hashComponent(componentIndex)
	}

	typeHashes := make(map[string]string, len(types))
	for key := range types {
		typeHashes[key] = hashes[semanticTypePrefix+key]
	}
	signatureHashes := make(map[string]string, len(signatures))
	for key := range signatures {
		signatureHashes[key] = hashes[semanticSignaturePrefix+key]
	}
	return typeHashes, signatureHashes
}

func semanticTypeHashNode(record *pendingType, symbols map[SymbolID]*pendingSymbol) semanticHashNode {
	parts := []string{"type", record.scalar, "kind:" + record.kind, "variance:" + record.variance}
	node := semanticHashNode{}
	appendTypeEdges := func(label string, keys []string, unordered bool) {
		for index, key := range keys {
			edgeLabel := label
			if !unordered {
				edgeLabel += ":" + paddedIndex(index)
			}
			node.edges = append(node.edges, semanticHashEdge{label: edgeLabel, target: semanticTypePrefix + key})
		}
	}
	appendTypeEdges("element", record.elementKeys, record.kind == "union" || record.kind == "intersection")
	appendTypeEdges("argument", record.typeArgumentKeys, false)
	appendTypeEdges("base", record.baseTypeKeys, false)
	if record.constraintKey != "" {
		node.edges = append(node.edges, semanticHashEdge{label: "constraint", target: semanticTypePrefix + record.constraintKey})
	}
	if record.defaultKey != "" {
		node.edges = append(node.edges, semanticHashEdge{label: "default", target: semanticTypePrefix + record.defaultKey})
	}
	for index, property := range record.propertyKeys {
		label := "property:" + paddedIndex(index) + ":" + string(property)
		parts = append(parts, label)
		if symbol := symbols[property]; symbol != nil && symbol.typeKey != "" {
			node.edges = append(node.edges, semanticHashEdge{label: label + ":type", target: semanticTypePrefix + symbol.typeKey})
		}
	}
	for index, property := range record.propertyFacts {
		label := "property-fact:" + paddedIndex(index) + ":" + string(property.symbol)
		parts = append(parts,
			label+":optional="+strconv.FormatBool(property.optional),
			label+":readonly="+strconv.FormatBool(property.readonly),
			label+":getter="+strconv.FormatBool(property.hasGetter),
			label+":setter="+strconv.FormatBool(property.hasSetter),
			label+":visibility="+property.visibility,
			label+":private="+property.privateIdentity,
		)
		if property.readKey != "" {
			node.edges = append(node.edges, semanticHashEdge{label: label + ":read", target: semanticTypePrefix + property.readKey})
		}
		if property.writeKey != "" {
			node.edges = append(node.edges, semanticHashEdge{label: label + ":write", target: semanticTypePrefix + property.writeKey})
		}
	}
	for index, key := range record.callSignatureKeys {
		node.edges = append(node.edges, semanticHashEdge{label: "call:" + paddedIndex(index), target: semanticSignaturePrefix + key})
	}
	for index, key := range record.constructSignatureKeys {
		node.edges = append(node.edges, semanticHashEdge{label: "construct:" + paddedIndex(index), target: semanticSignaturePrefix + key})
	}
	for index, info := range record.indexInfos {
		prefix := "index:" + paddedIndex(index) + ":" + strconv.FormatBool(info.readonly) + ":" + string(info.declaration)
		node.edges = append(node.edges,
			semanticHashEdge{label: prefix + ":key", target: semanticTypePrefix + info.keyType},
			semanticHashEdge{label: prefix + ":value", target: semanticTypePrefix + info.valueType},
		)
	}
	slices.Sort(parts)
	node.base = strings.Join(parts, "\x1f")
	return node
}

func semanticSignatureHashNode(record *pendingSignature) semanticHashNode {
	parts := []string{
		"signature",
		"flags:" + strconv.FormatUint(uint64(record.flags), 10),
		"declaration:" + string(record.declaration),
		"this:" + string(record.thisParameter),
		"minimum:" + strconv.Itoa(record.minArgumentCount),
		"rest:" + strconv.FormatBool(record.hasRest),
		"predicate-kind:" + strconv.FormatInt(int64(record.predicate.Kind), 10),
		"predicate-index:" + strconv.FormatInt(int64(record.predicate.ParameterIndex), 10),
		"predicate-name:" + record.predicate.ParameterName,
		"predicate-present:" + strconv.FormatBool(record.predicate.Present),
		"convention:" + record.callingConventionClass,
	}
	for index, parameter := range record.parameters {
		parts = append(parts, "parameter:"+paddedIndex(index)+":"+string(parameter))
	}
	for index, parameter := range record.parameterFacts {
		parts = append(parts,
			"parameter-fact:"+paddedIndex(index)+":"+string(parameter.symbol),
			"parameter-fact:"+paddedIndex(index)+":optional="+strconv.FormatBool(parameter.optional),
			"parameter-fact:"+paddedIndex(index)+":rest="+strconv.FormatBool(parameter.rest),
		)
	}
	for _, effect := range record.effects {
		parts = append(parts, "effect:"+effect)
	}
	node := semanticHashNode{base: strings.Join(parts, "\x1f")}
	appendTypeEdges := func(label string, keys []string) {
		for index, key := range keys {
			node.edges = append(node.edges, semanticHashEdge{label: label + ":" + paddedIndex(index), target: semanticTypePrefix + key})
		}
	}
	appendTypeEdges("parameter-type", record.parameterTypeKeys)
	appendTypeEdges("type-parameter", record.typeParameterKeys)
	appendTypeEdges("instantiated-argument", record.instantiatedTypeKeys)
	if record.returnTypeKey != "" {
		node.edges = append(node.edges, semanticHashEdge{label: "return", target: semanticTypePrefix + record.returnTypeKey})
	}
	if record.predicateTypeKey != "" {
		node.edges = append(node.edges, semanticHashEdge{label: "predicate-type", target: semanticTypePrefix + record.predicateTypeKey})
	}
	return node
}

func semanticSCCs(nodes map[string]semanticHashNode) ([][]string, map[string]int) {
	keys := make([]string, 0, len(nodes))
	for key := range nodes {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	index := 0
	indices := make(map[string]int, len(nodes))
	lowlink := make(map[string]int, len(nodes))
	onStack := make(map[string]bool, len(nodes))
	stack := make([]string, 0, len(nodes))
	components := make([][]string, 0)
	var visit func(string)
	visit = func(key string) {
		indices[key] = index
		lowlink[key] = index
		index++
		stack = append(stack, key)
		onStack[key] = true
		targets := make([]string, 0, len(nodes[key].edges))
		for _, edge := range nodes[key].edges {
			if _, ok := nodes[edge.target]; ok {
				targets = append(targets, edge.target)
			}
		}
		slices.Sort(targets)
		for _, target := range targets {
			if _, seen := indices[target]; !seen {
				visit(target)
				lowlink[key] = min(lowlink[key], lowlink[target])
			} else if onStack[target] {
				lowlink[key] = min(lowlink[key], indices[target])
			}
		}
		if lowlink[key] != indices[key] {
			return
		}
		component := make([]string, 0)
		for {
			last := len(stack) - 1
			target := stack[last]
			stack = stack[:last]
			onStack[target] = false
			component = append(component, target)
			if target == key {
				break
			}
		}
		slices.Sort(component)
		components = append(components, component)
	}
	for _, key := range keys {
		if _, seen := indices[key]; !seen {
			visit(key)
		}
	}
	componentByKey := make(map[string]int, len(nodes))
	for componentIndex, component := range components {
		for _, key := range component {
			componentByKey[key] = componentIndex
		}
	}
	return components, componentByKey
}

func refineSemanticComponent(
	component []string,
	componentIndex int,
	nodes map[string]semanticHashNode,
	componentByKey map[string]int,
	hashes map[string]string,
) map[string]string {
	colors := make(map[string]string, len(component))
	for _, key := range component {
		colors[key] = "recursive"
	}
	for round := 0; round < len(component)+2; round++ {
		descriptors := make(map[string]string, len(component))
		for _, key := range component {
			descriptors[key] = semanticDescriptor(key, componentIndex, nodes, componentByKey, hashes, colors)
		}
		next := canonicalLocalColors(component, descriptors)
		if equalStringMaps(colors, next) {
			return next
		}
		colors = next
	}
	return colors
}

func semanticDescriptor(
	key string,
	componentIndex int,
	nodes map[string]semanticHashNode,
	componentByKey map[string]int,
	hashes map[string]string,
	colors map[string]string,
) string {
	node := nodes[key]
	parts := []string{node.base}
	for _, edge := range node.edges {
		target := "missing"
		if targetComponent, ok := componentByKey[edge.target]; ok {
			if targetComponent == componentIndex {
				target = "recursive:" + colors[edge.target]
			} else {
				target = "hash:" + hashes[edge.target]
			}
		}
		parts = append(parts, "edge:"+edge.label+":"+target)
	}
	slices.Sort(parts[1:])
	return strings.Join(parts, "\x1f")
}

func canonicalLocalColors(keys []string, descriptors map[string]string) map[string]string {
	unique := make([]string, 0, len(descriptors))
	seen := make(map[string]struct{}, len(descriptors))
	for _, key := range keys {
		descriptor := descriptors[key]
		if _, ok := seen[descriptor]; !ok {
			seen[descriptor] = struct{}{}
			unique = append(unique, descriptor)
		}
	}
	slices.Sort(unique)
	rank := make(map[string]string, len(unique))
	for index, descriptor := range unique {
		rank[descriptor] = fmt.Sprintf("%08d", index)
	}
	result := make(map[string]string, len(keys))
	for _, key := range keys {
		result[key] = rank[descriptors[key]]
	}
	return result
}

func equalStringMaps(left, right map[string]string) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		if right[key] != value {
			return false
		}
	}
	return true
}

func paddedIndex(index int) string {
	return fmt.Sprintf("%08d", index)
}
