package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"io/fs"
	"log"
	"path/filepath"
)

// someFunc is just an example function in this file
func someFunc() {}

func main() {
	// Command-line flag: path to a file or directory
	path := flag.String("path", ".", "file or directory to scan for Go functions, structs, and call hierarchy")
	flag.Parse()

	// Walk the path; process each .go file found
	err := filepath.WalkDir(*path, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && filepath.Ext(path) == ".go" {
			// listFunctions(path)
			// listStructs(path)
			listCallHierarchy(path)
		}
		return nil
	})
	if err != nil {
		log.Fatalf("error walking path: %v", err)
	}
}

// listFunctions parses the Go source file at filename and prints its functions.
func listFunctions(filename string) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, nil, 0)
	if err != nil {
		log.Printf("failed to parse %s: %v", filename, err)
		return
	}

	fmt.Printf("Functions in %s:\n", filename)
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok {
			receiver := ""
			if fn.Recv != nil && len(fn.Recv.List) > 0 {
				buf := &bytes.Buffer{}
				printer.Fprint(buf, fset, fn.Recv.List[0].Type)
				receiver = fmt.Sprintf(" (%s)", buf.String())
			}
			fmt.Printf("  - %s%s\n", fn.Name.Name, receiver)
		}
	}
	fmt.Println()
}

// listStructs parses the Go source file at filename and prints its structs and fields.
func listStructs(filename string) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, nil, 0)
	if err != nil {
		log.Printf("failed to parse %s: %v", filename, err)
		return
	}

	fmt.Printf("Structs in %s:\n", filename)
	for _, decl := range file.Decls {
		genDecl, ok := decl.(*ast.GenDecl)
		if !ok || genDecl.Tok != token.TYPE {
			continue
		}
		for _, spec := range genDecl.Specs {
			typeSpec, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}
			structType, ok := typeSpec.Type.(*ast.StructType)
			if !ok {
				continue
			}
			fmt.Printf("  - %s\n", typeSpec.Name.Name)
			for _, field := range structType.Fields.List {
				names := []string{"<anonymous>"}
				if len(field.Names) > 0 {
					names = make([]string, len(field.Names))
					for i, n := range field.Names {
						names[i] = n.Name
					}
				}
				buf := &bytes.Buffer{}
				printer.Fprint(buf, fset, field.Type)
				for _, name := range names {
					fmt.Printf("      • %s %s\n", name, buf.String())
				}
			}
		}
	}
	fmt.Println()
}

// listCallHierarchy parses the Go source file at filename and prints a simple call hierarchy among functions defined in the file.
func listCallHierarchy(filename string) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, nil, 0)
	if err != nil {
		log.Printf("failed to parse %s: %v", filename, err)
		return
	}

	// Collect all function names defined in this file
	funcNames := map[string]struct{}{}
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok {
			funcNames[fn.Name.Name] = struct{}{}
		}
	}

	// Map each function to the set of other defined functions it calls
	calls := make(map[string]map[string]struct{})
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Body != nil {
			caller := fn.Name.Name
			calls[caller] = make(map[string]struct{})
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				switch fun := call.Fun.(type) {
				case *ast.Ident:
					if _, exists := funcNames[fun.Name]; exists {
						calls[caller][fun.Name] = struct{}{}
					}
				case *ast.SelectorExpr:
					if _, exists := funcNames[fun.Sel.Name]; exists {
						calls[caller][fun.Sel.Name] = struct{}{}
					}
				}
				return true
			})
		}
	}

	fmt.Printf("Call hierarchy in %s:\n", filename)
	for caller, calleeSet := range calls {
		fmt.Printf("  - %s calls:\n", caller)
		if len(calleeSet) == 0 {
			fmt.Printf("      • none\n")
		}
		for callee := range calleeSet {
			fmt.Printf("      • %s\n", callee)
		}
	}
	fmt.Println()
}
