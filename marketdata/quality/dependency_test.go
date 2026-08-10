package quality

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestQualityDependencyDirection(t *testing.T) {
	_, current, _, _ := runtime.Caller(0)
	qualityDir := filepath.Dir(current)
	repoRoot := filepath.Clean(filepath.Join(qualityDir, "..", ".."))
	assertReadOnlyPackage(t, qualityDir, "quality")
	forbiddenTradingImports := []string{"marketdata/" + "quality", "quality" + "adapters", "research" + "signal", "provider" + "contract"}
	tradingDir := filepath.Join(repoRoot, "trading")
	if _, err := os.Stat(tradingDir); err == nil {
		scanImports(t, tradingDir, func(path, imported string) {
			for _, forbidden := range forbiddenTradingImports {
				if strings.Contains(imported, forbidden) {
					t.Errorf("trading reverse dependency: %s imports %s", path, imported)
				}
			}
		})
	}
	assertNoWriteChainIdentifier(t, qualityDir, "quality")
}

func TestQualityAdaptersDependencyDirection(t *testing.T) {
	_, current, _, _ := runtime.Caller(0)
	adaptersDir := filepath.Join(filepath.Dir(current), "..", "qualityadapters")
	if _, err := os.Stat(adaptersDir); err != nil {
		t.Fatalf("quality adapters directory unavailable: %v", err)
	}
	assertReadOnlyPackage(t, adaptersDir, "qualityadapters")
	assertNoWriteChainIdentifier(t, adaptersDir, "qualityadapters")
}

func assertReadOnlyPackage(t *testing.T, root, label string) {
	t.Helper()
	forbiddenImports := []string{"data" + "base", "re" + "dis", "tra" + "ding", "refe" + "rence", "exch" + "ange", "led" + "ger", "order" + "book"}
	scanImports(t, root, func(path, imported string) {
		for _, forbidden := range forbiddenImports {
			if strings.Contains(imported, forbidden) {
				t.Errorf("%s import boundary: %s imports %s", label, path, imported)
			}
		}
	})
}

func assertNoWriteChainIdentifier(t *testing.T, root, label string) {
	t.Helper()
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || filepath.Ext(path) != ".go" {
			return err
		}
		file, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			t.Error(parseErr)
			return nil
		}
		ast.Inspect(file, func(node ast.Node) bool {
			if ident, ok := node.(*ast.Ident); ok && ident.Name == "Snapshot"+"Writer" {
				t.Errorf("%s write-chain identifier in %s", label, path)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func scanImports(t *testing.T, root string, check func(string, string)) {
	t.Helper()
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || filepath.Ext(path) != ".go" {
			return nil
		}
		file, parseErr := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if parseErr != nil {
			return parseErr
		}
		for _, spec := range file.Imports {
			value, unquoteErr := strconv.Unquote(spec.Path.Value)
			if unquoteErr != nil {
				return unquoteErr
			}
			check(path, value)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
