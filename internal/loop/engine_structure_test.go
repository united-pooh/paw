package loop

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEngineOwnsSmallIndependentExecutionState(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "runner.go", nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse runner.go: %v", err)
	}
	var engineFields int
	var foundEngine, foundRunner bool
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}
		for _, spec := range gen.Specs {
			typeSpec := spec.(*ast.TypeSpec)
			switch typeSpec.Name.Name {
			case "Engine":
				foundEngine = true
				if structType, ok := typeSpec.Type.(*ast.StructType); ok {
					engineFields = len(structType.Fields.List)
				}
			case "Runner":
				foundRunner = true
			}
		}
	}
	if !foundEngine {
		t.Fatal("loop.Engine type is missing")
	}
	if foundRunner {
		t.Fatal("legacy loop.Runner type must be removed")
	}
	if engineFields > 15 {
		t.Fatalf("Engine has %d top-level fields, want at most 15", engineFields)
	}
}

func TestLoopProductionFilesDoNotImportActorRuntime(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read loop package: %v", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), filepath.Clean(name), nil, parser.ImportsOnly|parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse %s imports: %v", name, err)
		}
		for _, imp := range file.Imports {
			if imp.Path.Value == `"paw/internal/actor"` {
				t.Fatalf("%s imports internal/actor; loop.Engine must remain actor-independent", name)
			}
		}
	}
}
