package chameleon

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFeatureCatalogReferences(t *testing.T) {
	var version struct {
		SchemaVersion int `json:"schema_version"`
	}
	if e := json.Unmarshal(featureCatalogJSON, &version); e != nil || version.SchemaVersion != FeatureSchemaVersion {
		t.Fatal("feature schema version mismatch", e)
	}
	symbols := map[string]bool{}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	files2, _ := filepath.Glob("dfu/*.go")
	files = append(files, files2...)
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		f, e := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if e != nil {
			t.Fatal(e)
		}
		prefix := ""
		if strings.HasPrefix(path, "dfu/") {
			prefix = "dfu."
		}
		for _, decl := range f.Decls {
			switch n := decl.(type) {
			case *ast.FuncDecl:
				name := n.Name.Name
				if n.Recv != nil {
					typ := n.Recv.List[0].Type
					if p, ok := typ.(*ast.StarExpr); ok {
						typ = p.X
					}
					if id, ok := typ.(*ast.Ident); ok {
						name = id.Name + "." + name
					}
				}
				symbols[prefix+name] = true
			case *ast.GenDecl:
				for _, s := range n.Specs {
					if v, ok := s.(*ast.ValueSpec); ok {
						for _, name := range v.Names {
							symbols[prefix+name.Name] = true
						}
					}
				}
			}
		}
	}
	seen := map[string]bool{}
	for _, f := range Features() {
		if f.ID == "" || seen[f.ID] {
			t.Errorf("duplicate/empty ID %q", f.ID)
		}
		seen[f.ID] = true
		for _, e := range []FeatureEntry{f.Web, f.CLI, f.SDK} {
			if e.Status != "supported" && e.Status != "partial" && e.Status != "raw" && e.Status != "missing" && e.Status != "removed" {
				t.Errorf("%s: bad entry status", f.ID)
			}
		}
		for _, name := range f.SDK.Entries {
			if !symbols[name] {
				t.Errorf("%s: missing SDK symbol %s", f.ID, name)
			}
		}
		for _, source := range f.Sources {
			if _, err := os.Stat(source); err != nil {
				t.Errorf("%s: %v", f.ID, err)
			}
		}
		cmds := append([]Command{}, f.Commands...)
		for _, condition := range f.Conditional {
			cmds = append(cmds, condition.Commands...)
		}
		for _, c := range cmds {
			if _, ok := LookupCommand(c); !ok {
				t.Errorf("%s: unknown command %d", f.ID, c)
			}
		}
		if f.RequirementMode == "offline" && len(f.Commands) > 0 {
			t.Errorf("%s: offline command requirements", f.ID)
		}
	}
}

func TestFeatureDocumentationMatchesCatalog(t *testing.T) {
	doc, err := os.ReadFile("docs/features.md")
	if err != nil {
		t.Fatal(err)
	}
	hash := fmt.Sprintf("%x", sha256.Sum256(bytes.ReplaceAll(featureCatalogJSON, []byte("\r\n"), []byte("\n"))))
	if !strings.Contains(string(doc), "Catalog SHA256: "+hash) {
		t.Fatal("regenerate docs/features.md with scripts/generate_features.py")
	}
	for _, f := range Features() {
		if !strings.Contains(string(doc), "## "+f.ID+" — ") {
			t.Errorf("missing feature documentation %s", f.ID)
		}
	}
}
