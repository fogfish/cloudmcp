//
// Copyright (C) 2025 Dmitry Kolesnikov
//
// This file may be modified and distributed under the terms
// of the MIT license.  See the LICENSE file for details.
// https://github.com/fogfish/cloudmcp
//

package serve

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"runtime/debug"
	"sync"
	"weak"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// JSON-RPC call being served, filled by the guard, consumed by metrics.
type call struct {
	Method string
	Tool   string
}

type callKey struct{}

func callOf(ctx context.Context) *call {
	if c, ok := ctx.Value(callKey{}).(*call); ok {
		return c
	}
	return nil
}

// withGuard rejects requests the MCP server cannot serve with correct
// HTTP status and JSON-RPC error, rather than failing the function.
func (c *config) withGuard(next http.Handler) http.Handler {
	limit := c.http.MaxRequestBodyBytes
	if limit == 0 {
		limit = mcp.DefaultMaxRequestBodyBytes
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			// Stateless servers (Lambda) cannot open event streams (GET) or
			// terminate sessions (DELETE).
			if c.http.Stateless {
				w.Header().Set("Allow", http.MethodPost)
				writeError(w, http.StatusMethodNotAllowed, jsonrpc.CodeInvalidRequest, "method not allowed")
				return
			}
			next.ServeHTTP(w, r)
			return
		}

		if r.Body == nil {
			writeError(w, http.StatusBadRequest, jsonrpc.CodeParseError, "parse error: empty body")
			return
		}

		reader := io.Reader(r.Body)
		if limit > 0 {
			reader = io.LimitReader(r.Body, limit+1)
		}
		body, err := io.ReadAll(reader)
		r.Body.Close()
		if err != nil {
			writeError(w, http.StatusBadRequest, jsonrpc.CodeParseError, "parse error: unable to read body")
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))

		// too large requests are answered by the go-sdk (413)
		if limit > 0 && int64(len(body)) > limit {
			next.ServeHTTP(w, r)
			return
		}

		msg, err := jsonrpc.DecodeMessage(body)
		if err != nil {
			writeError(w, http.StatusBadRequest, jsonrpc.CodeParseError, "parse error")
			return
		}

		if req, ok := msg.(*jsonrpc.Request); ok {
			cc := callOf(r.Context())
			if cc != nil {
				cc.Method = req.Method
				if req.Method == "tools/call" {
					var params struct {
						Name string `json:"name"`
					}
					if json.Unmarshal(req.Params, &params) == nil {
						cc.Tool = params.Name
					}
				}
			}

			if c.debug {
				c.logger.DebugContext(r.Context(), "json-rpc request", "method", req.Method, "id", req.ID.Raw())
			}
		}

		next.ServeHTTP(w, r)
	})
}

// withRecover answers panics with HTTP 500 and JSON-RPC internal error.
func (c *config) withRecover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				if err == http.ErrAbortHandler {
					panic(err)
				}
				c.logger.ErrorContext(r.Context(), "panic serving mcp request",
					"err", fmt.Sprint(err),
					"stack", string(debug.Stack()),
				)
				writeError(w, http.StatusInternalServerError, jsonrpc.CodeInternalError, "internal error")
			}
		}()

		next.ServeHTTP(w, r)
	})
}

// withRecoverMCP installs receiving middleware recovering panics of the MCP
// server's handlers (e.g. tools), which the go-sdk runs in own goroutines.
// The middleware is installed once per server instance.
func (c *config) withRecoverMCP(f ServerFactory) ServerFactory {
	var seen sync.Map // weak.Pointer[mcp.Server] → struct{}

	middleware := func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (result mcp.Result, err error) {
			defer func() {
				if x := recover(); x != nil {
					c.logger.ErrorContext(ctx, "panic serving mcp method",
						"method", method,
						"err", fmt.Sprint(x),
						"stack", string(debug.Stack()),
					)
					result, err = nil, &jsonrpc.Error{Code: jsonrpc.CodeInternalError, Message: "internal error"}
				}
			}()
			return next(ctx, method, req)
		}
	}

	return func(r *http.Request) *mcp.Server {
		server := f(r)
		if server == nil {
			return nil
		}

		key := weak.Make(server)
		if _, loaded := seen.LoadOrStore(key, struct{}{}); !loaded {
			server.AddReceivingMiddleware(middleware)
			runtime.AddCleanup(server, func(k weak.Pointer[mcp.Server]) { seen.Delete(k) }, key)
		}

		return server
	}
}

// writeError writes JSON-RPC error response (id is null, the request id is
// unknown or the request is invalid).
func writeError(w http.ResponseWriter, status int, code int64, message string) {
	body, _ := json.Marshal(struct {
		JsonRPC string         `json:"jsonrpc"`
		ID      any            `json:"id"`
		Error   *jsonrpc.Error `json:"error"`
	}{
		JsonRPC: "2.0",
		Error:   &jsonrpc.Error{Code: code, Message: message},
	})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write(body)
}
