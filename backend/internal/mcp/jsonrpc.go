package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

// JSON-RPC 2.0 (https://www.jsonrpc.org/specification), in the subset MCP
// uses, hand-rolled for IMPLEMENTATION_PLAN §1.1 (no new module).
//
// ⛔ NO BATCHES. JSON-RPC 2.0 allows an array body; MCP 2025-03-26 adopted
// that and 2025-06-18 REMOVED it ("Remove support for JSON-RPC batching"), and
// both protocol versions this server negotiates are 2025-06-18 or later. An
// array is answered with one Invalid Request error, id null — never partly
// executed, because a batch that mixed a commit with a read and failed halfway
// would leave the client unable to tell which half ran.

// Error codes. The -32000..-32099 band is JSON-RPC's "implementation-defined
// server errors"; ours are there.
const (
	CodeParseError     = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternalError  = -32603

	// CodeManifestChanged: the tool manifest this session pinned at
	// initialize is no longer the one the server would list. Re-initialize.
	CodeManifestChanged = -32001
	// CodeRunEnded: the session's agent run has finished (completed, failed,
	// expired or cancelled); nothing more can happen in it.
	CodeRunEnded = -32002
)

// rpcError is a JSON-RPC error object.
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func (e *rpcError) Error() string { return fmt.Sprintf("jsonrpc %d: %s", e.Code, e.Message) }

// message is any JSON-RPC message a client may send: a request (method + id),
// a notification (method, no id) or a response (result|error + id, no method).
type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   json.RawMessage `json:"error,omitempty"`
}

func (m *message) isRequest() bool      { return m.Method != "" && len(m.ID) > 0 }
func (m *message) isNotification() bool { return m.Method != "" && len(m.ID) == 0 }
func (m *message) isResponse() bool {
	return m.Method == "" && len(m.ID) > 0 && (len(m.Result) > 0 || len(m.Error) > 0)
}

// response is what the server sends back.
type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

var nullID = json.RawMessage("null")

func errResponse(id json.RawMessage, code int, msg string) response {
	if len(id) == 0 {
		id = nullID
	}
	return response{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: msg}}
}

func okResponse(id json.RawMessage, result any) response {
	return response{JSONRPC: "2.0", ID: id, Result: result}
}

var errBatch = errors.New("mcp: JSON-RPC batches are not supported")

// parseMessage decodes exactly one JSON-RPC message. The error is the
// response to send (with id null when the id could not be read).
func parseMessage(body []byte) (*message, *response) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		r := errResponse(nil, CodeInvalidRequest, "empty body")
		return nil, &r
	}
	if trimmed[0] == '[' {
		// Still reject malformed JSON as a parse error first, as JSON-RPC
		// orders the checks.
		if !json.Valid(trimmed) {
			r := errResponse(nil, CodeParseError, "parse error")
			return nil, &r
		}
		r := errResponse(nil, CodeInvalidRequest, errBatch.Error()+" (MCP 2025-06-18 removed batching)")
		return nil, &r
	}
	if !json.Valid(trimmed) {
		r := errResponse(nil, CodeParseError, "parse error")
		return nil, &r
	}
	if trimmed[0] != '{' {
		r := errResponse(nil, CodeInvalidRequest, "a JSON-RPC message is an object")
		return nil, &r
	}
	var m message
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	if err := dec.Decode(&m); err != nil {
		r := errResponse(nil, CodeInvalidRequest, "not a JSON-RPC message: "+err.Error())
		return nil, &r
	}
	id := m.ID
	if err := validID(m.ID); err != nil {
		r := errResponse(nil, CodeInvalidRequest, err.Error())
		return nil, &r
	}
	if m.JSONRPC != "2.0" {
		r := errResponse(id, CodeInvalidRequest, `jsonrpc must be "2.0"`)
		return nil, &r
	}
	if !m.isRequest() && !m.isNotification() && !m.isResponse() {
		r := errResponse(id, CodeInvalidRequest, "neither a request, a notification nor a response")
		return nil, &r
	}
	return &m, nil
}

// validID enforces MCP's rule on request ids: a string or an integer, never
// null (MCP narrows JSON-RPC here), never a fraction.
func validID(id json.RawMessage) error {
	if len(id) == 0 {
		return nil // a notification
	}
	switch id[0] {
	case '"':
		var s string
		if json.Unmarshal(id, &s) != nil {
			return errors.New("id must be a string or an integer")
		}
		return nil
	case 'n':
		return errors.New("id must not be null")
	default:
		var n json.Number
		if json.Unmarshal(id, &n) != nil {
			return errors.New("id must be a string or an integer")
		}
		if _, err := n.Int64(); err != nil {
			return errors.New("id must be a string or an integer")
		}
		return nil
	}
}
