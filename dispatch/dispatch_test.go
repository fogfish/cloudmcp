//
// Copyright (C) 2025 Dmitry Kolesnikov
//
// This file may be modified and distributed under the terms
// of the MIT license.  See the LICENSE file for details.
// https://github.com/fogfish/cloudmcp
//

package dispatch_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/fogfish/cloudmcp/dispatch"
	"github.com/fogfish/cloudmcp/serve"
	"github.com/fogfish/it/v2"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type Input struct {
	Name string `json:"name"`
}

type Output struct {
	Text string `json:"text"`
}

// workers are Lambda handlers invoked in-process
type invoker map[string]serve.LambdaHandler

func (inv invoker) Invoke(ctx context.Context, in *lambda.InvokeInput, _ ...func(*lambda.Options)) (*lambda.InvokeOutput, error) {
	h, has := inv[aws.ToString(in.FunctionName)]
	if !has {
		return nil, fmt.Errorf("function not found")
	}
	out, err := h(ctx, in.Payload)
	if err != nil {
		return &lambda.InvokeOutput{FunctionError: aws.String("Unhandled"), Payload: []byte(err.Error())}, nil
	}
	b, err := json.Marshal(out)
	return &lambda.InvokeOutput{Payload: b}, err
}

func hello(_ context.Context, req *mcp.CallToolRequest, in Input) (*mcp.CallToolResult, Output, error) {
	who := ""
	if req.Extra.TokenInfo != nil {
		who = req.Extra.TokenInfo.UserID
	}
	return nil, Output{Text: "hello " + in.Name + " from " + who}, nil
}

func bye(_ context.Context, _ *mcp.CallToolRequest, in Input) (*mcp.CallToolResult, Output, error) {
	return nil, Output{}, fmt.Errorf("bye %s", in.Name)
}

func workers() invoker {
	return invoker{
		"hello": serve.Handler(serve.Tool(&mcp.Tool{Name: "hello"}, hello)),
		"bye":   serve.Handler(serve.Tool(&mcp.Tool{Name: "bye"}, bye)),
	}
}

func call(t *testing.T, h serve.LambdaHandler, body string) map[string]any {
	t.Helper()
	return callWith(t, h, map[string]string{"mcp-protocol-version": "2025-06-18"}, body)
}

func callWith(t *testing.T, h serve.LambdaHandler, headers map[string]string, body string) map[string]any {
	t.Helper()
	head := map[string]string{
		"content-type": "application/json",
		"accept":       "application/json, text/event-stream",
	}
	for k, v := range headers {
		head[k] = v
	}
	evt, _ := json.Marshal(events.APIGatewayV2HTTPRequest{
		Version: "2.0",
		RawPath: "/mcp",
		Headers: head,
		Body:    body,
		RequestContext: events.APIGatewayV2HTTPRequestContext{
			HTTP: events.APIGatewayV2HTTPRequestContextHTTPDescription{Method: "POST"},
			Authorizer: &events.APIGatewayV2HTTPRequestContextAuthorizerDescription{
				Lambda: map[string]any{"principal": "caller"},
			},
		},
	})

	out, err := h(context.Background(), evt)
	if err != nil {
		t.Fatal(err)
	}
	var reply map[string]any
	if err := json.Unmarshal([]byte(out.(*events.APIGatewayV2HTTPResponse).Body), &reply); err != nil {
		t.Fatal(err)
	}
	return reply
}

func TestDispatcher(t *testing.T) {
	server, err := dispatch.New(context.Background(),
		dispatch.Config{
			Implementation: &mcp.Implementation{Name: "dispatcher", Version: "v0.0.0"},
			Workers:        []string{"hello", " bye", ""},
			Client:         workers(),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	h := serve.Handler(serve.Static(server))

	t.Run("List", func(t *testing.T) {
		reply := call(t, h, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
		tools := reply["result"].(map[string]any)["tools"].([]any)
		it.Then(t).Should(it.Equal(len(tools), 2))
	})

	t.Run("Call", func(t *testing.T) {
		reply := call(t, h, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"hello","arguments":{"name":"world"}}}`)
		result := reply["result"].(map[string]any)
		it.Then(t).Should(
			it.Equal(result["structuredContent"].(map[string]any)["text"].(string), "hello world from caller"),
		)
	})

	t.Run("ToolError", func(t *testing.T) {
		reply := call(t, h, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"bye","arguments":{"name":"world"}}}`)
		result := reply["result"].(map[string]any)
		it.Then(t).Should(it.Equal(result["isError"].(bool), true))
	})

	// Protocol 2026-07-28 requires standard headers (Mcp-Method, Mcp-Name),
	// the worker validates them as well.
	t.Run("CallStandardHeaders", func(t *testing.T) {
		reply := callWith(t, h,
			map[string]string{
				"mcp-protocol-version": "2026-07-28",
				"mcp-method":           "tools/call",
				"mcp-name":             "hello",
			},
			`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"hello","arguments":{"name":"world"},"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}}}`,
		)
		it.Then(t).Should(it.Nil(reply["error"]))
		result := reply["result"].(map[string]any)
		it.Then(t).Should(
			it.Equal(result["structuredContent"].(map[string]any)["text"].(string), "hello world from caller"),
		)

		// workers are hidden, the dispatcher identifies itself
		info := result["_meta"].(map[string]any)[mcp.MetaKeyServerInfo].(map[string]any)
		it.Then(t).Should(it.Equal(info["name"].(string), "dispatcher"))
	})

	t.Run("InvalidParams", func(t *testing.T) {
		reply := call(t, h, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"hello","arguments":{"name":1}}}`)
		it.Then(t).Should(it.True(reply["error"] != nil || reply["result"].(map[string]any)["isError"] == true))
	})
}

func TestDispatcherDuplicateTool(t *testing.T) {
	inv := workers()
	inv["hello2"] = inv["hello"]

	_, err := dispatch.New(context.Background(),
		dispatch.Config{
			Implementation: &mcp.Implementation{Name: "dispatcher", Version: "v0.0.0"},
			Workers:        []string{"hello", "hello2"},
			Client:         inv,
		},
	)
	it.Then(t).ShouldNot(it.Nil(err))
}

func TestDispatcherWorkerNotFound(t *testing.T) {
	_, err := dispatch.New(context.Background(),
		dispatch.Config{
			Implementation: &mcp.Implementation{Name: "dispatcher", Version: "v0.0.0"},
			Workers:        []string{"unknown"},
			Client:         workers(),
		},
	)
	it.Then(t).ShouldNot(it.Nil(err))
}
