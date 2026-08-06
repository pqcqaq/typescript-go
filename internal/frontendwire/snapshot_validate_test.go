package frontendwire

import (
	"strings"
	"testing"
)

func TestValidateSnapshotLogicalPathsRejectsRootedDiskPaths(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(*ProgramSnapshot)
		field  string
	}{
		{
			name: "Windows drive baseUrl",
			mutate: func(snapshot *ProgramSnapshot) {
				snapshot.Config.TypeScript.BaseURL = "D:/machine/project"
			},
			field: "TypeScript option baseUrl",
		},
		{
			name: "UNC rootDir",
			mutate: func(snapshot *ProgramSnapshot) {
				snapshot.Config.TypeScript.RootDirs = []string{"//server/share/generated"}
			},
			field: "TypeScript option rootDirs[0]",
		},
		{
			name: "POSIX typeRoot",
			mutate: func(snapshot *ProgramSnapshot) {
				snapshot.Config.TypeScript.TypeRoots = []string{"/opt/project/types"}
			},
			field: "TypeScript option typeRoots[0]",
		},
		{
			name: "Windows paths substitution",
			mutate: func(snapshot *ProgramSnapshot) {
				snapshot.Config.TypeScript.Paths = []TypeScriptPathMapping{{
					Pattern:       "@app/*",
					Substitutions: []string{"E:\\machine\\src\\*"},
				}}
			},
			field: "TypeScript option paths[0].substitutions[0]",
		},
		{
			name: "Windows drive project root",
			mutate: func(snapshot *ProgramSnapshot) {
				snapshot.Config.CanonicalProjectRoot = "C:/machine/project"
			},
			field: "canonical project root",
		},
		{
			name: "UNC config path",
			mutate: func(snapshot *ProgramSnapshot) {
				snapshot.Config.CanonicalConfigPath = "//server/share/tsconfig.json"
			},
			field: "canonical config path",
		},
		{
			name: "Windows drive file path",
			mutate: func(snapshot *ProgramSnapshot) {
				snapshot.Files = []FileSnapshot{{ID: "drive", CanonicalPath: "C:/machine/main.ts"}}
			},
			field: `file "drive" canonical path`,
		},
		{
			name: "UNC file path",
			mutate: func(snapshot *ProgramSnapshot) {
				snapshot.Files = []FileSnapshot{{ID: "unc", CanonicalPath: "//server/share/main.ts"}}
			},
			field: `file "unc" canonical path`,
		},
		{
			name: "POSIX file path",
			mutate: func(snapshot *ProgramSnapshot) {
				snapshot.Files = []FileSnapshot{{ID: "posix", CanonicalPath: "/machine/main.ts"}}
			},
			field: `file "posix" canonical path`,
		},
		{
			name: "Windows drive module path",
			mutate: func(snapshot *ProgramSnapshot) {
				snapshot.Modules = []ModuleSnapshot{{ID: "drive", CanonicalPath: "D:/machine/module.ts"}}
			},
			field: `module "drive" canonical path`,
		},
		{
			name: "UNC module path",
			mutate: func(snapshot *ProgramSnapshot) {
				snapshot.Modules = []ModuleSnapshot{{ID: "unc", CanonicalPath: `\\server\share\module.ts`}}
			},
			field: `module "unc" canonical path`,
		},
		{
			name: "POSIX module path",
			mutate: func(snapshot *ProgramSnapshot) {
				snapshot.Modules = []ModuleSnapshot{{ID: "posix", CanonicalPath: "/machine/module.ts"}}
			},
			field: `module "posix" canonical path`,
		},
		{
			name: "Windows drive diagnostic primary span",
			mutate: func(snapshot *ProgramSnapshot) {
				snapshot.Diagnostics = []Diagnostic{{PrimarySpan: SourceSpan{File: "C:/machine/tsconfig.json"}}}
			},
			field: "diagnostic[0] primary span file",
		},
		{
			name: "UNC diagnostic related span",
			mutate: func(snapshot *ProgramSnapshot) {
				snapshot.Diagnostics = []Diagnostic{{RelatedSpans: []RelatedSpan{{Span: SourceSpan{File: "//server/share/main.ts"}}}}}
			},
			field: "diagnostic[0] relatedSpans[0] file",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var snapshot ProgramSnapshot
			test.mutate(&snapshot)
			err := validateSnapshotLogicalPaths(snapshot)
			if err == nil || !strings.Contains(err.Error(), test.field) || !strings.Contains(err.Error(), "rooted disk path") {
				t.Fatalf("rooted snapshot path validation error = %v", err)
			}
		})
	}
}

func TestValidateSnapshotLogicalPathsAcceptsProjectRelativePaths(t *testing.T) {
	t.Parallel()
	snapshot := ProgramSnapshot{
		Config: ConfigSnapshot{
			CanonicalProjectRoot: ".",
			CanonicalConfigPath:  "../config/tsconfig.json",
			TypeScript: TypeScriptOptions{
				BaseURL:  ".",
				RootDirs: []string{"src", "../generated"},
				TypeRoots: []string{
					"types",
					"../shared/types",
				},
				Paths: []TypeScriptPathMapping{{
					Pattern:       "/absolute-looking-module-specifier/*",
					Substitutions: []string{"src/*", "../generated/*"},
				}},
			},
		},
		Files:   []FileSnapshot{{ID: "file", CanonicalPath: "../shared/main.ts"}},
		Modules: []ModuleSnapshot{{ID: "module", CanonicalPath: "@stdlib/lib.es2025.d.ts"}},
		Diagnostics: []Diagnostic{{
			PrimarySpan: SourceSpan{File: "src/main.ts"},
			RelatedSpans: []RelatedSpan{{
				Span: SourceSpan{File: "../shared/types.d.ts"},
			}},
		}},
	}
	if err := validateSnapshotLogicalPaths(snapshot); err != nil {
		t.Fatalf("project-relative snapshot path validation error = %v", err)
	}
}
