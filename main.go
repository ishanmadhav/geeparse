// main.go
package main

import (
	"log"

	"github.com/ishanmadhav/geeparse/pkg/callgraph"
	"github.com/ishanmadhav/geeparse/pkg/persistence"
	"github.com/ishanmadhav/geeparse/pkg/server"
)

func main() {
	// build in-memory graph
	graph, err := callgraph.BuildCallGraph(".")
	if err != nil {
		log.Fatal(err)
	}

	// open persistent store
	store, err := persistence.NewStore("graph.db")
	if err != nil {
		log.Fatal(err)
	}
	defer store.Close()

	// save to disk
	if err := store.SaveGraph(graph); err != nil {
		log.Fatal(err)
	}

	// reload from disk
	loaded, err := store.LoadGraph()
	if err != nil {
		log.Fatal(err)
	}

	// serve JSON/UI from loaded graph
	if err := server.StartServer(":8080", loaded); err != nil {
		log.Fatal(err)
	}
}
