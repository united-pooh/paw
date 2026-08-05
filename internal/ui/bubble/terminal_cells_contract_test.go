package bubble

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// TestTerminalCellGeometryContract keeps terminal_cells.go as the only place
// that may call third-party screen-width primitives. Other files may configure
// lipgloss.Style.Width, but all measurement and slicing must go through the
// styled-cell helpers in terminal_cells.go.
func TestTerminalCellGeometryContract(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package directory: %v", err)
	}

	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || filepath.Ext(name) != ".go" || name == "terminal_cells.go" {
			continue
		}
		if len(name) >= len("_test.go") && name[len(name)-len("_test.go"):] == "_test.go" {
			continue
		}

		fileSet := token.NewFileSet()
		file, err := parser.ParseFile(fileSet, name, nil, 0)
		if err != nil {
			t.Errorf("parse %s: %v", name, err)
			continue
		}
		aliases := geometryImportAliases(file)

		ast.Inspect(file, func(node ast.Node) bool {
			switch value := node.(type) {
			case *ast.CallExpr:
				selector, ok := value.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				identifier, ok := selector.X.(*ast.Ident)
				if !ok {
					return true
				}
				if forbiddenGeometryCall(aliases[identifier.Name], selector.Sel.Name) {
					position := fileSet.Position(value.Pos())
					t.Errorf("%s:%d calls %s.%s; route visual geometry through terminal_cells.go", name, position.Line, identifier.Name, selector.Sel.Name)
				}
			case *ast.FuncDecl:
				if forbiddenLocalGeometryHelper(value.Name.Name) {
					position := fileSet.Position(value.Pos())
					t.Errorf("%s:%d declares retired geometry helper %s; use terminal_cells.go", name, position.Line, value.Name.Name)
				}
			}
			return true
		})
	}
}

func geometryImportAliases(file *ast.File) map[string]string {
	aliases := make(map[string]string)
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			continue
		}
		alias := filepath.Base(path)
		if spec.Name != nil {
			alias = spec.Name.Name
		}
		aliases[alias] = path
	}
	return aliases
}

func forbiddenGeometryCall(importPath, name string) bool {
	switch importPath {
	case "github.com/charmbracelet/lipgloss":
		return name == "Width"
	case "github.com/charmbracelet/x/ansi":
		return name == "StringWidth" || name == "Truncate" || name == "TruncateLeft"
	case "github.com/mattn/go-runewidth":
		return true
	default:
		return false
	}
}

func forbiddenLocalGeometryHelper(name string) bool {
	switch name {
	case "truncateDisplayWidth",
		"wrapDisplayWidthLine",
		"wrapDisplayWidthLines",
		"padDisplayWidth",
		"truncateStyledDisplayWidth",
		"trimVisibleWidth",
		"padOrTruncateToWidth":
		return true
	default:
		return false
	}
}
