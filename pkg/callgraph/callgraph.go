// pkg/callgraph/callgraph.go
package callgraph

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"io/fs"
	"log"
	"path/filepath"

	"github.com/ishanmadhav/geeparse/pkg/lspclient"
	"go.lsp.dev/protocol"
)

// FunctionNode represents one function in the call graph.
type FunctionNode struct {
	Callees    []string `json:"callees"`
	Signature  string   `json:"signature"`
	Definition string   `json:"definition"`
}

// BuildCallGraph walks rootDir, parses your .go files to get signatures/definitions,
// then uses gopls (via lspclient) to compute only *internal* caller→callee edges.
func BuildCallGraph(rootDir string) (map[string]FunctionNode, error) {
	// 1. Parse files & collect your function names
	names, files, fset, err := parseGoFiles(rootDir)
	if err != nil {
		return nil, err
	}

	// 2. Extract AST-based signature & definition for each
	details := extractDetails(files, fset)

	// 3. Start a single gopls LSP session
	client, err := lspclient.New(rootDir)
	if err != nil {
		return nil, err
	}
	defer client.Close()

	// 4. Open each file in gopls
	for _, f := range files {
		filename := fset.Position(f.Package).Filename
		if err := client.OpenDocument(filename); err != nil {
			return nil, err
		}
	}

	// 5. Compute only internal call-graph edges via LSP
	rawGraph, err := extractGraphLSP(client, files, fset, names)
	if err != nil {
		return nil, err
	}

	// 6. Assemble final JSON-serializable map
	out := make(map[string]FunctionNode, len(details))
	for name, det := range details {
		callees := rawGraph[name]
		if callees == nil {
			callees = []string{}
		}
		out[name] = FunctionNode{
			Callees:    callees,
			Signature:  det.Signature,
			Definition: det.Definition,
		}
	}
	return out, nil
}

// parseGoFiles finds and parses all .go files under rootDir,
// returns your function-names set, the parsed ASTs, and the FileSet.
func parseGoFiles(rootDir string) (map[string]struct{}, []*ast.File,
	*token.FileSet, error) {

	fset := token.NewFileSet()
	names := make(map[string]struct{})
	var files []*ast.File

	err := filepath.WalkDir(rootDir, func(path string, d fs.DirEntry, e error) error {
		if e != nil || d.IsDir() || filepath.Ext(path) != ".go" {
			return nil
		}
		astFile, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			log.Printf("parse error %s: %v", path, err)
			return nil
		}
		files = append(files, astFile)
		for _, decl := range astFile.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok {
				names[fn.Name.Name] = struct{}{}
			}
		}
		return nil
	})
	return names, files, fset, err
}

// extractDetails builds a map[name] giving each func's signature+definition.
type funcDetail struct {
	Signature  string
	Definition string
}

func extractDetails(files []*ast.File, fset *token.FileSet) map[string]funcDetail {
	out := make(map[string]funcDetail, len(files))
	for _, f := range files {
		for _, decl := range f.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok {
				var sigBuf, defBuf bytes.Buffer
				printer.Fprint(&sigBuf, fset, fn.Type)
				printer.Fprint(&defBuf, fset, fn)
				out[fn.Name.Name] = funcDetail{
					Signature:  sigBuf.String(),
					Definition: defBuf.String(),
				}
			}
		}
	}
	return out
}

// extractGraphLSP uses lspclient to prepare call-hierarchy and then
// fetch outgoing calls *only* for functions in the `names` set.
func extractGraphLSP(
	client *lspclient.Client,
	files []*ast.File,
	fset *token.FileSet,
	names map[string]struct{},
) (map[string][]string, error) {

	graph := make(map[string][]string)

	for _, f := range files {
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			caller := fn.Name.Name
			pos := fset.Position(fn.Name.Pos())
			protoPos := protocol.Position{
				Line:      uint32(pos.Line - 1),
				Character: uint32(pos.Column - 1),
			}
			file := pos.Filename

			items, err := client.PrepareCallHierarchy(file, protoPos)
			if err != nil {
				log.Printf("prepare hierarchy %s: %v", caller, err)
				continue
			}
			if len(items) == 0 {
				continue
			}
			root := items[0]

			outgoing, err := client.OutgoingCalls(root)
			if err != nil {
				log.Printf("outgoing calls %s: %v", caller, err)
				continue
			}

			seen := make(map[string]struct{})
			for _, call := range outgoing {
				callee := call.To.Name
				// ONLY record if it's one of your own funcs
				if _, ok := names[callee]; !ok {
					continue
				}
				if _, dup := seen[callee]; !dup {
					graph[caller] = append(graph[caller], callee)
					seen[callee] = struct{}{}
				}
			}
		}
	}
	return graph, nil
}
