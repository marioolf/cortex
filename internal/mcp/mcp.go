// Package mcp is a Model Context Protocol server over the vault: newline
// delimited JSON-RPC 2.0 on stdio, read-only, standard library only. It reads
// the notes straight from disk, so it works whether or not Cortex is running.
package mcp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"sync"
)

// legacyVersions are the revisions that start with an initialize handshake
// (https://modelcontextprotocol.io/specification/versioning).
var legacyVersions = []string{"2024-11-05", "2025-03-26", "2025-06-18", "2025-11-25"}

// modernVersion is the revision with no handshake: every request carries its
// version in _meta, and server/discover replaces initialize. We answer it too,
// so clients that probe with server/discover first find us.
const modernVersion = "2026-07-28"

const metaVersionKey = "io.modelcontextprotocol/protocolVersion"

// JSON-RPC and MCP error codes.
const (
	codeParse           = -32700
	codeInvalidRequest  = -32600
	codeMethodNotFound  = -32601
	codeInvalidParams   = -32602
	codeUnsupportedVers = -32022
)

// Server answers MCP requests about the notes under Vault.
type Server struct {
	Vault   string // vault directory; never created or written
	Version string // Cortex version, reported in serverInfo
	Log     *log.Logger

	idx *index
}

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
	Result  json.RawMessage `json:"result"`
	Error   json.RawMessage `json:"error"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// Serve reads one JSON message per line from in and writes replies to out
// until in ends. Nothing else is ever written to out.
func (s *Server) Serve(in io.Reader, out io.Writer) error {
	if s.Log == nil {
		s.Log = log.New(io.Discard, "", 0)
	}
	s.idx = newIndex(s.Vault)
	var mu sync.Mutex
	send := func(r response) {
		r.JSONRPC = "2.0"
		b, err := json.Marshal(r) // escapes newlines inside strings
		if err != nil {
			s.Log.Printf("cannot encode reply: %v", err)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		out.Write(append(b, '\n'))
	}

	rd := bufio.NewReaderSize(in, 64<<10)
	for {
		line, err := rd.ReadBytes('\n')
		if line = bytes.TrimSpace(line); len(line) > 0 {
			if r, ok := s.handle(line); ok {
				send(r)
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

var null = json.RawMessage("null")

// handle processes one message. ok is false when no reply is due.
func (s *Server) handle(line []byte) (r response, ok bool) {
	var req request
	if err := json.Unmarshal(line, &req); err != nil {
		var syn *json.SyntaxError
		if errors.As(err, &syn) || errors.Is(err, io.ErrUnexpectedEOF) {
			return fail(null, codeParse, "parse error"), true
		}
		return fail(null, codeInvalidRequest, "invalid request"), true
	}
	hasID := len(req.ID) > 0
	if req.Method == "" {
		if req.Result != nil || req.Error != nil {
			return response{}, false // a client reply to something we never asked
		}
		id := req.ID
		if !hasID {
			id = null
		}
		return fail(id, codeInvalidRequest, "invalid request"), true
	}
	if !hasID {
		// Notifications (initialized, cancelled, ...) never get a reply, known or not.
		return response{}, false
	}

	modern, err := s.checkModern(req.Params)
	if err != nil {
		return response{ID: req.ID, Error: err}, true
	}
	res, rerr := s.dispatch(req, modern)
	if rerr != nil {
		return response{ID: req.ID, Error: rerr}, true
	}
	if m, isMap := res.(map[string]any); isMap && modern {
		m["resultType"] = "complete"
	}
	return response{ID: req.ID, Result: res}, true
}

// checkModern reports whether the request uses the handshake-less revision
// (its _meta names a version) and rejects versions we do not speak.
func (s *Server) checkModern(params json.RawMessage) (bool, *rpcError) {
	var p struct {
		Meta map[string]json.RawMessage `json:"_meta"`
	}
	if json.Unmarshal(params, &p) != nil || p.Meta == nil {
		return false, nil
	}
	raw, ok := p.Meta[metaVersionKey]
	if !ok {
		return false, nil
	}
	var v string
	json.Unmarshal(raw, &v)
	if v != modernVersion {
		return true, &rpcError{codeUnsupportedVers, "Unsupported protocol version",
			map[string]any{"supported": []string{modernVersion}, "requested": v}}
	}
	return true, nil
}

func fail(id json.RawMessage, code int, msg string) response {
	return response{ID: id, Error: &rpcError{Code: code, Message: msg}}
}

func (s *Server) dispatch(req request, modern bool) (any, *rpcError) {
	switch req.Method {
	case "initialize":
		return s.initialize(req.Params)
	case "server/discover":
		return map[string]any{
			"supportedVersions": []string{modernVersion},
			"capabilities":      capabilities(),
			"_meta":             map[string]any{"io.modelcontextprotocol/serverInfo": s.serverInfo()},
			"instructions":      instructions,
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		return map[string]any{"tools": toolDefs()}, nil
	case "tools/call":
		return s.callTool(req.Params)
	}
	return nil, &rpcError{Code: codeMethodNotFound, Message: "method not found: " + req.Method}
}

func capabilities() map[string]any { return map[string]any{"tools": map[string]any{}} }

func (s *Server) serverInfo() map[string]any {
	return map[string]any{"name": "cortex", "version": s.Version}
}

func (s *Server) initialize(params json.RawMessage) (any, *rpcError) {
	var p struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: "invalid params: " + err.Error()}
	}
	// Echo the client's version if we speak it, else offer our latest.
	v := legacyVersions[len(legacyVersions)-1]
	for _, known := range legacyVersions {
		if p.ProtocolVersion == known {
			v = known
		}
	}
	return map[string]any{
		"protocolVersion": v,
		"capabilities":    capabilities(),
		"serverInfo":      s.serverInfo(),
		"instructions":    instructions,
	}, nil
}

func (s *Server) callTool(params json.RawMessage) (any, *rpcError) {
	var p struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: "invalid params: " + err.Error()}
	}
	var text string
	var err error
	switch p.Name {
	case "search":
		text, err = s.search(p.Arguments)
	case "read":
		text, err = s.read(p.Arguments)
	case "recent":
		text, err = s.recent(p.Arguments)
	default:
		return nil, &rpcError{Code: codeInvalidParams, Message: "unknown tool: " + p.Name}
	}
	if err != nil {
		// Errors the model can act on are results, not protocol errors.
		s.Log.Printf("tool %s: %v", p.Name, err)
		return map[string]any{"content": textContent(err.Error()), "isError": true}, nil
	}
	return map[string]any{"content": textContent(text), "isError": false}, nil
}

func textContent(s string) []map[string]any {
	return []map[string]any{{"type": "text", "text": s}}
}

// decodeArgs fills dst from the tool arguments; absent arguments are empty.
func decodeArgs(raw json.RawMessage, dst any) error {
	if len(bytes.TrimSpace(raw)) == 0 || string(bytes.TrimSpace(raw)) == "null" {
		return nil
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		return fmt.Errorf("invalid arguments: %v", err)
	}
	return nil
}
