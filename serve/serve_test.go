//
// Copyright (C) 2025 Dmitry Kolesnikov
//
// This file may be modified and distributed under the terms
// of the MIT license.  See the LICENSE file for details.
// https://github.com/fogfish/cloudmcp
//

package serve_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/fogfish/cloudmcp/serve"
	"github.com/fogfish/it/v2"
	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
)

//------------------------------------------------------------------------------
// fixtures
//------------------------------------------------------------------------------

type None struct{}

type WhoAmI struct {
	User          string            `json:"user"`
	Scopes        []string          `json:"scopes,omitempty"`
	Path          map[string]string `json:"path,omitempty"`
	Extra         map[string]string `json:"extra"`
	Authorization string            `json:"authorization"`
	HasTokenInfo  bool              `json:"hasTokenInfo"`
}

func whoami(_ context.Context, req *mcp.CallToolRequest, _ None) (*mcp.CallToolResult, WhoAmI, error) {
	out := WhoAmI{Extra: map[string]string{}}
	if req.Extra.Header != nil {
		out.Authorization = req.Extra.Header.Get("Authorization")
	}
	if info := req.Extra.TokenInfo; info != nil {
		out.HasTokenInfo = true
		out.User = info.UserID
		out.Scopes = info.Scopes
		switch path := info.Extra["path"].(type) {
		case map[string]string:
			out.Path = path
		case map[string]any:
			out.Path = map[string]string{}
			for k, v := range path {
				out.Path[k] = fmt.Sprint(v)
			}
		}
		for k, v := range info.Extra {
			if k != "path" {
				out.Extra[k] = fmt.Sprint(v)
			}
		}
	}
	return nil, out, nil
}

func write(context.Context, *mcp.CallToolRequest, None) (*mcp.CallToolResult, None, error) {
	return nil, None{}, nil
}

func newServer() *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "v0.0.0"}, nil)
	mcp.AddTool(server,
		&mcp.Tool{Name: "whoami", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}},
		whoami,
	)
	mcp.AddTool(server, &mcp.Tool{Name: "write"}, write)
	return server
}

const protocol = "2025-06-18"

func rpc(method string, params any) string {
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	return string(b)
}

func callTool(name string) string {
	return rpc("tools/call", map[string]any{"name": name, "arguments": map[string]any{}})
}

func mcpHeaders() map[string]string {
	return map[string]string{
		"content-type":         "application/json",
		"accept":               "application/json, text/event-stream",
		"mcp-protocol-version": protocol,
		"host":                 "mcp.example.com",
	}
}

func eventV2(method, body string, mutate func(*events.APIGatewayV2HTTPRequest)) json.RawMessage {
	evt := events.APIGatewayV2HTTPRequest{
		Version:  "2.0",
		RawPath:  "/mcp",
		Headers:  mcpHeaders(),
		Body:     body,
		RouteKey: method + " /mcp",
		RequestContext: events.APIGatewayV2HTTPRequestContext{
			HTTP: events.APIGatewayV2HTTPRequestContextHTTPDescription{Method: method, Path: "/mcp"},
		},
	}
	if mutate != nil {
		mutate(&evt)
	}
	b, _ := json.Marshal(evt)
	return b
}

func eventV1(method, body string, mutate func(*events.APIGatewayProxyRequest)) json.RawMessage {
	evt := events.APIGatewayProxyRequest{
		HTTPMethod: method,
		Path:       "/mcp",
		Headers:    mcpHeaders(),
		Body:       body,
	}
	if mutate != nil {
		mutate(&evt)
	}
	b, _ := json.Marshal(evt)
	return b
}

