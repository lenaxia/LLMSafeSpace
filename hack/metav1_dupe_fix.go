package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token" 
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/tools/go/ast/astutil"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: go run hack/metav1_dupe_fix.go <directory>")
		os.Exit(1)
	}

	rootDir := os.Args[1]
	fset := token.NewFileSet()

	err := filepath.Walk(rootDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip directories, non-Go files, and hack directory
		if info.IsDir() {
			if info.Name() == "hack" || info.Name() == "vendor" || info.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}

		if !strings.HasSuffix(path, ".go") {
			return nil
		}

		return processFile(path, fset)
	})

	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
}

func processFile(filename string, fset *token.FileSet) error {
	src, err := ioutil.ReadFile(filename)
	if err != nil {
		return fmt.Errorf("error reading file %s: %v", filename, err)
	}

	file, err := parser.ParseFile(fset, filename, src, parser.ParseComments)
	if err != nil {
		return fmt.Errorf("error parsing file %s: %v", filename, err)
	}

	modified := false

	ast.Inspect(file, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.CompositeLit:
			// Handle &metav1.Time{Time: metav1.Now()}
			if sel, ok := x.Type.(*ast.SelectorExpr); ok {
				if ident, ok := sel.X.(*ast.Ident); ok && ident.Name == "metav1" {
					if sel.Sel.Name == "Time" {
						for _, elt := range x.Elts {
							if kv, ok := elt.(*ast.KeyValueExpr); ok && kv.Key.(*ast.Ident).Name == "Time" {
								if call, ok := kv.Value.(*ast.CallExpr); ok {
									if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
										if ident, ok := sel.X.(*ast.Ident); ok && ident.Name == "metav1" {
											if sel.Sel.Name == "Now" {
												// Replace the parent node with just metav1.Now()
												if unary, ok := n.(*ast.UnaryExpr); ok && unary.Op == token.AND {
													// Handle &metav1.Time{Time: metav1.Now()} case
													newCall := &ast.CallExpr{
														Fun:  call.Fun,
														Args: call.Args,
													}
													unary.X = newCall
													modified = true
												} else if parent, ok := n.(*ast.CompositeLit); ok {
													// Handle metav1.Time{Time: metav1.Now()} case
													newCall := &ast.CallExpr{
														Fun:  call.Fun,
														Args: call.Args,
													}
													astutil.Apply(parent, func(cr *astutil.Cursor) bool {
														if cr.Node() == parent {
															cr.Replace(newCall)
															return false
														}
														return true
													}, nil)
													modified = true
												}
											}
										}
									}
								}
							}
						}
					}
				}
			}

			// Handle metav1.Duration{Duration: metav1.Duration{...}}
			if isNestedMetav1Duration(x) {
				simplifyNestedDuration(x)
				modified = true
			}

		case *ast.CallExpr:
			// Handle metav1.NewTime(metav1.Now())
			if sel, ok := x.Fun.(*ast.SelectorExpr); ok {
				if ident, ok := sel.X.(*ast.Ident); ok && ident.Name == "metav1" {
					if sel.Sel.Name == "NewTime" {
						if len(x.Args) == 1 {
							if call, ok := x.Args[0].(*ast.CallExpr); ok {
								if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
									if ident, ok := sel.X.(*ast.Ident); ok && ident.Name == "metav1" {
										if sel.Sel.Name == "Now" {
											// Replace with just metav1.Now()
											*x = *call
											modified = true
										}
									}
								}
							}
						}
					}
				}
			}
		}
		return true
	})

	if modified {
		var buf bytes.Buffer
		if err := format.Node(&buf, fset, file); err != nil {
			return fmt.Errorf("error formatting file %s: %v", filename, err)
		}

		if err := ioutil.WriteFile(filename, buf.Bytes(), 0644); err != nil {
			return fmt.Errorf("error writing file %s: %v", filename, err)
		}

		fmt.Printf("Fixed metav1 references in: %s\n", filename)
	}

	return nil
}

func isNestedMetav1Duration(node ast.Node) bool {
	if cl, ok := node.(*ast.CompositeLit); ok {
		if sel, ok := cl.Type.(*ast.SelectorExpr); ok {
			if ident, ok := sel.X.(*ast.Ident); ok && ident.Name == "metav1" {
				if sel.Sel.Name == "Duration" {
					for _, elt := range cl.Elts {
						if kv, ok := elt.(*ast.KeyValueExpr); ok && kv.Key.(*ast.Ident).Name == "Duration" {
							if _, ok := kv.Value.(*ast.CompositeLit); ok {
								return true
							}
						}
					}
				}
			}
		}
	}
	return false
}

func simplifyNestedDuration(cl *ast.CompositeLit) {
	for i, elt := range cl.Elts {
		if kv, ok := elt.(*ast.KeyValueExpr); ok && kv.Key.(*ast.Ident).Name == "Duration" {
			if innerCl, ok := kv.Value.(*ast.CompositeLit); ok {
				if innerSel, ok := innerCl.Type.(*ast.SelectorExpr); ok {
					if innerIdent, ok := innerSel.X.(*ast.Ident); ok && innerIdent.Name == "metav1" {
						if innerSel.Sel.Name == "Duration" {
							for _, innerElt := range innerCl.Elts {
								if innerKv, ok := innerElt.(*ast.KeyValueExpr); ok && innerKv.Key.(*ast.Ident).Name == "Duration" {
									cl.Elts[i].(*ast.KeyValueExpr).Value = innerKv.Value
								}
							}
						}
					}
				}
			}
		}
	}
}
