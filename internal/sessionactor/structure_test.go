package sessionactor

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestSessionActorMainFileStaysSmall(t *testing.T) {
	data, err := os.ReadFile("actor.go")
	if err != nil {
		t.Fatalf("read actor.go: %v", err)
	}
	lines := strings.Count(string(data), "\n")
	if len(data) > 0 && data[len(data)-1] != '\n' {
		lines++
	}
	if lines > 200 {
		t.Fatalf("actor.go has %d lines, want at most 200", lines)
	}
}

func TestProductionEngineConstructionIsWrappedBySessionHost(t *testing.T) {
	repoRoot := filepath.Clean(filepath.Join("..", ".."))
	err := filepath.WalkDir(repoRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != repoRoot && (strings.HasPrefix(entry.Name(), ".") || entry.Name() == "vendor") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || strings.Contains(path, filepath.Join("internal", "loop")+string(filepath.Separator)) {
			return nil
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		loopAliases := importAliases(file, "paw/internal/loop", "loop")
		hostAliases := importAliases(file, "paw/internal/sessionactor", "sessionactor")
		if len(loopAliases) == 0 {
			return nil
		}
		ast.Inspect(file, func(node ast.Node) bool {
			fn, ok := node.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				return true
			}
			engines := map[string]token.Position{}
			ast.Inspect(fn.Body, func(child ast.Node) bool {
				assign, ok := child.(*ast.AssignStmt)
				if !ok {
					return true
				}
				for i, rhs := range assign.Rhs {
					if i >= len(assign.Lhs) || !isEngineConstructor(rhs, loopAliases) {
						continue
					}
					if ident, ok := assign.Lhs[i].(*ast.Ident); ok {
						engines[ident.Name] = fset.Position(ident.Pos())
					}
				}
				return true
			})
			if len(engines) == 0 {
				return false
			}
			wrapped := map[string]bool{}
			ast.Inspect(fn.Body, func(child ast.Node) bool {
				call, ok := child.(*ast.CallExpr)
				if !ok || !isSelectorCall(call, hostAliases, "NewHost") {
					return true
				}
				for _, arg := range call.Args {
					if ident, ok := arg.(*ast.Ident); ok {
						wrapped[ident.Name] = true
					}
				}
				return true
			})
			for name, position := range engines {
				if !wrapped[name] {
					t.Errorf("%s:%d constructs loop.Engine as %s without wrapping it in sessionactor.NewHost", path, position.Line, name)
				}
			}
			return false
		})
		return nil
	})
	if err != nil {
		t.Fatalf("scan production Go files: %v", err)
	}
}

func importAliases(file *ast.File, importPath, defaultName string) map[string]bool {
	aliases := map[string]bool{}
	for _, imp := range file.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil || path != importPath {
			continue
		}
		name := defaultName
		if imp.Name != nil {
			name = imp.Name.Name
		}
		if name != "_" && name != "." {
			aliases[name] = true
		}
	}
	return aliases
}

func isEngineConstructor(expr ast.Expr, aliases map[string]bool) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return false
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || !strings.HasPrefix(selector.Sel.Name, "NewEngine") {
		return false
	}
	ident, ok := selector.X.(*ast.Ident)
	return ok && aliases[ident.Name]
}

func isSelectorCall(call *ast.CallExpr, aliases map[string]bool, method string) bool {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != method {
		return false
	}
	ident, ok := selector.X.(*ast.Ident)
	return ok && aliases[ident.Name]
}
