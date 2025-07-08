// pkg/lspclient/client.go
package lspclient

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"

	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"
)

// stdio wraps gopls stdio for JSON-RPC transport.
type stdio struct {
	in  io.WriteCloser
	out io.ReadCloser
}

func (s *stdio) Read(p []byte) (int, error)  { return s.out.Read(p) }
func (s *stdio) Write(p []byte) (int, error) { return s.in.Write(p) }
func (s *stdio) Close() error {
	_ = s.in.Close()
	return s.out.Close()
}

// Client manages the gopls subprocess and LSP connection.
type Client struct {
	ctx       context.Context
	cancel    context.CancelFunc
	rootDir   string
	stream    *stdio
	conn      jsonrpc2.Conn
	goplsCmd  *exec.Cmd
	connected bool
}

// New starts gopls and initializes an LSP session rooted at rootDir.
func New(rootDir string) (*Client, error) {
	absRoot, err := filepath.Abs(rootDir)
	if err != nil {
		return nil, fmt.Errorf("resolve root dir: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	stream, cmd, err := startGopls(ctx)
	if err != nil {
		cancel()
		return nil, err
	}

	conn := newConn(ctx, stream)
	if err := initialize(ctx, conn, absRoot); err != nil {
		_ = stream.Close()
		_ = cmd.Process.Kill()
		cancel()
		return nil, err
	}

	return &Client{
		ctx:       ctx,
		cancel:    cancel,
		rootDir:   absRoot,
		stream:    stream,
		conn:      conn,
		goplsCmd:  cmd,
		connected: true,
	}, nil
}

// Close terminates the gopls subprocess and frees resources.
func (c *Client) Close() {
	if !c.connected {
		return
	}
	c.connected = false
	_ = c.conn.Close()
	_ = c.stream.Close()
	_ = c.goplsCmd.Process.Kill()
	c.cancel()
}

// OpenDocument sends a textDocument/didOpen notification.
func (c *Client) OpenDocument(path string) error {
	src, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read file %s: %w", path, err)
	}
	uri := fileURI(path)
	params := protocol.DidOpenTextDocumentParams{
		TextDocument: protocol.TextDocumentItem{
			URI:        uri,
			LanguageID: "go",
			Version:    1,
			Text:       string(src),
		},
	}
	return c.conn.Notify(c.ctx, protocol.MethodTextDocumentDidOpen, params)
}

// FetchSymbols requests the document symbols.
func (c *Client) FetchSymbols(path string) ([]protocol.DocumentSymbol, error) {
	var symbols []protocol.DocumentSymbol
	params := protocol.DocumentSymbolParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: fileURI(path)},
	}
	if _, err := c.conn.Call(c.ctx, protocol.MethodTextDocumentDocumentSymbol, params, &symbols); err != nil {
		return nil, err
	}
	return symbols, nil
}

// PrepareCallHierarchy sends textDocument/prepareCallHierarchy.
func (c *Client) PrepareCallHierarchy(path string,
	pos protocol.Position,
) ([]protocol.CallHierarchyItem, error) {
	params := protocol.CallHierarchyPrepareParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: fileURI(path)},
			Position:     pos,
		},
	}
	var items []protocol.CallHierarchyItem
	if _, err := c.conn.Call(c.ctx,
		protocol.MethodTextDocumentPrepareCallHierarchy,
		params, &items,
	); err != nil {
		return nil, err
	}
	return items, nil
}

// IncomingCalls lists who calls the given item.
func (c *Client) IncomingCalls(
	item protocol.CallHierarchyItem,
) ([]protocol.CallHierarchyIncomingCall, error) {
	params := protocol.CallHierarchyIncomingCallsParams{Item: item}
	var calls []protocol.CallHierarchyIncomingCall
	if _, err := c.conn.Call(c.ctx,
		protocol.MethodCallHierarchyIncomingCalls,
		params, &calls,
	); err != nil {
		return nil, err
	}
	return calls, nil
}

// OutgoingCalls lists what the given item calls.
func (c *Client) OutgoingCalls(
	item protocol.CallHierarchyItem,
) ([]protocol.CallHierarchyOutgoingCall, error) {
	params := protocol.CallHierarchyOutgoingCallsParams{Item: item}
	var calls []protocol.CallHierarchyOutgoingCall
	if _, err := c.conn.Call(c.ctx,
		protocol.MethodCallHierarchyOutgoingCalls,
		params, &calls,
	); err != nil {
		return nil, err
	}
	return calls, nil
}

func initialize(ctx context.Context, conn jsonrpc2.Conn, rootDir string) error {
	caps := protocol.ClientCapabilities{
		Workspace: &protocol.WorkspaceClientCapabilities{
			ApplyEdit: true,
			WorkspaceEdit: &protocol.WorkspaceClientCapabilitiesWorkspaceEdit{
				DocumentChanges: true,
			},
			DidChangeConfiguration: &protocol.DidChangeConfigurationWorkspaceClientCapabilities{
				DynamicRegistration: true,
			},
			DidChangeWatchedFiles: &protocol.DidChangeWatchedFilesWorkspaceClientCapabilities{
				DynamicRegistration: true,
			},
		},
		TextDocument: &protocol.TextDocumentClientCapabilities{
			Synchronization: &protocol.TextDocumentSyncClientCapabilities{
				DynamicRegistration: true,
			},
			DocumentSymbol: &protocol.DocumentSymbolClientCapabilities{
				DynamicRegistration:               true,
				HierarchicalDocumentSymbolSupport: true,
				SymbolKind: &protocol.SymbolKindCapabilities{
					ValueSet: []protocol.SymbolKind{
						protocol.SymbolKindFunction,
						protocol.SymbolKindMethod,
					},
				},
			},
			CallHierarchy: &protocol.CallHierarchyClientCapabilities{
				DynamicRegistration: true,
			},
		},
	}

	params := protocol.InitializeParams{
		ProcessID:    int32(os.Getpid()),
		RootURI:      fileURI(rootDir),
		Capabilities: caps,
	}
	var result protocol.InitializeResult
	if _, err := conn.Call(ctx, protocol.MethodInitialize, params, &result); err != nil {
		return fmt.Errorf("initialize failed: %w", err)
	}
	if err := conn.Notify(ctx, protocol.MethodInitialized, protocol.InitializedParams{}); err != nil {
		return fmt.Errorf("initialized notification failed: %w", err)
	}
	return nil
}

func startGopls(ctx context.Context) (*stdio, *exec.Cmd, error) {
	cmd := exec.CommandContext(ctx, "gopls", "serve")
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, err
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, nil, err
	}
	log.Printf("[lspclient] gopls started (PID %d)", cmd.Process.Pid)
	return &stdio{in: in, out: out}, cmd, nil
}

func newConn(ctx context.Context, transport *stdio) jsonrpc2.Conn {
	stream := jsonrpc2.NewStream(transport)
	conn := jsonrpc2.NewConn(stream)
	conn.Go(ctx, jsonrpc2.MethodNotFoundHandler)
	return conn
}

func fileURI(path string) protocol.DocumentURI {
	abs, _ := filepath.Abs(path)
	return protocol.DocumentURI("file://" + filepath.ToSlash(abs))
}

func utilFunc() {

}
