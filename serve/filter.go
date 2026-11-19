//
// Copyright (C) 2025 Dmitry Kolesnikov
//
// This file may be modified and distributed under the terms
// of the MIT license.  See the LICENSE file for details.
// https://github.com/fogfish/cloudmcp
//

package serve

import (
	"context"
	"fmt"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Filter chooses tools of the server per request. The server is built once,
// with all tools, the predicate decides which tools the caller sees in
// tools/list and may call with tools/call. The predicate receives go-sdk's
// TokenInfo of the request (nil if none).
//
//	readonly := func(t *mcp.Tool, info *auth.TokenInfo) bool {
//		if info != nil && slices.Contains(info.Scopes, "write") {
//			return true
//		}
//		return t.Annotations != nil && t.Annotations.ReadOnlyHint
//	}
//
//	lambda.Start(serve.Handler(serve.Filter(server, readonly)))
//
// Calls of hidden tools fail as calls of unknown tools. Filter installs
// receiving middleware to the server, call it once per server.
func Filter(full *mcp.Server, allow func(*mcp.Tool, *auth.TokenInfo) bool) ServerFactory {
	f := &filter{allow: allow}
	full.AddReceivingMiddleware(f.middleware)
	return Static(full)
}

type filter struct {
	allow func(*mcp.Tool, *auth.TokenInfo) bool
	tools sync.Map // name → *mcp.Tool
}

func (f *filter) middleware(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		switch method {
		case "tools/list":
			result, err := next(ctx, method, req)
			if err != nil {
				return result, err
			}
			list, ok := result.(*mcp.ListToolsResult)
			if !ok {
				return result, err
			}

			info := tokenInfo(req)
			tools := make([]*mcp.Tool, 0, len(list.Tools))
			for _, t := range list.Tools {
				f.tools.Store(t.Name, t)
				if f.allow(t, info) {
					tools = append(tools, t)
				}
			}
			list.Tools = tools
			return list, nil

		case "tools/call":
			call, ok := req.(*mcp.CallToolRequest)
			if !ok || call.Params == nil {
				return next(ctx, method, req)
			}

			tool, err := f.lookup(ctx, next, call)
			if err != nil {
				return nil, err
			}
			if tool == nil || !f.allow(tool, tokenInfo(req)) {
				return nil, &jsonrpc.Error{
					Code:    jsonrpc.CodeInvalidParams,
					Message: fmt.Sprintf("unknown tool %q", call.Params.Name),
				}
			}
			return next(ctx, method, req)

		default:
			return next(ctx, method, req)
		}
	}
}

// lookup tool definition, the cache is refreshed from the server on miss
func (f *filter) lookup(ctx context.Context, next mcp.MethodHandler, call *mcp.CallToolRequest) (*mcp.Tool, error) {
	if t, ok := f.tools.Load(call.Params.Name); ok {
		return t.(*mcp.Tool), nil
	}

	cursor := ""
	for {
		result, err := next(ctx, "tools/list",
			&mcp.ListToolsRequest{
				Session: call.Session,
				Params:  &mcp.ListToolsParams{Cursor: cursor},
				Extra:   call.Extra,
			},
		)
		if err != nil {
			return nil, err
		}

		list, ok := result.(*mcp.ListToolsResult)
		if !ok {
			return nil, nil
		}
		for _, t := range list.Tools {
			f.tools.Store(t.Name, t)
		}

		if list.NextCursor == "" {
			break
		}
		cursor = list.NextCursor
	}

	if t, ok := f.tools.Load(call.Params.Name); ok {
		return t.(*mcp.Tool), nil
	}
	return nil, nil
}

func tokenInfo(req mcp.Request) *auth.TokenInfo {
	if extra := req.GetExtra(); extra != nil {
		return extra.TokenInfo
	}
	return nil
}
