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
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/aws/aws-lambda-go/events"
	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// LambdaHandler is the entry point of Lambda function, use it with lambda.Start.
type LambdaHandler = func(context.Context, json.RawMessage) (any, error)

// Handler returns Lambda function handler serving MCP servers of the factory.
//
//	func main() {
//		lambda.Start(serve.Handler(server.New))
//	}
//
// The handler accepts API Gateway events of payload format 1.0
// (events.APIGatewayProxyRequest) and 2.0 (events.APIGatewayV2HTTPRequest),
// detected from the event, and direct invocations by the dispatcher.
// Servers are stateless and respond with JSON (no event streams).
func Handler(f ServerFactory, opts ...Option) LambdaHandler {
	c := newConfig(
		&mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true},
		opts,
	)
	h := c.pipeline(f)

	return func(ctx context.Context, evt json.RawMessage) (any, error) {
		var probe struct {
			Version  string          `json:"version"`
			Dispatch json.RawMessage `json:"cloudmcp"`
		}
		if err := json.Unmarshal(evt, &probe); err != nil {
			c.logger.ErrorContext(ctx, "unsupported lambda event", "err", err)
			return nil, err
		}

		switch {
		case len(probe.Dispatch) > 0:
			return serveInvocation(ctx, h, probe.Dispatch)
		case probe.Version == "2.0":
			return serveV2(ctx, h, evt)
		default:
			return serveV1(ctx, h, evt)
		}
	}
}

//------------------------------------------------------------------------------
// Payload format 1.0
//------------------------------------------------------------------------------

func serveV1(ctx context.Context, h http.Handler, evt json.RawMessage) (*events.APIGatewayProxyResponse, error) {
	var req events.APIGatewayProxyRequest
	if err := json.Unmarshal(evt, &req); err != nil {
		w := newResponse()
		writeError(w, http.StatusBadRequest, jsonrpc.CodeInvalidRequest, "invalid request")
		return w.v1(), nil
	}

	w := newResponse()
	r, err := NewRequestV1(ctx, &req)
	if err != nil {
		writeError(w, http.StatusBadRequest, jsonrpc.CodeInvalidRequest, "invalid request")
		return w.v1(), nil
	}

	h.ServeHTTP(w, r)
	return w.v1(), nil
}

// NewRequestV1 converts API Gateway event (payload format 1.0) to HTTP request
// with the caller's Identity in the context.
func NewRequestV1(ctx context.Context, evt *events.APIGatewayProxyRequest) (*http.Request, error) {
	query := url.Values{}
	if len(evt.MultiValueQueryStringParameters) > 0 {
		for k, seq := range evt.MultiValueQueryStringParameters {
			for _, v := range seq {
				query.Add(k, v)
			}
		}
	} else {
		for k, v := range evt.QueryStringParameters {
			query.Add(k, v)
		}
	}

	header := http.Header{}
	if len(evt.MultiValueHeaders) > 0 {
		for k, seq := range evt.MultiValueHeaders {
			for _, v := range seq {
				header.Add(k, v)
			}
		}
	} else {
		for k, v := range evt.Headers {
			header.Add(k, v)
		}
	}

	body, err := decodeBody(evt.Body, evt.IsBase64Encoded)
	if err != nil {
		return nil, err
	}

	id := &Identity{PathParameters: evt.PathParameters}
	authorizer := evt.RequestContext.Authorizer
	switch {
	case isMap(authorizer["lambda"]):
		id.Authorizer = authorizer["lambda"].(map[string]any)
	case isMap(authorizer["jwt"]):
		jwt := authorizer["jwt"].(map[string]any)
		id.Claims, _ = jwt["claims"].(map[string]any)
		id.Scopes = toScopes(jwt["scopes"])
	case isMap(authorizer["claims"]):
		id.Claims = authorizer["claims"].(map[string]any)
		if authorizer["scopes"] != nil {
			id.Scopes = toScopes(authorizer["scopes"])
		}
	default:
		id.Authorizer = authorizer
	}
	id.Principal = evt.RequestContext.Identity.UserArn

	return newRequest(ctx, id, evt.HTTPMethod, evt.Path, query.Encode(), header, body,
		evt.RequestContext.Identity.SourceIP)
}

//------------------------------------------------------------------------------
// Payload format 2.0
//------------------------------------------------------------------------------

