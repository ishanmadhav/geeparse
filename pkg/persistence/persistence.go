package persistence

import (
	"database/sql"
	"fmt"

	// CGO sqlite3 driver; embeds SQLite in your binary.
	_ "github.com/mattn/go-sqlite3"

	"github.com/ishanmadhav/geeparse/pkg/callgraph"
)

// Store provides methods to persist and load call-graphs from an embedded SQLite DB.
type Store struct {
	db *sql.DB
}

// NewStore opens (or creates) the SQLite file at dbPath,
// ensures the schema is in place, and returns a Store.
func NewStore(dbPath string) (*Store, error) {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open sqlite db: %w", err)
	}

	schema := `
	PRAGMA foreign_keys = ON;
	CREATE TABLE IF NOT EXISTS functions (
	  name TEXT PRIMARY KEY,
	  signature TEXT NOT NULL,
	  definition TEXT NOT NULL
	);
	CREATE TABLE IF NOT EXISTS calls (
	  caller TEXT NOT NULL,
	  callee TEXT NOT NULL,
	  PRIMARY KEY (caller, callee),
	  FOREIGN KEY (caller) REFERENCES functions(name) ON DELETE CASCADE,
	  FOREIGN KEY (callee) REFERENCES functions(name) ON DELETE CASCADE
	);
	`
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("init schema: %w", err)
	}

	return &Store{db: db}, nil
}

// Close closes the underlying database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

// SaveGraph writes the entire call-graph into the DB,
// wiping any previous contents.
func (s *Store) SaveGraph(graph map[string]callgraph.FunctionNode) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}

	// clear existing data
	if _, err := tx.Exec(`DELETE FROM calls`); err != nil {
		tx.Rollback()
		return err
	}
	if _, err := tx.Exec(`DELETE FROM functions`); err != nil {
		tx.Rollback()
		return err
	}

	// prepare statements
	insertFn, err := tx.Prepare(
		`INSERT INTO functions(name, signature, definition) VALUES(?,?,?)`,
	)
	if err != nil {
		tx.Rollback()
		return err
	}
	defer insertFn.Close()

	insertCall, err := tx.Prepare(
		`INSERT OR IGNORE INTO calls(caller, callee) VALUES(?,?)`,
	)
	if err != nil {
		tx.Rollback()
		return err
	}
	defer insertCall.Close()

	// 1) insert all function nodes
	for name, node := range graph {
		if _, err := insertFn.Exec(name, node.Signature, node.Definition); err != nil {
			tx.Rollback()
			return fmt.Errorf("insert function %s: %w", name, err)
		}
	}

	// 2) insert all call edges
	for caller, node := range graph {
		for _, callee := range node.Callees {
			if _, err := insertCall.Exec(caller, callee); err != nil {
				tx.Rollback()
				return fmt.Errorf("insert call %s→%s: %w", caller, callee, err)
			}
		}
	}

	return tx.Commit()
}

// LoadGraph reads back the call-graph from the DB into the same
// map[string]FunctionNode form.
func (s *Store) LoadGraph() (map[string]callgraph.FunctionNode, error) {
	// load all functions
	rows, err := s.db.Query(`SELECT name, signature, definition FROM functions`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	graph := make(map[string]callgraph.FunctionNode)
	for rows.Next() {
		var name, sig, def string
		if err := rows.Scan(&name, &sig, &def); err != nil {
			return nil, err
		}
		graph[name] = callgraph.FunctionNode{
			Signature:  sig,
			Definition: def,
			Callees:    []string{},
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// load edges
	edgeRows, err := s.db.Query(`SELECT caller, callee FROM calls`)
	if err != nil {
		return nil, err
	}
	defer edgeRows.Close()

	for edgeRows.Next() {
		var caller, callee string
		if err := edgeRows.Scan(&caller, &callee); err != nil {
			return nil, err
		}
		if node, ok := graph[caller]; ok {
			node.Callees = append(node.Callees, callee)
			graph[caller] = node
		}
	}
	if err := edgeRows.Err(); err != nil {
		return nil, err
	}

	return graph, nil
}
