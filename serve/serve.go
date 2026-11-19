//
// Copyright (C) 2025 Dmitry Kolesnikov
//
// This file may be modified and distributed under the terms
// of the MIT license.  See the LICENSE file for details.
// https://github.com/fogfish/cloudmcp
//

// Package serve is the runtime of cloudmcp. It runs MCP servers, defined with
// the official Go SDK, as AWS Lambda functions behind AWS API Gateway, or as
// long-running HTTP servers.
//
// The package does not depend on AWS CDK, the Lambda function's main imports it:
//
//	func main() {
//		lambda.Start(serve.Handler(server.New))
//	}
package serve

import (
	"io"
	"log/slog"
	"net/http"
	"os"
	"runtime/debug"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ServerFactory returns MCP server for the request. It is the argument of
// mcp.NewStreamableHTTPHandler. Use Static for the server shared by all
// requests, or Filter to choose tools per request.
type ServerFactory = func(*http.Request) *mcp.Server

// Static returns the factory that serves the same server for every request.
func Static(server *mcp.Server) ServerFactory {
	return func(*http.Request) *mcp.Server { return server }
}

// Option configures the runtime.
type Option func(*config)

type config struct {
	http     *mcp.StreamableHTTPOptions
	logger   *slog.Logger
	identity func(*Identity) *auth.TokenInfo
	verifier auth.TokenVerifier
	resource *ResourceServer
	metrics  *metrics
	debug    bool
}

// WithHTTPOptions overrides options of the go-sdk streamable HTTP handler.
// Lambda functions default to stateless JSON responses, Lambda cannot keep
// sessions or stream events. Server mode defaults to the go-sdk defaults.
func WithHTTPOptions(opts *mcp.StreamableHTTPOptions) Option {
	return func(c *config) { c.http = opts }
}

// WithLogger sets the logger of the runtime and the go-sdk handler.
// The default is slog.Default().
func WithLogger(logger *slog.Logger) Option {
	return func(c *config) { c.logger = logger }
}

// WithJSONLogs configures structured JSON logs to stdout at the given level,
// it also becomes the default slog logger of the process.
func WithJSONLogs(level slog.Leveler) Option {
	return func(c *config) {
		c.logger = slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
		slog.SetDefault(c.logger)
	}
}

// WithIdentity overrides the mapping of the gateway's identity (authorizer
// context and path parameters) into go-sdk's auth.TokenInfo, which tools
// read from req.Extra.TokenInfo. The default is DefaultTokenInfo. Returning
// nil leaves the request without TokenInfo.
func WithIdentity(f func(*Identity) *auth.TokenInfo) Option {
	return func(c *config) { c.identity = f }
}

// WithVerifier verifies bearer tokens within the function, using go-sdk's
// auth.RequireBearerToken. Use it when the route is public (server mode, or
// public API Gateway route) and the function is the resource server.
// Path parameters of the gateway are added to TokenInfo.Extra["path"].
func WithVerifier(verifier auth.TokenVerifier) Option {
	return func(c *config) { c.verifier = verifier }
}

// WithDebug logs JSON-RPC method and id of each request at debug level.
// Parameters are never logged, they may hold user data.
func WithDebug() Option {
	return func(c *config) { c.debug = true }
}

// HTTPHandler returns http.Handler serving MCP servers of the factory.
// It is the core of both Lambda (Handler) and server (ListenAndServe) modes.
func HTTPHandler(f ServerFactory, opts ...Option) http.Handler {
	c := newConfig(nil, opts)
	return c.pipeline(f)
}

// ListenAndServe runs MCP servers of the factory as long-running HTTP server.
func ListenAndServe(addr string, f ServerFactory, opts ...Option) error {
	c := newConfig(nil, opts)
	return http.ListenAndServe(addr, c.pipeline(f))
}

func newConfig(defaults *mcp.StreamableHTTPOptions, opts []Option) *config {
	c := &config{
		http:     defaults,
		identity: DefaultTokenInfo,
	}
	for _, opt := range opts {
		opt(c)
	}
	if c.logger == nil {
		c.logger = slog.Default()
	}
	if c.http == nil {
		c.http = &mcp.StreamableHTTPOptions{}
	}
	if c.http.Logger == nil {
		c.http.Logger = c.logger
	}
	return c
}

// recover → metrics → resource metadata → protocol guard → identity → go-sdk
func (c *config) pipeline(f ServerFactory) http.Handler {
	var h http.Handler = mcp.NewStreamableHTTPHandler(c.withRecoverMCP(f), c.http)
	h = c.withIdentity(h)
	h = c.withGuard(h)
	if c.resource != nil {
		h = c.withResourceMetadata(h)
	}
	if c.metrics != nil {
		h = c.metrics.handler(h)
	}
	h = c.withRecover(h)
	return h
}

// version of the main module, used by servers built by the library
func version() string {
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" {
		return info.Main.Version
	}
	return "(devel)"
}

var stdout io.Writer = os.Stdout
