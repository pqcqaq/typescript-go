package bingo

import (
	"bytes"
	"slices"
	"strings"
	"testing"
)

func TestTypeRelationGraphCanonicalPath(t *testing.T) {
	graph, err := BuildTypeRelationGraph([]TypeRelationNode{{TypeKey: "animal", DeclarationKey: "animal"}, {TypeKey: "dog", DeclarationKey: "dog"}, {TypeKey: "poodle", DeclarationKey: "poodle"}}, []TypeRelationEdge{{SubTypeKey: "poodle", SuperTypeKey: "dog", Path: "Poodle extends Dog"}, {SubTypeKey: "dog", SuperTypeKey: "animal", Path: "Dog extends Animal"}})
	if err != nil {
		t.Fatal(err)
	}
	path, err := FindTypeRelationPath(graph, "poodle", "animal")
	if err != nil || !slices.Equal(path, []string{"poodle", "dog", "animal"}) {
		t.Fatalf("unexpected path %#v: %v", path, err)
	}
	encoded, hash, err := CanonicalTypeRelationGraph(graph)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeTypeRelationGraph(encoded)
	if err != nil {
		t.Fatal(err)
	}
	reencoded, rehash, err := CanonicalTypeRelationGraph(*decoded)
	if err != nil || hash != rehash || !bytes.Equal(encoded, reencoded) {
		t.Fatal("type relation graph did not round trip")
	}
}

func TestTypeRelationGraphRejectsTamper(t *testing.T) {
	graph, err := BuildTypeRelationGraph([]TypeRelationNode{{TypeKey: "a", DeclarationKey: "a"}, {TypeKey: "b", DeclarationKey: "b"}}, []TypeRelationEdge{{SubTypeKey: "b", SuperTypeKey: "a", Path: "b extends a"}})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _, err := CanonicalTypeRelationGraph(graph)
	if err != nil {
		t.Fatal(err)
	}
	for _, data := range [][]byte{[]byte(strings.Replace(string(encoded), `"contentHash":`, `"unknown":true,"contentHash":`, 1)), []byte(strings.Replace(string(encoded), graph.ContentHash, strings.Repeat("0", 64), 1))} {
		if _, err := DecodeTypeRelationGraph(data); err == nil {
			t.Fatal("accepted tampered type relation graph")
		}
	}
	if _, err := FindTypeRelationPath(graph, "a", "b"); err == nil {
		t.Fatal("accepted reversed relation")
	}
}
