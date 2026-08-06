package tsfrontend

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
)

type kindCoverageDocument struct {
	SchemaVersion int                     `json:"schemaVersion"`
	Evidence      []kindCoverageEvidence  `json:"evidence"`
	Exemptions    []kindCoverageExemption `json:"exemptions"`
}

type kindCoverageEvidence struct {
	Kind   string `json:"kind"`
	CaseID string `json:"caseId"`
	Expect string `json:"expect"`
}

type kindCoverageExemption struct {
	Kind   string                      `json:"kind"`
	Reason kindCoverageExemptionReason `json:"reason"`
}

type kindCoverageExemptionReason string

const (
	kindCoverageExemptionToken       kindCoverageExemptionReason = "token"
	kindCoverageExemptionTrivia      kindCoverageExemptionReason = "trivia"
	kindCoverageExemptionParserOwned kindCoverageExemptionReason = "parser-owned"
	kindCoverageExemptionSynthetic   kindCoverageExemptionReason = "synthetic"
)

func TestKindCoverageUsesRealFixtures(t *testing.T) {
	manifest := loadFrontendFixtureJSON[frontendFixtureManifest](t, filepath.Join(frontendFixtureRoot, "manifest.json"))
	fixtures := validateFrontendFixtureManifest(t, manifest)
	byID := make(map[string]frontendFixtureCase, len(fixtures))
	for _, fixture := range fixtures {
		byID[fixture.ID] = fixture
	}
	coverage := loadFrontendFixtureJSON[kindCoverageDocument](t, filepath.Join(frontendFixtureRoot, "kind-coverage.json"))
	if coverage.SchemaVersion != 2 {
		t.Fatalf("kind coverage schema = %d, want 2", coverage.SchemaVersion)
	}
	kindManifest, err := LoadKindManifest()
	if err != nil {
		t.Fatal(err)
	}
	byKind := make(map[string]KindManifestEntry, len(kindManifest.Kinds))
	for _, entry := range kindManifest.Kinds {
		byKind[entry.Kind] = entry
	}
	wantExemptions := make(map[kindCoverageExemptionReason]int)
	for _, entry := range kindManifest.Kinds {
		if reason, exempt := expectedKindCoverageExemption(entry); exempt {
			wantExemptions[reason]++
		}
	}
	if len(coverage.Evidence) != len(kindManifest.Kinds)-157 || len(coverage.Exemptions) != 157 {
		t.Fatalf("Kind coverage has %d evidence and %d exemptions, want %d and 157", len(coverage.Evidence), len(coverage.Exemptions), len(kindManifest.Kinds)-157)
	}

	seen := make(map[string]string, len(kindManifest.Kinds))
	levelCoverage := make(map[KindSupportLevel]struct{})
	expectCoverage := make(map[string]struct{})
	observed := make(map[string]frontendFixtureObservation, len(coverage.Evidence))
	for index, evidence := range coverage.Evidence {
		if index > 0 && coverage.Evidence[index-1].Kind >= evidence.Kind {
			t.Fatalf("Kind coverage evidence is not Kind-sorted and unique at %q", evidence.Kind)
		}
		if previous, duplicate := seen[evidence.Kind]; duplicate {
			t.Fatalf("Kind coverage %q appears as both %s and evidence", evidence.Kind, previous)
		}
		seen[evidence.Kind] = "evidence"
		entry, ok := byKind[evidence.Kind]
		if !ok {
			t.Fatalf("Kind coverage names unknown Kind %q", evidence.Kind)
		}
		if reason, exempt := expectedKindCoverageExemption(entry); exempt {
			t.Fatalf("Kind coverage %s must use the %q exemption", evidence.Kind, reason)
		}
		fixture, ok := byID[evidence.CaseID]
		if !ok {
			t.Fatalf("Kind coverage names unknown fixture ID %q", evidence.CaseID)
		}
		if evidence.Expect != "accept" && evidence.Expect != "reject" && evidence.Expect != "compile-only" {
			t.Fatalf("Kind coverage %s has unknown expectation %q", evidence.Kind, evidence.Expect)
		}
		level := entry.PlannedLevels[0]
		switch level {
		case KindSupportCompileOnly:
			if evidence.Expect != "compile-only" {
				t.Fatalf("Kind coverage %s level C has expectation %q, want compile-only", evidence.Kind, evidence.Expect)
			}
		case KindSupportS0:
			if evidence.Expect != "accept" {
				t.Fatalf("Kind coverage %s level S0 has expectation %q, want accept", evidence.Kind, evidence.Expect)
			}
		case KindSupportS2, KindSupportPlanned, KindSupportReject:
			if evidence.Expect != "reject" {
				t.Fatalf("Kind coverage %s level %s has expectation %q, want reject", evidence.Kind, level, evidence.Expect)
			}
		case KindSupportS1:
			if evidence.Expect != "accept" && evidence.Expect != "reject" {
				t.Fatalf("Kind coverage %s level S1 has expectation %q", evidence.Kind, evidence.Expect)
			}
		}
		observation, ok := observed[evidence.CaseID]
		if !ok {
			observation = runFrontendFixture(t, fixture)
			observed[evidence.CaseID] = observation
		}
		if observation.snapshot == nil {
			t.Fatalf("Kind coverage %s/%s did not produce a snapshot", evidence.Kind, evidence.CaseID)
		}
		if !slices.ContainsFunc(observation.snapshot.Nodes, func(node NodeSnapshot) bool {
			return node.Kind == evidence.Kind && node.KindValue == entry.KindValue
		}) {
			t.Fatalf("fixture %s snapshot does not contain %s", evidence.CaseID, evidence.Kind)
		}
		levelCoverage[level] = struct{}{}
		expectCoverage[evidence.Expect] = struct{}{}
		switch evidence.Expect {
		case "accept":
			if fixture.Mode != "accept" || DiagnosticsHaveErrors(observation.diagnostics) {
				t.Fatalf("fixture %s is not an accepted, error-free observation", evidence.CaseID)
			}
		case "compile-only":
			if entry.PlannedLevels[0] != KindSupportCompileOnly || fixture.Mode != "accept" || DiagnosticsHaveErrors(observation.diagnostics) {
				t.Fatalf("fixture %s is not a compile-only, error-free C observation", evidence.CaseID)
			}
		case "reject":
			if fixture.Mode != "reject" || !slices.ContainsFunc(observation.diagnostics, func(diagnostic Diagnostic) bool {
				return diagnostic.NodeKind == evidence.Kind && diagnostic.Severity == DiagnosticSeverityError
			}) {
				t.Fatalf("fixture %s has no error diagnostic tied to %s", evidence.CaseID, evidence.Kind)
			}
		}
	}

	gotExemptions := make(map[kindCoverageExemptionReason]int)
	for index, exemption := range coverage.Exemptions {
		if index > 0 && coverage.Exemptions[index-1].Kind >= exemption.Kind {
			t.Fatalf("Kind coverage exemptions are not Kind-sorted and unique at %q", exemption.Kind)
		}
		if previous, duplicate := seen[exemption.Kind]; duplicate {
			t.Fatalf("Kind coverage %q appears as both %s and exemption", exemption.Kind, previous)
		}
		seen[exemption.Kind] = "exemption"
		entry, ok := byKind[exemption.Kind]
		if !ok {
			t.Fatalf("Kind coverage exemption names unknown Kind %q", exemption.Kind)
		}
		wantReason, exempt := expectedKindCoverageExemption(entry)
		if !exempt {
			t.Fatalf("source Kind %s cannot use a coverage exemption", exemption.Kind)
		}
		if exemption.Reason != wantReason {
			t.Fatalf("Kind coverage exemption %s has reason %q, want %q", exemption.Kind, exemption.Reason, wantReason)
		}
		gotExemptions[exemption.Reason]++
	}
	for _, reason := range []kindCoverageExemptionReason{
		kindCoverageExemptionToken,
		kindCoverageExemptionTrivia,
		kindCoverageExemptionParserOwned,
		kindCoverageExemptionSynthetic,
	} {
		if gotExemptions[reason] != wantExemptions[reason] {
			t.Errorf("Kind coverage has %d %q exemptions, want %d", gotExemptions[reason], reason, wantExemptions[reason])
		}
	}
	for _, entry := range kindManifest.Kinds {
		if _, covered := seen[entry.Kind]; !covered {
			t.Errorf("Kind coverage has neither evidence nor exemption for %s", entry.Kind)
		}
	}
	for _, expect := range []string{"accept", "reject", "compile-only"} {
		if _, ok := expectCoverage[expect]; !ok {
			t.Errorf("Kind coverage has no %s evidence", expect)
		}
	}
	for _, level := range []KindSupportLevel{KindSupportS0, KindSupportS1, KindSupportS2, KindSupportCompileOnly, KindSupportPlanned, KindSupportReject} {
		if _, ok := levelCoverage[level]; !ok {
			t.Errorf("Kind coverage has no %s evidence", level)
		}
	}
}

