package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"io/fs"
	"log"
	"net/http"
	"path/filepath"
)

// indexHTML is the embedded HTML UI for visualizing the call hierarchy.
const indexHTML = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <title>Call Hierarchy</title>
  <script src="https://d3js.org/d3.v7.min.js"></script>
  <style>
    .node circle { fill: #fff; stroke: steelblue; stroke-width: 3px; }
    .link { fill: none; stroke: #ccc; stroke-width: 2px; }
    text { font: 12px sans-serif; }
    #info-panel { position:absolute; top:10px; right:10px; width:300px; max-height:90vh; overflow:auto; background:#f9f9f9; padding:10px; border:1px solid #ccc; }
  </style>
</head>
<body>
<div id="info-panel"><i>Click a node to see details</i></div>
<script>
fetch('/graph.json')
  .then(response => response.json())
  .then(graph => drawTree(graph))
  .catch(err => { document.body.innerText = 'Error loading graph: ' + err; });

function drawTree(graph) {
  const toTree = obj => {
    const allNames = new Set(Object.keys(obj));
    Object.values(obj).forEach(node => node.callees.forEach(c => allNames.delete(c)));
    const roots = Array.from(allNames);
    const build = name => ({ name, signature: obj[name].signature, definition: obj[name].definition, children: obj[name].callees.map(build) });
    return { name: 'root', children: roots.map(build) };
  };

  const data = toTree(graph);
  const width = window.innerWidth, height = window.innerHeight;
  const margin = { top: 20, right: 120, bottom: 20, left: 120 };
  const svg = d3.select('body').append('svg')
    .attr('width', width)
    .attr('height', height)
    .append('g')
    .attr('transform', 'translate(' + margin.left + ',' + margin.top + ')');

  const root = d3.hierarchy(data);
  d3.tree().size([height - margin.top - margin.bottom, width - margin.left - margin.right])(root);

  svg.selectAll('.link')
    .data(root.links())
    .join('path')
      .attr('class', 'link')
      .attr('d', d3.linkHorizontal().x(d => d.y).y(d => d.x));

  const node = svg.selectAll('.node')
    .data(root.descendants())
    .join('g')
      .attr('class', 'node')
      .attr('transform', d => 'translate(' + d.y + ',' + d.x + ')')
      .on('click', (event, d) => {
        const panel = d3.select('#info-panel');
        panel.html('<h3>' + d.data.name + '</h3>'
          + '<pre>' + d.data.signature + '</pre>'
          + '<pre>' + d.data.definition + '</pre>');
      });

  node.append('circle').attr('r', 4);
  node.append('text')
    .attr('dy', 3)
    .attr('x', d => d.children ? -8 : 8)
    .style('text-anchor', d => d.children ? 'end' : 'start')
    .text(d => d.data.name);
}
</script>
</body>
</html>`

// Config holds command-line configuration.
type Config struct {
	RootPath string
	Port     int
}

// FuncDetail stores signature and definition for a function.
type FuncDetail struct {
	Signature  string `json:"signature"`
	Definition string `json:"definition"`
}

// FunctionNode represents a node in the call graph.
type FunctionNode struct {
	Callees    []string `json:"callees"`
	Signature  string   `json:"signature"`
	Definition string   `json:"definition"`
}

func main() {
	cfg := parseFlags()
	graph, err := buildCallGraph(cfg.RootPath)
	if err != nil {
		log.Fatalf("failed to build call graph: %v", err)
	}

	registerHandlers(graph)
	startServer(cfg.Port)
}

// parseFlags parses command-line flags.
func parseFlags() Config {
	var cfg Config
	flag.StringVar(&cfg.RootPath, "path", ".", "file or directory to scan for call hierarchy")
	flag.IntVar(&cfg.Port, "port", 8080, "port for HTTP server")
	flag.Parse()
	return cfg
}

// registerHandlers sets up HTTP handlers for graph JSON and the UI.
func registerHandlers(graph map[string]FunctionNode) {
	http.HandleFunc("/graph.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(graph); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, indexHTML)
	})
}

// startServer launches the HTTP server on the given port.
func startServer(port int) {
	addr := fmt.Sprintf(":%d", port)
	log.Printf("Serving call hierarchy UI at http://localhost%s", addr)
	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

// buildCallGraph walks the rootPath, parses Go files, and returns the enriched call graph.
func buildCallGraph(rootPath string) (map[string]FunctionNode, error) {
	fset := token.NewFileSet()
	funcNames, fileAsts, err := parseGoFiles(fset, rootPath)
	if err != nil {
		return nil, err
	}
	// Extract signatures and definitions
	details := extractFuncDetails(funcNames, fileAsts, fset)
	// Extract raw call graph (caller -> []callee)
	raw := extractCallGraph(funcNames, fileAsts)

	// Merge into enriched nodes, ensuring non-nil slices
	graph := make(map[string]FunctionNode)
	for name, det := range details {
		cs := raw[name]
		if cs == nil {
			cs = []string{}
		}
		graph[name] = FunctionNode{
			Callees:    cs,
			Signature:  det.Signature,
			Definition: det.Definition,
		}
	}
	return graph, nil
}

// parseGoFiles walks the directory, parses each .go file, and
// returns a set of function names and parsed ASTs.
func parseGoFiles(fset *token.FileSet, rootPath string) (map[string]struct{}, []*ast.File, error) {
	funcNames := make(map[string]struct{})
	var fileAsts []*ast.File
	err := filepath.WalkDir(rootPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Ext(path) != ".go" {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			log.Printf("parse error in %s: %v", path, err)
			return nil
		}
		fileAsts = append(fileAsts, f)
		for _, decl := range f.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok {
				funcNames[fn.Name.Name] = struct{}{}
			}
		}
		return nil
	})
	return funcNames, fileAsts, err
}

// extractFuncDetails extracts signature and full definition for each function.
func extractFuncDetails(funcNames map[string]struct{}, files []*ast.File, fset *token.FileSet) map[string]FuncDetail {
	details := make(map[string]FuncDetail)
	for _, f := range files {
		for _, decl := range f.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok {
				// signature
				bufSig := &bytes.Buffer{}
				printer.Fprint(bufSig, fset, fn.Type)
				// definition
				bufDef := &bytes.Buffer{}
				printer.Fprint(bufDef, fset, fn)
				details[fn.Name.Name] = FuncDetail{Signature: bufSig.String(), Definition: bufDef.String()}
			}
		}
	}
	return details
}

// extractCallGraph inspects ASTs and builds a map of caller -> callee list.
func extractCallGraph(funcNames map[string]struct{}, files []*ast.File) map[string][]string {
	graph := make(map[string][]string)
	for _, f := range files {
		for _, decl := range f.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Body != nil {
				caller := fn.Name.Name
				calleeSet := make(map[string]struct{})
				ast.Inspect(fn.Body, func(n ast.Node) bool {
					callExpr, ok := n.(*ast.CallExpr)
					if !ok {
						return true
					}
					switch fun := callExpr.Fun.(type) {
					case *ast.Ident:
						if _, exists := funcNames[fun.Name]; exists {
							calleeSet[fun.Name] = struct{}{}
						}
					case *ast.SelectorExpr:
						if _, exists := funcNames[fun.Sel.Name]; exists {
							calleeSet[fun.Sel.Name] = struct{}{}
						}
					}
					return true
				})
				for callee := range calleeSet {
					graph[caller] = append(graph[caller], callee)
				}
			}
		}
	}
	return graph
}