type rpcReply struct {
	Result struct {
		StructuredContent json.RawMessage `json:"structuredContent"`
		Tools             []struct {
			Name string `json:"name"`
		} `json:"tools"`
	} `json:"result"`
	Error *struct {
		Code    int64  `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func parseReply(t *testing.T, body string) rpcReply {
	t.Helper()
	var reply rpcReply
	if err := json.Unmarshal([]byte(body), &reply); err != nil {
		t.Fatalf("invalid reply %q: %v", body, err)
	}
	return reply
}

func whoamiOf(t *testing.T, body string) WhoAmI {
	t.Helper()
	reply := parseReply(t, body)
	if reply.Error != nil {
		t.Fatalf("unexpected error: %+v", reply.Error)
	}
	var out WhoAmI
	if err := json.Unmarshal(reply.Result.StructuredContent, &out); err != nil {
		t.Fatalf("invalid structured content %s: %v", reply.Result.StructuredContent, err)
	}
	return out
}

func serveV2(t *testing.T, h serve.LambdaHandler, evt json.RawMessage) *events.APIGatewayV2HTTPResponse {
	t.Helper()
	out, err := h(context.Background(), evt)
	if err != nil {
		t.Fatalf("handler failed: %v", err)
	}
	resp, ok := out.(*events.APIGatewayV2HTTPResponse)
	if !ok {
		t.Fatalf("unexpected response type %T", out)
	}
	return resp
}

func serveV1(t *testing.T, h serve.LambdaHandler, evt json.RawMessage) *events.APIGatewayProxyResponse {
	t.Helper()
	out, err := h(context.Background(), evt)
	if err != nil {
		t.Fatalf("handler failed: %v", err)
	}
	resp, ok := out.(*events.APIGatewayProxyResponse)
	if !ok {
		t.Fatalf("unexpected response type %T", out)
	}
	return resp
}

//------------------------------------------------------------------------------
// payload 2.0
//------------------------------------------------------------------------------

func TestV2LambdaAuthorizer(t *testing.T) {
	h := serve.Handler(serve.Static(newServer()))

	resp := serveV2(t, h, eventV2("POST", callTool("whoami"), func(evt *events.APIGatewayV2HTTPRequest) {
		evt.Headers["authorization"] = "Basic YWNjZXNzOnNlY3JldA=="
		evt.RawPath = "/i-1"
		evt.PathParameters = map[string]string{"instance": "i-1"}
		evt.RequestContext.Authorizer = &events.APIGatewayV2HTTPRequestContextAuthorizerDescription{
			Lambda: map[string]any{"principal": "u-1", "scope": "read write", "instance": "i-1", "decision": "allow"},
		}
	}))

	out := whoamiOf(t, resp.Body)
	it.Then(t).
		Should(it.Equal(resp.StatusCode, 200)).
		Should(it.Equal(out.User, "u-1")).
		Should(it.Seq(out.Scopes).Equal("read", "write")).
		Should(it.Equal(out.Path["instance"], "i-1")).
		Should(it.Equal(out.Extra["instance"], "i-1")).
		Should(it.Equal(out.Extra["decision"], "allow")).
		// tools see the client's own Authorization header
		Should(it.Equal(out.Authorization, "Basic YWNjZXNzOnNlY3JldA==")).
		Should(it.String(resp.Headers["Content-Type"]).Contain("application/json"))
}

func TestV2JWTAuthorizer(t *testing.T) {
	h := serve.Handler(serve.Static(newServer()))

	resp := serveV2(t, h, eventV2("POST", callTool("whoami"), func(evt *events.APIGatewayV2HTTPRequest) {
		evt.Headers["authorization"] = "Bearer eyJ"
		evt.RequestContext.Authorizer = &events.APIGatewayV2HTTPRequestContextAuthorizerDescription{
			JWT: &events.APIGatewayV2HTTPRequestContextAuthorizerJWTDescription{
				Claims: map[string]string{"sub": "jwt-1"},
				Scopes: []string{"mcp:read"},
			},
		}
	}))

	out := whoamiOf(t, resp.Body)
	it.Then(t).
		Should(it.Equal(out.User, "jwt-1")).
		Should(it.Seq(out.Scopes).Equal("mcp:read")).
		Should(it.Equal(out.Authorization, "Bearer eyJ"))
}

func TestV2Anonymous(t *testing.T) {
	h := serve.Handler(serve.Static(newServer()))

	resp := serveV2(t, h, eventV2("POST", callTool("whoami"), func(evt *events.APIGatewayV2HTTPRequest) {
		evt.PathParameters = map[string]string{"instance": "i-2"}
	}))

	out := whoamiOf(t, resp.Body)
	it.Then(t).
		Should(it.True(out.HasTokenInfo)).
		Should(it.Equal(out.User, "")).
		Should(it.Equal(out.Path["instance"], "i-2")).
		Should(it.Equal(out.Authorization, ""))
}

func TestV2Base64Body(t *testing.T) {
	h := serve.Handler(serve.Static(newServer()))

	resp := serveV2(t, h, eventV2("POST", base64.StdEncoding.EncodeToString([]byte(rpc("tools/list", nil))),
		func(evt *events.APIGatewayV2HTTPRequest) { evt.IsBase64Encoded = true },
	))

	reply := parseReply(t, resp.Body)
	it.Then(t).
		Should(it.Equal(resp.StatusCode, 200)).
		Should(it.Equal(len(reply.Result.Tools), 2))
}

//------------------------------------------------------------------------------
// payload 1.0
//------------------------------------------------------------------------------

func TestV1BasicAuthorizer(t *testing.T) {
	h := serve.Handler(serve.Static(newServer()))

	resp := serveV1(t, h, eventV1("POST", callTool("whoami"), func(evt *events.APIGatewayProxyRequest) {
		evt.Headers = nil
		evt.MultiValueHeaders = map[string][]string{}
		for k, v := range mcpHeaders() {
			evt.MultiValueHeaders[k] = []string{v}
		}
		evt.MultiValueHeaders["authorization"] = []string{"Basic YWNjZXNzOnNlY3JldA"}
		evt.RequestContext.Authorizer = map[string]any{"auth": "basic", "sub": "access"}
	}))

	out := whoamiOf(t, resp.Body)
	it.Then(t).
		Should(it.Equal(resp.StatusCode, 200)).
		Should(it.Equal(out.User, "access")).
		Should(it.Equal(out.Extra["auth"], "basic")).
		Should(it.Equal(out.Authorization, "Basic YWNjZXNzOnNlY3JldA"))
}

func TestV1NestedLambdaAuthorizer(t *testing.T) {
	h := serve.Handler(serve.Static(newServer()))

	resp := serveV1(t, h, eventV1("POST", callTool("whoami"), func(evt *events.APIGatewayProxyRequest) {
		evt.PathParameters = map[string]string{"instance": "i-3"}
		evt.RequestContext.Authorizer = map[string]any{
			"lambda": map[string]any{"principal": "u-3", "scopes": []any{"read"}},
		}
	}))

	out := whoamiOf(t, resp.Body)
	it.Then(t).
		Should(it.Equal(out.User, "u-3")).
		Should(it.Seq(out.Scopes).Equal("read")).
		Should(it.Equal(out.Path["instance"], "i-3"))
}

func TestV1JWTAuthorizer(t *testing.T) {
	h := serve.Handler(serve.Static(newServer()))

	resp := serveV1(t, h, eventV1("POST", callTool("whoami"), func(evt *events.APIGatewayProxyRequest) {
		evt.RequestContext.Authorizer = map[string]any{
			"jwt": map[string]any{"claims": map[string]any{"sub": "jwt-2"}, "scopes": []any{"a", "b"}},
		}
	}))

	out := whoamiOf(t, resp.Body)
	it.Then(t).
		Should(it.Equal(out.User, "jwt-2")).
		Should(it.Seq(out.Scopes).Equal("a", "b"))
}

func TestV1MultiValueQuery(t *testing.T) {
	var query string
	f := func(r *http.Request) *mcp.Server {
		query = r.URL.RawQuery
		return newServer()
	}
	h := serve.Handler(f)

	serveV1(t, h, eventV1("POST", rpc("tools/list", nil), func(evt *events.APIGatewayProxyRequest) {
		evt.QueryStringParameters = map[string]string{"a": "2"}
		evt.MultiValueQueryStringParameters = map[string][]string{"a": {"1", "2"}}
	}))

	it.Then(t).Should(it.Equal(query, "a=1&a=2"))
}

//------------------------------------------------------------------------------
// errors
//------------------------------------------------------------------------------

func TestParseError(t *testing.T) {
	h := serve.Handler(serve.Static(newServer()))

	for _, body := range []string{"not json", "", `{"jsonrpc":"2.0"`} {
		resp := serveV2(t, h, eventV2("POST", body, nil))
		reply := parseReply(t, resp.Body)
		it.Then(t).
			Should(it.Equal(resp.StatusCode, 400)).
			Should(it.Equal(reply.Error.Code, -32700))
	}
}

func TestMethodNotAllowed(t *testing.T) {
	h := serve.Handler(serve.Static(newServer()))

	for _, method := range []string{"GET", "DELETE"} {
		resp := serveV2(t, h, eventV2(method, "", nil))
		it.Then(t).
			Should(it.Equal(resp.StatusCode, 405)).
			Should(it.Equal(resp.Headers["Allow"], "POST"))

		v1 := serveV1(t, h, eventV1(method, "", nil))
		it.Then(t).
			Should(it.Equal(v1.StatusCode, 405)).
			Should(it.Seq(v1.MultiValueHeaders["Allow"]).Equal("POST"))
	}
}

func TestPanic(t *testing.T) {
	h := serve.Handler(func(*http.Request) *mcp.Server { panic("boom") })

	resp := serveV2(t, h, eventV2("POST", callTool("whoami"), nil))
	reply := parseReply(t, resp.Body)
	it.Then(t).
		Should(it.Equal(resp.StatusCode, 500)).
		Should(it.Equal(reply.Error.Code, -32603))
}

func TestToolPanic(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "t", Version: "v"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "boom"},
		func(context.Context, *mcp.CallToolRequest, None) (*mcp.CallToolResult, None, error) {
			panic("boom")
		},
	)
	h := serve.Handler(serve.Static(server))

	for range 2 {
		resp := serveV2(t, h, eventV2("POST", callTool("boom"), nil))
		reply := parseReply(t, resp.Body)
		it.Then(t).
			Should(it.True(reply.Error != nil)).
			Should(it.Equal(reply.Error.Code, -32603))
	}
}

func TestUnknownEvent(t *testing.T) {
	h := serve.Handler(serve.Static(newServer()))

	_, err := h(context.Background(), json.RawMessage(`[]`))
	it.Then(t).ShouldNot(it.Nil(err))
}

//------------------------------------------------------------------------------
// tools per request
//------------------------------------------------------------------------------

func readonly(t *mcp.Tool, info *auth.TokenInfo) bool {
	if info != nil && slices.Contains(info.Scopes, "write") {
		return true
	}
	return t.Annotations != nil && t.Annotations.ReadOnlyHint
}

func TestFilter(t *testing.T) {
	h := serve.Handler(serve.Filter(newServer(), readonly))

	withScope := func(scope string) func(*events.APIGatewayV2HTTPRequest) {
		return func(evt *events.APIGatewayV2HTTPRequest) {
			evt.RequestContext.Authorizer = &events.APIGatewayV2HTTPRequestContextAuthorizerDescription{
				Lambda: map[string]any{"principal": "u", "scope": scope},
			}
		}
	}

	t.Run("ListRead", func(t *testing.T) {
		reply := parseReply(t, serveV2(t, h, eventV2("POST", rpc("tools/list", nil), withScope("read"))).Body)
		it.Then(t).
			Should(it.Equal(len(reply.Result.Tools), 1)).
			Should(it.Equal(reply.Result.Tools[0].Name, "whoami"))
	})

	t.Run("ListWrite", func(t *testing.T) {
		reply := parseReply(t, serveV2(t, h, eventV2("POST", rpc("tools/list", nil), withScope("read write"))).Body)
		it.Then(t).Should(it.Equal(len(reply.Result.Tools), 2))
	})

	t.Run("CallDenied", func(t *testing.T) {
		reply := parseReply(t, serveV2(t, h, eventV2("POST", callTool("write"), withScope("read"))).Body)
		it.Then(t).
			Should(it.True(reply.Error != nil)).
			Should(it.Equal(reply.Error.Code, -32602))
	})

	t.Run("CallAllowed", func(t *testing.T) {
		reply := parseReply(t, serveV2(t, h, eventV2("POST", callTool("write"), withScope("write"))).Body)
		it.Then(t).Should(it.True(reply.Error == nil))
	})
}

//------------------------------------------------------------------------------
// authorization
//------------------------------------------------------------------------------

func TestResourceServer(t *testing.T) {
	h := serve.Handler(serve.Static(newServer()),
		serve.WithResourceServer(serve.ResourceServer{
			Metadata: &oauthex.ProtectedResourceMetadata{
				Resource:             "https://mcp.example.com/mcp",
				AuthorizationServers: []string{"https://auth.example.com"},
			},
			Authenticated: func(info *auth.TokenInfo) bool { return info.Extra["decision"] == "allow" },
		}),
	)

	t.Run("Metadata", func(t *testing.T) {
		resp := serveV2(t, h, eventV2("GET", "", func(evt *events.APIGatewayV2HTTPRequest) {
			evt.RawPath = "/.well-known/oauth-protected-resource"
		}))
		var meta oauthex.ProtectedResourceMetadata
		json.Unmarshal([]byte(resp.Body), &meta)
		it.Then(t).
			Should(it.Equal(resp.StatusCode, 200)).
			Should(it.Equal(meta.Resource, "https://mcp.example.com/mcp"))
	})

	t.Run("Challenge", func(t *testing.T) {
		resp := serveV2(t, h, eventV2("POST", callTool("whoami"), func(evt *events.APIGatewayV2HTTPRequest) {
			evt.RequestContext.Authorizer = &events.APIGatewayV2HTTPRequestContextAuthorizerDescription{
				Lambda: map[string]any{"decision": "anonymous"},
			}
		}))
		it.Then(t).
			Should(it.Equal(resp.StatusCode, 401)).
			Should(it.Equal(resp.Headers["Www-Authenticate"],
				`Bearer resource_metadata="https://mcp.example.com/.well-known/oauth-protected-resource"`))
	})

	t.Run("Authenticated", func(t *testing.T) {
		resp := serveV2(t, h, eventV2("POST", callTool("whoami"), func(evt *events.APIGatewayV2HTTPRequest) {
			evt.RequestContext.Authorizer = &events.APIGatewayV2HTTPRequestContextAuthorizerDescription{
				Lambda: map[string]any{"decision": "allow", "principal": "u"},
			}
		}))
		it.Then(t).
			Should(it.Equal(resp.StatusCode, 200)).
			Should(it.Equal(whoamiOf(t, resp.Body).User, "u"))
	})
}

func verifier(_ context.Context, token string, _ *http.Request) (*auth.TokenInfo, error) {
	if token != "good" {
		return nil, auth.ErrInvalidToken
	}
	return &auth.TokenInfo{UserID: "v-1", Scopes: []string{"read"}, Expiration: time.Now().Add(time.Hour)}, nil
}

func TestVerifierLambda(t *testing.T) {
	h := serve.Handler(serve.Static(newServer()),
		serve.WithVerifier(verifier),
		serve.WithResourceServer(serve.ResourceServer{
			Metadata: &oauthex.ProtectedResourceMetadata{Resource: "https://mcp.example.com/mcp"},
		}),
	)

	t.Run("Valid", func(t *testing.T) {
		resp := serveV2(t, h, eventV2("POST", callTool("whoami"), func(evt *events.APIGatewayV2HTTPRequest) {
			evt.Headers["authorization"] = "Bearer good"
			evt.PathParameters = map[string]string{"instance": "i-4"}
		}))
		out := whoamiOf(t, resp.Body)
		it.Then(t).
			Should(it.Equal(out.User, "v-1")).
			Should(it.Equal(out.Path["instance"], "i-4"))
	})

	t.Run("Missing", func(t *testing.T) {
		resp := serveV2(t, h, eventV2("POST", callTool("whoami"), nil))
		it.Then(t).
			Should(it.Equal(resp.StatusCode, 401)).
			Should(it.String(resp.Headers["Www-Authenticate"]).Contain("resource_metadata="))
	})
}

//------------------------------------------------------------------------------
// server mode
//------------------------------------------------------------------------------

func TestHTTPHandler(t *testing.T) {
	ts := httptest.NewServer(
		serve.HTTPHandler(serve.Static(newServer()),
			serve.WithVerifier(verifier),
			serve.WithHTTPOptions(&mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true}),
		),
	)
	defer ts.Close()

	req, _ := http.NewRequest("POST", ts.URL, strings.NewReader(callTool("whoami")))
	for k, v := range mcpHeaders() {
		req.Header.Set(k, v)
	}
	req.Header.Set("Authorization", "Bearer good")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}

	it.Then(t).
		Should(it.Equal(resp.StatusCode, 200)).
		Should(it.Equal(whoamiOf(t, string(body)).User, "v-1"))
}

func TestMCPClient(t *testing.T) {
	ts := httptest.NewServer(serve.HTTPHandler(serve.Static(newServer())))
	defer ts.Close()

	client := mcp.NewClient(&mcp.Implementation{Name: "client", Version: "v0.0.0"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: ts.URL}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	tools, err := session.ListTools(context.Background(), nil)
	it.Then(t).
		Should(it.Nil(err)).
		Should(it.Equal(len(tools.Tools), 2))
}

//------------------------------------------------------------------------------
// dispatcher invocation
//------------------------------------------------------------------------------

func TestInvocation(t *testing.T) {
	h := serve.Handler(serve.Static(newServer()))

	evt, err := serve.NewInvocation(serve.Invocation{
		Message:         json.RawMessage(callTool("whoami")),
		ProtocolVersion: protocol,
		TokenInfo:       &auth.TokenInfo{UserID: "fwd", Scopes: []string{"read"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	out, err := h(context.Background(), evt)
	if err != nil {
		t.Fatal(err)
	}
	raw, ok := out.(json.RawMessage)
	it.Then(t).Should(it.True(ok))

	who := whoamiOf(t, string(raw))
	it.Then(t).
		Should(it.Equal(who.User, "fwd")).
		Should(it.Seq(who.Scopes).Equal("read"))
}
