package kernel_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

const modulePath = "github.com/freesoulcode/foya"

func TestPackageBoundaries(t *testing.T) {
	root := repositoryRoot(t)
	legacy := []string{
		"agentdef", "approval", "backend", "command", "compaction",
		"credential", "event", "imagegen", "message", "prompt",
		"protocol", "provider", "question", "queue", "session",
		"state", "title", "videogen",
	}
	for _, name := range legacy {
		if _, err := os.Stat(filepath.Join(root, "internal", name)); !os.IsNotExist(err) {
			t.Errorf("legacy package internal/%s must not be reintroduced", name)
		}
	}

	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "node_modules", "target":
				return filepath.SkipDir
			default:
				return nil
			}
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}
		importer := packagePath(root, filepath.Dir(path))
		for _, imported := range fileImports(t, path) {
			for _, name := range legacy {
				if imported == modulePath+"/internal/"+name ||
					strings.HasPrefix(imported, modulePath+"/internal/"+name+"/") {
					t.Errorf("%s imports removed package %s", relative(root, path), imported)
				}
			}
			if strings.HasSuffix(path, "_test.go") {
				continue
			}
			assertDependencyDirection(t, relative(root, path), importer, imported)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func assertDependencyDirection(t *testing.T, file, importer, imported string) {
	t.Helper()
	internalRoot := modulePath + "/internal/"
	if !strings.HasPrefix(imported, internalRoot) {
		return
	}
	importedName := strings.TrimPrefix(imported, internalRoot)

	if (importedName == "server" || strings.HasPrefix(importedName, "server/")) &&
		importer != modulePath+"/cmd/foya" {
		t.Errorf("%s: only cmd/foya may import internal/server", file)
	}
	if (importedName == "kernel" || strings.HasPrefix(importedName, "kernel/")) &&
		importer != modulePath+"/internal/server" &&
		importer != modulePath+"/cmd/foya" {
		t.Errorf("%s: only transport entry points may import internal/kernel", file)
	}
	if importer == modulePath+"/internal/kernel" && importedName == "server" {
		t.Errorf("%s: kernel must not depend on its HTTP adapter", file)
	}
	if importer == modulePath+"/internal/model" {
		t.Errorf("%s: the model SPI must not depend on internal implementation packages", file)
	}
	if importer == modulePath+"/internal/conversation" {
		switch importedName {
		case "model", "storage":
		default:
			t.Errorf("%s: conversation may only depend on model and storage, not %s", file, importedName)
		}
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate architecture test")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(current), "..", ".."))
}

func packagePath(root, dir string) string {
	relativePath, err := filepath.Rel(root, dir)
	if err != nil || relativePath == "." {
		return modulePath
	}
	return modulePath + "/" + filepath.ToSlash(relativePath)
}

func fileImports(t *testing.T, path string) []string {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
	if err != nil {
		t.Fatal(err)
	}
	imports := make([]string, 0, len(file.Imports))
	for _, spec := range file.Imports {
		if imported, err := strconv.Unquote(spec.Path.Value); err == nil {
			imports = append(imports, imported)
		}
	}
	return imports
}

func relative(root, path string) string {
	value, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return filepath.ToSlash(value)
}
