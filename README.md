<p align="center">
  <img src="./doc/cloudmcp-v1.png" height="128" />
  <p align="center"><strong>Cloud-Native Serverless Model-Context-Protocol (MCP) Server</strong></p>
</p>

<p align="center">
  <a href="https://github.com/fogfish/cloudmcp/releases">
    <img src="https://img.shields.io/github/v/tag/fogfish/cloudmcp?label=version" />
  </a>
  <a href="https://github.com/fogfish/cloudmcp/actions/">
    <img src="https://github.com/fogfish/cloudmcp/workflows/build/badge.svg" />
  </a>
  <a href="http://github.com/fogfish/cloudmcp">
    <img src="https://img.shields.io/github/last-commit/fogfish/cloudmcp.svg" />
  </a>
  <a href="https://coveralls.io/github/fogfish/cloudmcp?branch=main">
    <img src="https://coveralls.io/repos/github/fogfish/cloudmcp/badge.svg?branch=main" />
  </a>
  <a href="LICENSE">
    <img src="https://img.shields.io/badge/license-MIT-blue.svg" />
  </a>
</p>

> Deploy Model Context Protocol servers to AWS Lambda with zero infrastructure hassle

CloudMCP is a Golang library that runs **Model Context Protocol (MCP)** servers, written with the [official Go SDK](https://github.com/modelcontextprotocol/go-sdk), on **AWS Lambda** behind **AWS API Gateway**, using AWS CDK for infrastructure-as-code. Write your MCP tools in Go, write a one-line Lambda main, deploy with `cdk deploy`.

## Why `cloudmcp` library?

Building MCP servers is easy. Deploying them to production with proper authentication, scaling, and monitoring takes real effort. This library removes that friction.

**What you get:**

* **Built on the [official Go MCP SDK](https://github.com/modelcontextprotocol/go-sdk)**, tools are plain go-sdk tools, they never import cloudmcp.
* **Serverless-first architecture** with instant autoscaling, zero server management and pay-only-for-use economics.
* **Security at AWS API Gateway**: API keys, JWT (Cognito, Auth0, ...), AWS IAM or your own Lambda authorizer. The caller's identity reaches tools as go-sdk's `req.Extra.TokenInfo`.
* **Tools per caller**: the server is built once, tools are filtered per request (e.g. read-only callers see read-only tools).
* **MCP authorization compliance** (opt-in): protected resource metadata (RFC 9728) and `401` challenge for OAuth-capable clients.
* **Observability**: CloudWatch metrics (EMF) per method and tool, structured JSON logs.
* **Infrastructure as Code** with AWS CDK constructs that drop into an existing application.
* **Portability to server mode**: the same server runs as long-running HTTP server with `serve.ListenAndServe`.

## Quick Start

```bash
go get github.com/fogfish/cloudmcp
```

See [examples/helloworld](examples/helloworld) for a complete working example. Define your MCP server using the official Go SDK:

```go
package server

func New() *mcp.Server {
  server := mcp.NewServer(&mcp.Implementation{Name: "hello world", Version: "v1.0.0"}, nil)
  mcp.AddTool(server, &mcp.Tool{Name: "sayer", Description: "says Hello World!"}, Sayer)
  return server
}
```

Write the Lambda function's main (e.g. `cmd/mcp/main.go`), dependencies are injected by your code:

```go
package main

import (
  "github.com/aws/aws-lambda-go/lambda"
  "github.com/fogfish/cloudmcp/serve"
)

func main() {
  lambda.Start(serve.Handler(serve.Static(server.New())))
}
```

Deploy it (CDK application, e.g. `main.go` with `cdk.json`), run `cdk deploy`:

```go
func main() {
  cloudmcp.New("cmd/mcp").
    Hostless().
    AccessApiKey("access", "secret").
    Build()
}
```

The library has two parts:

* [`github.com/fogfish/cloudmcp/serve`](./serve) is the runtime used by the Lambda function. It does not depend on AWS CDK, which keeps functions small.
* [`github.com/fogfish/cloudmcp`](.) is AWS CDK constructs: a high-level builder for standalone deployments and low-level constructs for existing CDK applications.

## Runtime

`serve.Handler(factory, options...)` is the Lambda handler. The factory has go-sdk's own shape `func(*http.Request) *mcp.Server` (the argument of `mcp.NewStreamableHTTPHandler`):

* `serve.Static(server)` serves the same server to every request.
* `serve.Filter(server, predicate)` builds the server once and chooses tools per request, the predicate receives the tool and the caller's `TokenInfo`. Hidden tools are absent from `tools/list` and fail `tools/call` as unknown tools.
* `serve.Tool(tool, handler)` is the server with a single typed tool (e.g. worker of the dispatcher).

```go
readonly := func(t *mcp.Tool, info *auth.TokenInfo) bool {
  if info != nil && slices.Contains(info.Scopes, "write") {
    return true
  }
  return t.Annotations != nil && t.Annotations.ReadOnlyHint
}

lambda.Start(serve.Handler(serve.Filter(server.New(), readonly)))
```

The handler accepts API Gateway events of payload format 1.0 and 2.0 (detected from the event; multi-value headers and query parameters are preserved). Servers are stateless and respond with JSON, Lambda cannot keep sessions or stream events.

### Identity

The caller's identity, as established by API Gateway, is mapped to go-sdk's `auth.TokenInfo`, tools read it from `req.Extra.TokenInfo`. Nothing in the request body or headers can change it. `TokenInfo` is produced for every request through API Gateway, including Basic (API key) and anonymous (public) calls, whose `UserID` is empty.

| `TokenInfo` | Source                                                                                                       |
| ----------- | ------------------------------------------------------------------------------------------------------------ |
| `UserID`    | Lambda authorizer context `principal`, `sub` or `principalId`; JWT claim `sub`; IAM user ARN                 |
| `Scopes`    | Lambda authorizer context `scope` or `scopes` (space separated string or list); JWT scopes                   |
| `Extra`     | every key of the Lambda authorizer context, JWT claims at `claims`, path parameters at `path`                |
| `Expiration`| deadline of the Lambda invocation                                                                            |

```go
func Tool(ctx context.Context, req *mcp.CallToolRequest, in Input) (*mcp.CallToolResult, Output, error) {
  info := req.Extra.TokenInfo
  instance := info.Extra["path"].(map[string]string)["instance"]
  // ...
}
```

Use `serve.WithIdentity(func(*serve.Identity) *auth.TokenInfo)` to customize the mapping, or `serve.WithVerifier(verifier)` to verify bearer tokens in the function (public routes, server mode).

### Errors

* A body that is not JSON-RPC is answered with HTTP 400 and JSON-RPC error `-32700`.
* `GET` and `DELETE` are answered with HTTP 405 and `Allow: POST`.
* Panics (in the factory or in tools) are recovered, logged and answered with HTTP 500 / JSON-RPC error `-32603`.
* `serve.WithDebug()` logs JSON-RPC method and id of each request; parameters, which may hold user data, are never logged.

### Observability

* `serve.WithMetrics(namespace)` emits CloudWatch metrics using Embedded Metric Format: `Requests`, `Errors` and `Latency` with dimensions `[Method]` and `[Method, Tool]`. A request is an error if the HTTP status is not 2xx, the JSON-RPC response is an error, or the tool result has `isError`.
* `serve.WithJSONLogs(level)` writes structured JSON logs to stdout and makes it the default `slog` logger; `serve.WithLogger(logger)` uses your own logger.

### Server mode

The same factory runs as long-running HTTP server, with go-sdk defaults (sessions, event streams):

```go
serve.ListenAndServe(":8080", serve.Static(server.New()))
```

`serve.HTTPHandler(factory, options...)` returns the `http.Handler` to embed into your own server.

### MCP authorization

OAuth-capable MCP clients discover how to authenticate from a `401` with `WWW-Authenticate: Bearer resource_metadata="..."` and protected resource metadata (RFC 9728). API Gateway's own `401` cannot carry the challenge, so the function is the resource server:

```go
serve.Handler(factory,
  serve.WithResourceServer(serve.ResourceServer{
    Metadata: &oauthex.ProtectedResourceMetadata{
      Resource:             "https://mcp.example.com/mcp",
      AuthorizationServers: []string{"https://auth.example.com"},
    },
    // with annotating Lambda authorizer, which always allows and passes its decision
    Authenticated: func(info *auth.TokenInfo) bool { return info.Extra["decision"] == "allow" },
  }),
)
```

The route is either public with `serve.WithVerifier` verifying tokens in the function, or protected by an annotating Lambda authorizer. Route the metadata to the function with `.ResourceMetadata()` (high-level) or `Server.ServeResourceMetadata` (low-level).

## High-level API

The builder deploys a standalone stack: API Gateway, authorizer, the server function and the log group `/app/{name}` (5 days retention).

`cloudmcp.New("cmd/mcp")` takes the path of the server's main package relative to the main module of the CDK application (`.Module(...)` overrides the module). Configure **hosting** and **security** before `.Build()`.

* `.Name(name)` the stack name, default is the base name of the path
* `.Route(route)` the route, default `/mcp`, may have path parameters (`/{instance}`) or be the root `/`
* `.Function(props)` properties of the function (memory, environment, ...)
* `.Hostless()` default API Gateway host, `.Host(domain, tlsarn)` custom domain, `.Gateway(props)` custom properties
* `.Cors(origins...)` CORS for browser MCP clients, allowing MCP headers (`Mcp-Session-Id`, `Mcp-Protocol-Version`)
* `.ResourceMetadata()` public routes of protected resource metadata

Security:

* `.AccessPublic()` no authentication
* `.AccessApiKey(access, secret)` access/secret keys as Basic credentials
* `.AccessJWT(issuer, audiences...)` JWT tokens
* `.AccessAwsCognito(poolArn, clients...)` AWS Cognito
* `.AccessLambda("cmd/authorizer", props)` your own Lambda authorizer (see [scud/authorizer](https://github.com/fogfish/scud))

## Low-level API

Constructs drop into an existing CDK application, the stack, naming and log groups follow the application's conventions (`scud.SetDefaults`).

```go
gateway := scud.NewGateway(stack, jsii.String("Gateway"),
  &scud.GatewayProps{Cors: cloudmcp.Cors("https://example.com")},
)

server := cloudmcp.NewServer(stack, jsii.String("MCP"),
  &cloudmcp.ServerProps{
    FunctionGoProps: &scud.FunctionGoProps{
      SourceCodeModule: "github.com/example/app",
      SourceCodeLambda: "cmd/mcp",
    },
    Route: "/{instance}",
  },
)

server.AllowAccessLambda(gateway.NewAuthorizerLambda(authorizer, nil))
```

* `Route` default is `/mcp`; `POST`, `GET` and `DELETE` are routed to the function (the function answers 405 to the latter).
* `PayloadFormatVersion` default is 2.0, the function accepts both.
* The function timeout defaults to 29 seconds; synthesis fails if it is 30 seconds or longer (API Gateway HTTP API integration timeout).
* Access: `AllowAccessPublic`, `AllowAccessApiKey`, `AllowAccessJWT(api, scopes...)`, `AllowAccessIAM(api, principal)`, `AllowAccessLambda`; `ServeResourceMetadata` for MCP authorization.

### Dispatcher

The dispatcher is a single MCP endpoint aggregating tools of worker functions. It lists tools of workers at cold start and forwards `tools/call` to the owning worker through Lambda Invoke, together with the caller's `TokenInfo`. Each worker keeps its own IAM role (least privilege per tool). Tool names must be unique across workers.

```go
// worker's main
lambda.Start(serve.Handler(serve.Tool(&mcp.Tool{Name: "sayer"}, sayer.Sayer)))

// CDK
cloudmcp.NewDispatcher(stack, jsii.String("Dispatcher"),
  &cloudmcp.DispatcherProps{
    Name:             "tools",
    MetricsNamespace: "tools",
    Workers:          []awslambda.IFunction{sayer, clock},
  },
).AllowAccessApiKey(gateway.NewAuthorizerBasic("access", "secret"))
```

The dispatcher function is built from [cmd/dispatcher](./cmd/dispatcher); use package [dispatch](./dispatch) for your own dispatcher main.

### Examples

- **[helloworld](examples/helloworld)** - MCP server using the high-level API
- **[serverless](examples/serverless)** - dispatcher with workers using the low-level API

Both examples are deployable with `cdk deploy`.

## Clients

[`pkg/auth`](./pkg/auth) creates MCP client transports with authentication:

* `auth.NewTransportApiKey` access/secret keys as standard Basic credentials
* `auth.NewTransportBearer` bearer token (e.g. API keys issued as bearer tokens)
* `auth.NewTransportIAM` AWS SigV4 signed requests

```go
transport, err := auth.NewTransportApiKey(auth.ConfigApiKey{Url: "https://...", Access: "access", Secret: "secret"})

client := mcp.NewClient(/* ... */)
client.Connect(context.Background(), transport, nil)
```

### Authorization header

MCP clients configured by JSON or CLI (VS Code, Claude Code, ...) send the `Authorization` header as given, build its value for the access method of the server:

* `.AccessApiKey(access, secret)`: `Basic <digest>`, where the digest is base64 of `access:secret` ([RFC 7617](https://www.rfc-editor.org/rfc/rfc7617))
* `.AccessJWT(...)`, `.AccessAwsCognito(...)`: `Bearer <token>`, where the token is a JWT issued by your identity provider
* `.AccessPublic()`: no header

The digest of Basic credentials:

```bash
printf '%s' 'access:secret' | base64
# YWNjZXNzOnNlY3JldA==
```

Use `printf` (or `echo -n`), a trailing newline of plain `echo` corrupts the digest. GNU `base64` wraps output longer than 76 characters, use `base64 -w0` on Linux for long keys. Note that Basic credentials are encoded, not encrypted, keep the digest as secret as the keys.

### Use in Claude Code

```bash
claude mcp add --transport http helloworld https://0000000000.execute-api.eu-west-1.amazonaws.com/mcp \
  --header "Authorization: Basic $(printf '%s' 'access:secret' | base64)"
```

### Use in VS Code

Integration of serverless MCPs is as straightforward as any other, within `mcp.json`

```json
{
  "servers": {
    "helloworld": {
      "type": "http",
      "url": "https://0000000000.execute-api.eu-west-1.amazonaws.com/mcp",
      "headers": {
        "Authorization": "Basic YWNjZXNzOnNlY3JldA=="
      }
    }
  }
}
```

## When to use `cloudmcp` library

✅ You need MCP servers in production, not just local development  
✅ You want serverless benefits: auto-scaling, pay-per-use, zero maintenance  
✅ You use AWS or are willing to deploy there  
✅ You want infrastructure as code with AWS CDK  

❌ You need server-to-client requests or event streams in Lambda (use server mode)  
❌ You're committed to a different cloud provider  


## Requirements

- Go 1.26+
- AWS Account
- AWS CDK CLI: `npm install -g aws-cdk`
- Configured AWS credentials


## Related Projects

- [Model Context Protocol](https://modelcontextprotocol.io) - Official MCP specification
- [MCP Go SDK](https://github.com/modelcontextprotocol/go-sdk) - Official Go MCP implementation
- [Scud](https://github.com/fogfish/scud) - Serverless CDK utilities (cloudmcp dependency)


## Contributing

`cloudmcp` is [MIT](LICENSE) licensed and accepts contributions via GitHub pull requests:

1. Fork it
2. Create your feature branch (`git checkout -b my-new-feature`)
3. Commit your changes (`git commit -am 'Added some feature'`)
4. Push to the branch (`git push origin my-new-feature`)
5. Create new Pull Request


## License


[![See LICENSE](https://img.shields.io/github/license/fogfish/cloudmcp.svg?style=for-the-badge)](LICENSE)