func serveV2(ctx context.Context, h http.Handler, evt json.RawMessage) (*events.APIGatewayV2HTTPResponse, error) {
	var req events.APIGatewayV2HTTPRequest
	if err := json.Unmarshal(evt, &req); err != nil {
		w := newResponse()
		writeError(w, http.StatusBadRequest, jsonrpc.CodeInvalidRequest, "invalid request")
		return w.v2(), nil
	}

	w := newResponse()
	r, err := NewRequestV2(ctx, &req)
	if err != nil {
		writeError(w, http.StatusBadRequest, jsonrpc.CodeInvalidRequest, "invalid request")
		return w.v2(), nil
	}

	h.ServeHTTP(w, r)
	return w.v2(), nil
}

// NewRequestV2 converts API Gateway event (payload format 2.0) to HTTP request
// with the caller's Identity in the context.
func NewRequestV2(ctx context.Context, evt *events.APIGatewayV2HTTPRequest) (*http.Request, error) {
	header := http.Header{}
	for k, v := range evt.Headers {
		header.Add(k, v)
	}
	if len(evt.Cookies) > 0 {
		header.Set("Cookie", strings.Join(evt.Cookies, "; "))
	}

	body, err := decodeBody(evt.Body, evt.IsBase64Encoded)
	if err != nil {
		return nil, err
	}

	id := &Identity{PathParameters: evt.PathParameters}
	if a := evt.RequestContext.Authorizer; a != nil {
		id.Authorizer = a.Lambda
		if a.JWT != nil {
			id.Claims = make(map[string]any, len(a.JWT.Claims))
			for k, v := range a.JWT.Claims {
				id.Claims[k] = v
			}
			id.Scopes = a.JWT.Scopes
		}
		if a.IAM != nil {
			id.Principal = a.IAM.UserARN
		}
	}

	path := evt.RawPath
	if path == "" {
		path = evt.RequestContext.HTTP.Path
	}

	return newRequest(ctx, id, evt.RequestContext.HTTP.Method, path, evt.RawQueryString, header, body,
		evt.RequestContext.HTTP.SourceIP)
}

//------------------------------------------------------------------------------
// Invocation by dispatcher
//------------------------------------------------------------------------------

// Invocation is the event of the dispatcher invoking the worker function.
// The worker's IAM policy grants invoke only to the dispatcher, the worker
// trusts the caller's TokenInfo established by the dispatcher.
type Invocation struct {
	// JSON-RPC message
	Message json.RawMessage `json:"message"`

	// Negotiated MCP protocol version (Mcp-Protocol-Version)
	ProtocolVersion string `json:"protocolVersion,omitempty"`

	// Standard MCP headers of the client's request (Mcp-Method, Mcp-Name,
	// Mcp-Param-*). Since protocol 2026-07-28 the worker validates them
	// against the message, Mcp-Method and Mcp-Name are derived from the
	// message if absent, Mcp-Param-* must be forwarded.
	Header map[string]string `json:"header,omitempty"`

	// The caller's identity
	TokenInfo *auth.TokenInfo `json:"tokenInfo,omitempty"`
}

// NewInvocation encodes the invocation event of the worker function.
func NewInvocation(inv Invocation) ([]byte, error) {
	return json.Marshal(struct {
		Cloudmcp Invocation `json:"cloudmcp"`
	}{inv})
}

func serveInvocation(ctx context.Context, h http.Handler, evt json.RawMessage) (json.RawMessage, error) {
	var inv Invocation
	if err := json.Unmarshal(evt, &inv); err != nil {
		return nil, err
	}

	header := http.Header{}
	header.Set("Content-Type", "application/json")
	header.Set("Accept", "application/json, text/event-stream")
	if inv.ProtocolVersion != "" {
		header.Set("Mcp-Protocol-Version", inv.ProtocolVersion)
	}
	for k, v := range inv.Header {
		if IsStandardHeader(k) {
			header.Set(k, v)
		}
	}
	setMethodHeaders(header, inv.Message)

	if inv.TokenInfo != nil {
		ctx = context.WithValue(ctx, forwardedKey{}, inv.TokenInfo)
	}

	r, err := newRequest(ctx, nil, http.MethodPost, "/", "", header, inv.Message, "")
	if err != nil {
		return nil, err
	}

	w := newResponse()
	h.ServeHTTP(w, r)

	body := bytes.TrimSpace(w.body.Bytes())
	if json.Valid(body) && len(body) > 0 {
		return body, nil
	}

	// non JSON-RPC response (e.g. 401 of resource server)
	msg := strings.TrimSpace(string(body))
	if msg == "" {
		msg = http.StatusText(w.status())
	}
	reply, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      nil,
		"error":   &jsonrpc.Error{Code: jsonrpc.CodeInternalError, Message: msg},
	})
	return reply, nil
}

