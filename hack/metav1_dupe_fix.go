package main

import (
	"bytes"
	"flag"
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

var verbose bool

func main() {
	flag.BoolVar(&verbose, "v", false, "Enable verbose output")
	flag.BoolVar(&verbose, "verbose", false, "Enable verbose output")
	flag.Parse()

	if flag.NArg() < 1 {
		fmt.Println("Usage: go run hack/metav1_dupe_fix.go [-v] <directory>")
		os.Exit(1)
	}

	rootDir := flag.Arg(0)
	fset := token.NewFileSet()

	err := filepath.Walk(rootDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

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
	var changes []string

	ast.Inspect(file, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.CompositeLit:
			if sel, ok := x.Type.(*ast.SelectorExpr); ok {
				if ident, ok := sel.X.(*ast.Ident); ok && ident.Name == "metav1" {
					if sel.Sel.Name == "Time" {
						for _, elt := range x.Elts {
							if kv, ok := elt.(*ast.KeyValueExpr); ok && kv.Key.(*ast.Ident).Name == "Time" {
								if call, ok := kv.Value.(*ast.CallExpr); ok {
									if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
										if ident, ok := sel.X.(*ast.Ident); ok && ident.Name == "metav1" {
											if sel.Sel.Name == "Now" {
												original := formatNode(fset, x)
												if unary, ok := n.(*ast.UnaryExpr); ok && unary.Op == token.AND {
													newCall := &ast.CallExpr{Fun: call.Fun, Args: call.Args}
													unary.X = newCall
													modified = true
													changes = append(changes, fmt.Sprintf("Replaced %s with %s",
														original, formatNode(fset, newCall)))
												} else if parent, ok := n.(*ast.CompositeLit); ok {
													newCall := &ast.CallExpr{Fun: call.Fun, Args: call.Args}
													astutil.Apply(parent, func(cr *astutil.Cursor) bool {
														if cr.Node() == parent {
															cr.Replace(newCall)
															return false
														}
														return true
													}, nil)
													modified = true
													changes = append(changes, fmt.Sprintf("Replaced %s with %s",
														original, formatNode(fset, newCall)))
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

			if isNestedMetav1Duration(x) {
				original := formatNode(fset, x)
				simplifyNestedDuration(x)
				modified = true
				changes = append(changes, fmt.Sprintf("Simplified nested duration: %s -> %s",
					original, formatNode(fset, x)))
			}

		case *ast.CallExpr:
			// Handle Add method calls with metav1.Duration arguments
			if sel, ok := x.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Add" {
				// Wrap any Add() result in metav1.NewTime()
				original := formatNode(fset, x)
				newCall := &ast.CallExpr{
					Fun: &ast.SelectorExpr{
						X:   &ast.Ident{Name: "metav1"},
						Sel: &ast.Ident{Name: "NewTime"},
					},
					Args: []ast.Expr{x},
				}
				modified = true
				changes = append(changes, fmt.Sprintf("Wrapped Add() result: %s -> %s",
					original, formatNode(fset, newCall)))
				astutil.Apply(x, func(cr *astutil.Cursor) bool {
					if cr.Node() == x {
						cr.Replace(newCall)
						return false
					}
					return true
				}, nil)
			}

		case *ast.KeyValueExpr:
			if cl, ok := x.Value.(*ast.CompositeLit); ok {
				if sel, ok := cl.Type.(*ast.SelectorExpr); ok {
					if ident, ok := sel.X.(*ast.Ident); ok && ident.Name == "metav1" && sel.Sel.Name == "Time" {
						// Replace metav1.Time{Time: ...} with direct value
						for _, elt := range cl.Elts {
							if kv, ok := elt.(*ast.KeyValueExpr); ok && kv.Key.(*ast.Ident).Name == "Time" {
								original := formatNode(fset, cl)
								x.Value = kv.Value
								modified = true
								changes = append(changes, fmt.Sprintf("Simplified Time struct: %s -> %s",
									original, formatNode(fset, kv.Value)))
							}
						}
					}
				}
			}

			// Handle metav1.NewTime(metav1.Now()) pattern
			if sel, ok := x.Fun.(*ast.SelectorExpr); ok {
				if ident, ok := sel.X.(*ast.Ident); ok && ident.Name == "metav1" {
					if sel.Sel.Name == "NewTime" {
						if len(x.Args) == 1 {
							if call, ok := x.Args[0].(*ast.CallExpr); ok {
								if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
									if ident, ok := sel.X.(*ast.Ident); ok && ident.Name == "metav1" {
										if sel.Sel.Name == "Now" {
											original := formatNode(fset, x)
											*x = *call
											modified = true
											changes = append(changes, fmt.Sprintf("Replaced %s with %s",
												original, formatNode(fset, call)))
										}
									}
								}
							}
						}
					}
				}
			}

		case *ast.KeyValueExpr:
			if call, ok := x.Value.(*ast.CallExpr); ok {
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Add" {
					if selRecv, ok := sel.X.(*ast.CallExpr); ok {
						if selNow, ok := selRecv.Fun.(*ast.SelectorExpr); ok && selNow.Sel.Name == "Now" {
							// Wrap metav1.Now().Add() with metav1.NewTime()
							original := formatNode(fset, x.Value)
							newCall := &ast.CallExpr{
								Fun: &ast.SelectorExpr{
									X:   &ast.Ident{Name: "metav1"},
									Sel: &ast.Ident{Name: "NewTime"},
								},
								Args: []ast.Expr{call},
							}
							x.Value = newCall
							modified = true
							changes = append(changes, fmt.Sprintf("Wrapped time.Time with metav1.NewTime(): %s -> %s",
								original, formatNode(fset, newCall)))
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

		if verbose {
			fmt.Printf("Modified %s:\n", filename)
			for _, change := range changes {
				fmt.Printf("  • %s\n", change)
			}
		}
	}

	return nil
}

func formatNode(fset *token.FileSet, node ast.Node) string {
	var buf bytes.Buffer
	format.Node(&buf, fset, node)
	return strings.TrimSpace(buf.String())
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
// Add this case to handle time.Time -> metav1.Time conversions

// Enhance the existing Add() handler 
