// pkg/server/server.go
package server

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/ishanmadhav/geeparse/pkg/callgraph"
)

// StartServer registers HTTP routes and starts listening on addr (e.g. ":8080").
func StartServer(addr string, graph map[string]callgraph.FunctionNode) error {
	mux := http.NewServeMux()

	// JSON endpoint
	mux.HandleFunc("/graph.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if err := json.NewEncoder(w).Encode(graph); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})

	// UI endpoint
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, indexHTML)
	})

	fmt.Printf("Serving call-graph UI at http://localhost%s/\n", addr)
	return http.ListenAndServe(addr, mux)
}

// indexHTML is our D3-based browser UI, with cycle detection baked in.
// Note: we switched the JS node-click snippet to use string concatenation
// instead of backticks, so this can remain a valid Go raw string.
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
    #info-panel {
      position:absolute; top:10px; right:10px;
      width:300px; max-height:90vh; overflow:auto;
      background:#f9f9f9; padding:10px; border:1px solid #ccc;
    }
  </style>
</head>
<body>
<div id="info-panel"><i>Click a node to see details</i></div>
<script>
fetch('/graph.json')
  .then(r => r.json())
  .then(graph => drawTree(graph))
  .catch(err => { document.body.innerText = 'Error loading graph: ' + err; });

function drawTree(graph) {
  const toTree = obj => {
    const all = new Set(Object.keys(obj));
    Object.values(obj).forEach(n => n.callees.forEach(c => all.delete(c)));
    const build = (name, vis = new Set()) => {
      if (vis.has(name)) {
        return { name: name, signature: obj[name].signature, definition: obj[name].definition, children: [] };
      }
      vis.add(name);
      return {
        name: name,
        signature: obj[name].signature,
        definition: obj[name].definition,
        children: obj[name].callees.map(c => build(c, new Set(vis))),
      };
    };
    return { name: 'root', children: Array.from(all).map(r => build(r)) };
  };

  const data = toTree(graph);
  const W = innerWidth, H = innerHeight;
  const M = { top:20, right:120, bottom:20, left:120 };
  const svg = d3.select('body').append('svg')
    .attr('width', W).attr('height', H)
    .append('g').attr('transform','translate(' + M.left + ',' + M.top + ')');

  const root = d3.hierarchy(data);
  d3.tree().size([H - M.top - M.bottom, W - M.left - M.right])(root);

  svg.selectAll('.link').data(root.links()).join('path')
    .attr('class','link')
    .attr('d', d3.linkHorizontal().x(d=>d.y).y(d=>d.x));

  const node = svg.selectAll('.node').data(root.descendants()).join('g')
    .attr('class','node')
    .attr('transform', d=>'translate(' + d.y + ',' + d.x + ')')
    .on('click', (e, d) => {
      d3.select('#info-panel').html(
        '<h3>' + d.data.name + '</h3>' +
        '<pre>' + d.data.signature + '</pre>' +
        '<pre>' + d.data.definition + '</pre>'
      );
    });

  node.append('circle').attr('r',4);
  node.append('text')
    .attr('dy',3)
    .attr('x', d => d.children ? -8 : 8)
    .style('text-anchor', d => d.children ? 'end' : 'start')
    .text(d => d.data.name);
}
</script>
</body>
</html>`
