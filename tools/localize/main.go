// Command localize 将目录查找应用于原生 UI 展示字面量。模型值、内部路由标识符
// 和弹窗判别值保持不变。
package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "catalogs" {
		updateCatalogs()
		return
	}
	paths, _ := filepath.Glob("internal/ui/*.go")
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") || strings.HasSuffix(path, "language.go") {
			continue
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			panic(err)
		}
		changed := false
		wrap := func(value ast.Expr) ast.Expr {
			lit, ok := value.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return value
			}
			text, _ := strconv.Unquote(lit.Value)
			if !strings.ContainsFunc(text, func(r rune) bool { return unicode.Is(unicode.Han, r) }) {
				return value
			}
			changed = true
			return &ast.CallExpr{Fun: ast.NewIdent("tr"), Args: []ast.Expr{ast.NewIdent("c"), lit}}
		}
		for _, declaration := range file.Decls {
			fn, ok := declaration.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			hasContext := false
			for _, param := range fn.Type.Params.List {
				for _, name := range param.Names {
					if name.Name == "c" {
						hasContext = true
					}
				}
			}
			if !hasContext {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				switch n := n.(type) {
				case *ast.CallExpr:
					if selectCall, ok := n.Fun.(*ast.SelectorExpr); ok {
						switch selectCall.Sel.Name {
						case "Checkbox", "Switch":
							if receiver, ok := selectCall.X.(*ast.Ident); ok && receiver.Name == "ui" && len(n.Args) == 3 {
								n.Args[2] = wrap(n.Args[2])
							}
						case "Text":
							if receiver, ok := selectCall.X.(*ast.Ident); ok && receiver.Name == "ui" && len(n.Args) == 2 {
								n.Args[1] = wrap(n.Args[1])
								if value, ok := n.Args[1].(*ast.Ident); ok {
									selected := map[string]bool{"title": true, "label": true, "hint": true, "status": true, "expiry": true, "state": true, "peakText": true}
									if selected[value.Name] || (value.Name == "value" && fn.Name.Name != "infoPair") {
										n.Args[1] = &ast.CallExpr{Fun: ast.NewIdent("tr"), Args: []ast.Expr{ast.NewIdent("c"), value}}
										changed = true
									}
								}
							}
						case "FieldGroup":
							if len(n.Args) > 1 {
								n.Args[1] = wrap(n.Args[1])
							}
						case "Sprintf", "Placeholder", "Tooltip":
							if len(n.Args) > 0 {
								n.Args[0] = wrap(n.Args[0])
							}
						}
					}
				case *ast.KeyValueExpr:
					if key, ok := n.Key.(*ast.Ident); ok && (key.Name == "Help" || key.Name == "Placeholder") {
						n.Value = wrap(n.Value)
					}
				}
				return true
			})
		}
		if changed {
			var out bytes.Buffer
			if err := format.Node(&out, fset, file); err != nil {
				panic(err)
			}
			if err := os.WriteFile(path, out.Bytes(), 0644); err != nil {
				panic(err)
			}
			fmt.Println(path)
		}
	}
}
