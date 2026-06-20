// Package mcp implements an MCP (Model Context Protocol) server with SSE
// transport and JSON-RPC 2.0 dispatcher for the OMOTG Telegram bot.
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
)

// --- Public types ---

// ToolDefinition describes an MCP tool exposed by the server.
type ToolDefinition struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema InputSchema `json:"inputSchema"`
}

// InputSchema describes the JSON Schema for a tool's input.
type InputSchema struct {
	Type       string                    `json:"type"`
	Properties map[string]PropertySchema `json:"properties"`
	Required   []string                  `json:"required"`
}

// PropertySchema describes a single property in the input schema.
type PropertySchema struct {
	Type        string `json:"type"`
	Description string `json:"description"`
}

// HandlerFunc processes an MCP tool call and returns a text result.
type HandlerFunc func(ctx context.Context, args json.RawMessage) (string, error)

// --- Internal types ---

type registeredTool struct {
	def     ToolDefinition
	handler HandlerFunc
}

// Server implements an MCP server with SSE transport.
type Server struct {
	tools      []registeredTool
	mu         sync.RWMutex
	sseClients map[string]chan json.RawMessage
	sseMu      sync.Mutex
	sseSeq     int
	sseBaseURL string
}

// New creates a new MCP Server. baseURL is used to construct the message
// endpoint advertised to SSE clients (e.g. "http://localhost:9090").
func New(baseURL string) *Server {
	return &Server{
		sseClients: make(map[string]chan json.RawMessage),
		sseBaseURL: baseURL,
	}
}

// RegisterTool registers an MCP tool with its handler.
func (s *Server) RegisterTool(def ToolDefinition, handler HandlerFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tools = append(s.tools, registeredTool{def: def, handler: handler})
}

// Handler returns an http.Handler that routes MCP endpoints.
//
//	GET  /mcp/sse     — SSE stream for clients
//	POST /mcp/message — JSON-RPC 2.0 dispatcher
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /mcp/sse", s.handleSSE)
	mux.HandleFunc("POST /mcp/message", s.handleMessage)
	return mux
}

// ---------------------------------------------------------------------------
// SSE handler
// ---------------------------------------------------------------------------

func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	clientID := fmt.Sprintf("sse-%d", s.nextSeq())
	ch := make(chan json.RawMessage, 64)

	s.sseMu.Lock()
	s.sseClients[clientID] = ch
	s.sseMu.Unlock()

	slog.Debug("SSE client connected", "client_id", clientID)

	defer func() {
		s.sseMu.Lock()
		delete(s.sseClients, clientID)
		s.sseMu.Unlock()
		close(ch)
		slog.Debug("SSE client disconnected", "client_id", clientID)
	}()

	// Advertise the message endpoint.
	endpointData, _ := json.Marshal(map[string]string{
		"endpoint": s.sseBaseURL + "/mcp/message",
	})
	fmt.Fprintf(w, "event: endpoint\ndata: %s\n\n", endpointData)
	flusher.Flush()

	for {
		select {
		case <-r.Context().Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			fmt.Fprintf(w, "event: message\ndata: %s\n\n", msg)
			flusher.Flush()
		}
	}
}

func (s *Server) nextSeq() int {
	s.sseMu.Lock()
	defer s.sseMu.Unlock()
	s.sseSeq++
	return s.sseSeq
}

// ---------------------------------------------------------------------------
// JSON-RPC 2.0 types (internal)
// ---------------------------------------------------------------------------

type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.Number     `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type jsonRPCResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      json.Number `json:"id"`
	Result  interface{} `json:"result,omitempty"`
	Error   *rpcError   `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// --- MCP protocol types ---

type initializeResult struct {
	ProtocolVersion string             `json:"protocolVersion"`
	Capabilities    serverCapabilities `json:"capabilities"`
	ServerInfo      serverInfo         `json:"serverInfo"`
}

type serverCapabilities struct {
	Tools *toolsCapability `json:"tools,omitempty"`
}

type toolsCapability struct {
	ListChanged bool `json:"listChanged"`
}

type serverInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type toolCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type toolCallResult struct {
	Content []toolContent `json:"content"`
	IsError bool          `json:"isError"`
}

type toolContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// ---------------------------------------------------------------------------
// Message handler
// ---------------------------------------------------------------------------

func (s *Server) handleMessage(w http.ResponseWriter, r *http.Request) {
	var req jsonRPCRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		slog.Warn("failed to decode JSON-RPC request", "error", err)
		s.jsonRPCError(w, r, "", -32700, "Parse error")
		return
	}

	slog.Debug("MCP message", "method", req.Method, "id", req.ID)

	switch req.Method {
	case "initialize":
		s.respond(w, r, jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: initializeResult{
				ProtocolVersion: "2024-11-05",
				Capabilities: serverCapabilities{
					Tools: &toolsCapability{ListChanged: false},
				},
				ServerInfo: serverInfo{
					Name:    "omotg-telegram",
					Version: "1.0.0",
				},
			},
		})

	case "notifications/initialized":
		// Notifications have no ID — just acknowledge the HTTP request.
		acceptAccepted(w)
		return

	case "tools/list":
		s.mu.RLock()
		defs := make([]ToolDefinition, len(s.tools))
		for i, t := range s.tools {
			defs[i] = t.def
		}
		s.mu.RUnlock()

		s.respond(w, r, jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]any{
				"tools": defs,
			},
		})

	case "tools/call":
		var params toolCallParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			s.respond(w, r, jsonRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error:   &rpcError{Code: -32602, Message: "Invalid params"},
			})
			return
		}

		s.mu.RLock()
		var handler HandlerFunc
		for _, t := range s.tools {
			if t.def.Name == params.Name {
				handler = t.handler
				break
			}
		}
		s.mu.RUnlock()

		if handler == nil {
			s.respond(w, r, jsonRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error:   &rpcError{Code: -32602, Message: fmt.Sprintf("Unknown tool: %s", params.Name)},
			})
			return
		}

		result, err := handler(r.Context(), params.Arguments)
		if err != nil {
			result = err.Error()
		}
		s.respond(w, r, jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: toolCallResult{
				Content: []toolContent{{Type: "text", Text: result}},
				IsError: err != nil,
			},
		})

	default:
		s.respond(w, r, jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &rpcError{Code: -32601, Message: "Method not found"},
		})
	}
}

// ---------------------------------------------------------------------------
// Response helpers
// ---------------------------------------------------------------------------

// respond marshals the JSON-RPC response, broadcasts it to all SSE clients,
// and replies to the HTTP request with 202 Accepted.
func (s *Server) respond(w http.ResponseWriter, _ *http.Request, resp jsonRPCResponse) {
	data, err := json.Marshal(resp)
	if err != nil {
		slog.Error("failed to marshal JSON-RPC response", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Broadcast the response to every connected SSE client.
	s.sseMu.Lock()
	for _, ch := range s.sseClients {
		select {
		case ch <- data:
		default:
			// Client too slow — drop the event.
		}
	}
	s.sseMu.Unlock()

	acceptAccepted(w)
}

// jsonRPCError is a convenience for respond with an error body.
func (s *Server) jsonRPCError(w http.ResponseWriter, r *http.Request, id json.Number, code int, message string) {
	s.respond(w, r, jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &rpcError{Code: code, Message: message},
	})
}

// acceptAccepted writes a 202 Accepted HTTP response.
func acceptAccepted(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]string{"status": "accepted"})
}
