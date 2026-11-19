//
// Copyright (C) 2025 Dmitry Kolesnikov
//
// This file may be modified and distributed under the terms
// of the MIT license.  See the LICENSE file for details.
// https://github.com/fogfish/cloudmcp
//

// Package dispatch aggregates tools of several worker Lambda functions into
// one MCP server. The dispatcher lists tools of workers once (at cold start)
// and forwards tools/call to the worker owning the tool through Lambda Invoke,
// together with the caller's TokenInfo. Each worker keeps its own IAM role
// (least privilege per tool), clients connect to the single endpoint.
//
// Workers are Lambda functions running serve.Handler, e.g. serve.Tool.
package dispatch

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/fogfish/cloudmcp/serve"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Environment variables of the dispatcher function (cmd/dispatcher).
const (
	// Comma separated names (or ARNs) of worker functions
	EnvWorkers = "CLOUDMCP_WORKERS"

	// Implementation name and version reported to clients
	EnvName    = "CLOUDMCP_NAME"
	EnvVersion = "CLOUDMCP_VERSION"

	// CloudWatch namespace of metrics, metrics are disabled if empty
	EnvMetrics = "CLOUDMCP_METRICS"
)

// Invoker is the subset of Lambda client used by the dispatcher.
type Invoker interface {
	Invoke(context.Context, *lambda.InvokeInput, ...func(*lambda.Options)) (*lambda.InvokeOutput, error)
}

// Config of the dispatcher.
type Config struct {
	// Implementation reported to clients (required)
	Implementation *mcp.Implementation

	// Options of the MCP server
	Options *mcp.ServerOptions

	// Names (or ARNs) of worker functions
	Workers []string

	// Lambda client
	Client Invoker
}

// New lists tools of all workers and returns the MCP server forwarding
// tool calls to workers. Call it at cold start, the tools are listed once.
// Tool names must be unique across workers.
//
// Workers list their tools without the caller's identity (TokenInfo is nil),
// use serve.Filter at the dispatcher to choose tools per caller.
func New(ctx context.Context, cfg Config) (*mcp.Server, error) {
	if cfg.Client == nil {
		return nil, fmt.Errorf("dispatch: lambda client is required")
	}
	if cfg.Implementation == nil {
		return nil, fmt.Errorf("dispatch: implementation is required")
	}

	type listing struct {
		worker string
		tools  []*mcp.Tool
		err    error
	}

	workers := make([]string, 0, len(cfg.Workers))
	for _, w := range cfg.Workers {
		if w = strings.TrimSpace(w); w != "" {
			workers = append(workers, w)
		}
	}

	listings := make([]listing, len(workers))
	var wg sync.WaitGroup
	for i, w := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tools, err := listTools(ctx, cfg.Client, w)
			listings[i] = listing{worker: w, tools: tools, err: err}
		}()
	}
	wg.Wait()

	server := mcp.NewServer(cfg.Implementation, cfg.Options)
	owner := map[string]string{}
	for _, l := range listings {
		if l.err != nil {
			return nil, fmt.Errorf("dispatch: worker %s: %w", l.worker, l.err)
		}
		for _, t := range l.tools {
			if w, has := owner[t.Name]; has {
				return nil, fmt.Errorf("dispatch: tool %q is defined by workers %s and %s", t.Name, w, l.worker)
			}
			owner[t.Name] = l.worker
			server.AddTool(t, forward(cfg.Client, l.worker))
		}
	}

	return server, nil
}

func listTools(ctx context.Context, client Invoker, worker string) ([]*mcp.Tool, error) {
	var tools []*mcp.Tool
	cursor := ""
	for i := 1; ; i++ {
		params, err := json.Marshal(&mcp.ListToolsParams{Cursor: cursor})
		if err != nil {
			return nil, err
		}

		raw, err := invoke(ctx, client, worker, i, "tools/list", params, serve.Invocation{})
		if err != nil {
			return nil, err
		}

		var list mcp.ListToolsResult
		if err := json.Unmarshal(raw, &list); err != nil {
			return nil, err
		}
		tools = append(tools, list.Tools...)

		if list.NextCursor == "" {
			return tools, nil
		}
		cursor = list.NextCursor
	}
}

func forward(client Invoker, worker string) mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		params, err := json.Marshal(req.Params)
		if err != nil {
			return nil, err
		}

		inv := serve.Invocation{}
		if req.Extra != nil {
			inv.TokenInfo = req.Extra.TokenInfo
			if req.Extra.Header != nil {
				inv.ProtocolVersion = req.Extra.Header.Get("Mcp-Protocol-Version")
				for k := range req.Extra.Header {
					if serve.IsStandardHeader(k) {
						if inv.Header == nil {
							inv.Header = map[string]string{}
						}
						inv.Header[k] = req.Extra.Header.Get(k)
					}
				}
			}
		}

		raw, err := invoke(ctx, client, worker, 1, "tools/call", params, inv)
		if err != nil {
			return nil, err
		}

		var result mcp.CallToolResult
		if err := json.Unmarshal(raw, &result); err != nil {
			return nil, err
		}

		// The dispatcher is the server, workers are internal infrastructure.
		// Drop the worker's identity, the dispatcher annotates its own.
		if result.Meta != nil {
			delete(result.Meta, mcp.MetaKeyServerInfo)
			if len(result.Meta) == 0 {
				result.Meta = nil
			}
		}
		return &result, nil
	}
}

// invoke the worker with JSON-RPC request, returns the result
func invoke(
	ctx context.Context,
	client Invoker,
	worker string,
	seq int,
	method string,
	params json.RawMessage,
	inv serve.Invocation,
) (json.RawMessage, error) {
	id, err := jsonrpc.MakeID(float64(seq))
	if err != nil {
		return nil, err
	}

	msg, err := jsonrpc.EncodeMessage(&jsonrpc.Request{ID: id, Method: method, Params: params})
	if err != nil {
		return nil, err
	}
	inv.Message = msg

	payload, err := serve.NewInvocation(inv)
	if err != nil {
		return nil, err
	}

	out, err := client.Invoke(ctx, &lambda.InvokeInput{
		FunctionName: aws.String(worker),
		Payload:      payload,
	})
	if err != nil {
		return nil, err
	}
	if out.FunctionError != nil {
		return nil, fmt.Errorf("worker %s failed: %s: %s", worker, aws.ToString(out.FunctionError), string(out.Payload))
	}

	var resp struct {
		Result json.RawMessage `json:"result"`
		Error  *jsonrpc.Error  `json:"error"`
	}
	if err := json.Unmarshal(out.Payload, &resp); err != nil {
		return nil, fmt.Errorf("worker %s: invalid response: %w", worker, err)
	}
	if resp.Error != nil {
		return nil, resp.Error
	}

	return resp.Result, nil
}
