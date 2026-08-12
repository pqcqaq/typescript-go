package tsfrontend

import (
	"context"
	"slices"
	"testing"
)

func TestCheckClassAccessDiagnostics(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		source string
		code string
	}{
		{
			name: "derived private access",
			source: `class Vault { private secret = 1; }
class DerivedVault extends Vault { read(): number { return this.secret; } }`,
			code: "TS2341",
		},
		{
			name: "protected base receiver",
			source: `class Vault { protected value = 1; }
class DerivedVault extends Vault { read(other: Vault): number { return other.value; } }`,
			code: "TS2446",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := Check(context.Background(), testBuildRequest(map[string]string{
				"/project/tsconfig.json": `{"compilerOptions":{"strict":true,"noEmit":true},"files":["main.ts"]}`,
				"/project/main.ts":       test.source,
			}))
			if err != nil {
				t.Fatal(err)
			}
			if !DiagnosticsHaveErrors(result.Diagnostics) || !slices.ContainsFunc(result.Diagnostics, func(diagnostic Diagnostic) bool { return diagnostic.Code == test.code }) {
				t.Fatalf("diagnostics = %#v, want %s", result.Diagnostics, test.code)
			}
		})
	}
}