// IsStandardHeader reports whether the header is a standard MCP header
// mirroring the message (Mcp-Method, Mcp-Name, Mcp-Param-*), which the
// dispatcher forwards to the worker.
func IsStandardHeader(key string) bool {
	key = http.CanonicalHeaderKey(key)
	return key == "Mcp-Method" || key == "Mcp-Name" || strings.HasPrefix(key, "Mcp-Param-")
}

// setMethodHeaders derives Mcp-Method and Mcp-Name from the JSON-RPC request
// unless they are given.
func setMethodHeaders(header http.Header, msg json.RawMessage) {
	var req struct {
		Method string `json:"method"`
		Params struct {
			Name string `json:"name"`
			URI  string `json:"uri"`
		} `json:"params"`
	}
	if err := json.Unmarshal(msg, &req); err != nil || req.Method == "" {
		return
	}

	if header.Get("Mcp-Method") == "" {
		header.Set("Mcp-Method", req.Method)
	}

	if header.Get("Mcp-Name") == "" {
		switch req.Method {
		case "tools/call", "prompts/get":
			header.Set("Mcp-Name", req.Params.Name)
		case "resources/read":
			header.Set("Mcp-Name", req.Params.URI)
		}
	}
}

//------------------------------------------------------------------------------

func isMap(x any) bool {
	_, ok := x.(map[string]any)
	return ok
}

func decodeBody(body string, isBase64 bool) ([]byte, error) {
	if isBase64 {
		return base64.StdEncoding.DecodeString(body)
	}
	return []byte(body), nil
}

func newRequest(
	ctx context.Context,
	id *Identity,
	method, path, query string,
	header http.Header,
	body []byte,
	remoteAddr string,
) (*http.Request, error) {
	if path == "" {
		path = "/"
	}

	u := &url.URL{Path: path, RawQuery: query}
	if id != nil {
		ctx = WithIdentityContext(ctx, id)
	}

	r, err := http.NewRequestWithContext(ctx, method, u.RequestURI(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	r.Header = header
	r.Host = header.Get("Host")
	r.RequestURI = u.RequestURI()
	r.RemoteAddr = remoteAddr
	r.ContentLength = int64(len(body))

	return r, nil
}

//------------------------------------------------------------------------------

// response buffers HTTP response for API Gateway
type response struct {
	code int
	head http.Header
	body bytes.Buffer
}

func newResponse() *response { return &response{head: http.Header{}} }

func (w *response) Header() http.Header { return w.head }

func (w *response) WriteHeader(code int) {
	if w.code == 0 {
		w.code = code
	}
}

func (w *response) Write(b []byte) (int, error) {
	if w.code == 0 {
		w.code = http.StatusOK
	}
	return w.body.Write(b)
}

func (w *response) Flush() {}

func (w *response) status() int {
	if w.code == 0 {
		return http.StatusOK
	}
	return w.code
}

func (w *response) encodeBody() (string, bool) {
	if utf8.Valid(w.body.Bytes()) {
		return w.body.String(), false
	}
	return base64.StdEncoding.EncodeToString(w.body.Bytes()), true
}

func (w *response) v1() *events.APIGatewayProxyResponse {
	body, isBase64 := w.encodeBody()
	return &events.APIGatewayProxyResponse{
		StatusCode:        w.status(),
		MultiValueHeaders: w.head,
		Body:              body,
		IsBase64Encoded:   isBase64,
	}
}

func (w *response) v2() *events.APIGatewayV2HTTPResponse {
	body, isBase64 := w.encodeBody()
	head := make(map[string]string, len(w.head))
	for k, seq := range w.head {
		if k == "Set-Cookie" {
			continue
		}
		head[k] = strings.Join(seq, ",")
	}

	return &events.APIGatewayV2HTTPResponse{
		StatusCode:      w.status(),
		Headers:         head,
		Cookies:         w.head.Values("Set-Cookie"),
		Body:            body,
		IsBase64Encoded: isBase64,
	}
}

var _ io.Writer = (*response)(nil)
