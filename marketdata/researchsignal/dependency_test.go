package researchsignal

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestResearchAndTradingDependenciesRemainSeparated(t *testing.T) {
	t.Parallel()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate test source")
	}
	repository := filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", ".."))
	researchDirectories := []string{
		filepath.Join(repository, "marketdata", "researchsignal"),
		filepath.Join(repository, "services", "http", "researchsignals"),
		filepath.Join(repository, "cmd", "research-golden"),
	}
	for _, directory := range researchDirectories {
		walkGoFiles(t, directory, func(path string, imports []string, source []byte) {
			for _, imported := range imports {
				for _, forbidden := range []string{"/trading", "/database", "/redis", "/reference", "/exchange", "/ledger"} {
					if strings.Contains(imported, forbidden) {
						t.Errorf("research boundary %s imports forbidden write-side package %s", path, imported)
					}
				}
			}
			if bytes.Contains(source, []byte("Snapshot"+"Writer")) {
				t.Errorf("research boundary %s references the market snapshot writer", path)
			}
		})
	}
	walkGoFiles(t, filepath.Join(repository, "trading"), func(path string, imports []string, _ []byte) {
		for _, imported := range imports {
			if strings.Contains(imported, "/marketdata/researchsignal") || strings.Contains(imported, "/services/http/researchsignals") {
				t.Errorf("trading package %s imports research package %s", path, imported)
			}
		}
	})
}

func walkGoFiles(t *testing.T, root string, inspect func(string, []string, []byte)) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".go" {
			return nil
		}
		source, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), path, source, parser.ImportsOnly)
		if err != nil {
			return err
		}
		imports := make([]string, 0, len(parsed.Imports))
		for _, declaration := range parsed.Decls {
			importDeclaration, ok := declaration.(*ast.GenDecl)
			if !ok || importDeclaration.Tok != token.IMPORT {
				continue
			}
			for _, specification := range importDeclaration.Specs {
				importSpecification := specification.(*ast.ImportSpec)
				value, err := strconv.Unquote(importSpecification.Path.Value)
				if err != nil {
					return err
				}
				imports = append(imports, value)
			}
		}
		inspect(path, imports, source)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