func expectedKindCoverageExemption(entry KindManifestEntry) (kindCoverageExemptionReason, bool) {
	kind := ast.Kind(entry.KindValue)
	switch kind {
	case ast.KindSingleLineCommentTrivia, ast.KindMultiLineCommentTrivia, ast.KindNewLineTrivia, ast.KindWhitespaceTrivia:
		return kindCoverageExemptionTrivia, true
	case ast.KindUnknown, ast.KindEndOfFile, ast.KindConflictMarkerTrivia, ast.KindNonTextFileMarkerTrivia, ast.KindMissingDeclaration:
		return kindCoverageExemptionParserOwned, true
	case ast.KindSyntheticExpression, ast.KindSyntaxList, ast.KindNotEmittedStatement, ast.KindPartiallyEmittedExpression,
		ast.KindSyntheticReferenceExpression, ast.KindNotEmittedTypeElement:
		return kindCoverageExemptionSynthetic, true
	case ast.KindIdentifier, ast.KindPrivateIdentifier, ast.KindNullKeyword, ast.KindTrueKeyword, ast.KindFalseKeyword,
		ast.KindThisKeyword, ast.KindSuperKeyword:
		return "", false
	default:
		if kind >= ast.KindOpenBraceToken && kind <= ast.KindDeferKeyword {
			return kindCoverageExemptionToken, true
		}
		return "", false
	}
}
