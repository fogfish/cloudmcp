# **Why Building a Serverless MCP Server Is Far Harder Than It Looks**

Implementing a Model Context Protocol (MCP) server in a fully serverless environment sounds deceptively simple: write a Lambda, wire up API Gateway, return JSON-RPC responses. In practice, however, MCP introduces a blend of protocol-level expectations, OAuth 2.1 security requirements, distributed execution semantics, and routing constraints that expose limitations of the AWS serverless stack.

This post distills the architectural challenges and lessons learned from implementing a serverless MCP server using Go (the official MCP Go SDK) and AWS’s serverless primitives, as explored in the [`cloudmcp`](https://github.com/fogfish/cloudmcp) project.


# **1. Choosing the Go SDK to Minimize Protocol Drift**

MCP continues to evolve quickly. Rather than recreating the protocol surface, the server leans on the **official Go MCP SDK**:

* [https://github.com/modelcontextprotocol/go-sdk](https://github.com/modelcontextprotocol/go-sdk)

This avoids divergence from the specification and ensures compatibility with evolving MCP features (e.g., streaming, distributed workers). But using the SDK also means you inherit the protocol’s full expectations—which collide head-on with serverless constraints.



# **2. OAuth 2.1: AWS Serverless ≠ MCP Security Requirements**

MCP’s security model is built on **OAuth 2.1 Protected Resource** semantics, including:

* Mandatory bearer token validation
* Dynamic discovery documents
* Protected resource metadata endpoint
* WWW-Authenticate challenge responses

None of this comes “for free” with AWS Cognito, Auth0, or API Gateway.

### **2.1. Missing OAuth 2.1 Support in Major Identity Providers**

AWS Cognito (and even Auth0) does *not* yet support OAuth 2.1 fully. MCP requires:

* `/openid-configuration`
* `/jwks.json`
* RFC 9728 Protected Resource Metadata
* RFC 8414 Authorization Server Metadata

Cognito exposes OIDC discovery (`/.well-known/openid-configuration`) and JWKS endpoints, but does not implement OAuth 2.1’s *protected resource metadata* endpoint.

### **2.2. API Gateway Cannot Emit the Required Headers**

MCP requires a challenge like:

```
HTTP/1.1 401 Unauthorized
WWW-Authenticate: Bearer realm="mcp",
  resource_metadata="https://your-server.com/.well-known/oauth-protected-resource"
```

API Gateway **cannot inject arbitrary WWW-Authenticate headers** in an authorizer-driven 401.
Consequently, an MCP-compliant flow requires a custom **proxy Lambda**:

1. Detect missing/invalid Bearer token
2. Return the exact WWW-Authenticate header MCP needs
3. Host the `.well-known/oauth-protected-resource` endpoint
4. Validate tokens manually using Cognito JWKS
5. Forward validated requests to protected MCP handlers

This is a non-trivial reimplementation of OAuth-grade resource protection.



# **3. MCP Features That Don’t Fit Serverless Constraints**

## **3.1. Streaming & Long-Lived Connections**

MCP supports:

* **streamable HTTP**
* **incremental or multi-part responses**
* **server-initiated events**

AWS API Gateway and Lambda **do not support true bidirectional or long-lived streaming**.
This means that a single Lambda instance cannot satisfy a full MCP session.

The workaround is a **distributed execution model**, which the Go SDK supports:

* A **control plane** MCP server accepts connections
* It dispatches calls to worker nodes (Lambdas)
* Workers generate responses asynchronously and return them via the control plane

Reference:
[https://github.com/modelcontextprotocol/go-sdk/blob/main/examples/server/distributed/main.go](https://github.com/modelcontextprotocol/go-sdk/blob/main/examples/server/distributed/main.go)

This architecture is modeled after the streaming HTTP spec:
[https://modelcontextprotocol.io/specification/2025-06-18/basic/transports#streamable-http](https://modelcontextprotocol.io/specification/2025-06-18/basic/transports#streamable-http)

But constructing this using Lambda + API Gateway is highly non-trivial.



# **4. Routing Problems: JSON-RPC Inside API Gateway**

MCP uses **JSON-RPC 2.0**.
API Gateway routes based on:

* path
* method
* headers

But **not** arbitrary JSON fields in the request body.
Thus:

* You cannot route based on `"method": "tools/list"`
* You cannot use different integrations per tool
* WebSockets are incompatible with most MCP clients

This forces a serverless architecture where:

### **One endpoint must dispatch all MCP methods internally.**

Resulting pattern:

1. API Gateway → Single `POST /jsonrpc` Lambda
2. Lambda parses JSON-RPC payload
3. Dispatcher looks up method → handler
4. Handler is:

   * a separate Lambda (with specific IAM role), or
   * an internal Go function

This preserves least-privilege security while enabling modular “tool” implementations.



# **5. EventBridge Is Tempting but Not Usable Synchronously**

EventBridge *can* route on JSON fields (unlike API Gateway).
You could match:

```
detail.method == "tools/list"
```

However:

* EventBridge is **asynchronous only**
* You cannot wait synchronously for the result
* MCP requires synchronous JSON-RPC responses

Possible patterns:

* Step Functions with callback/task tokens
* A “wait for callback” state machine
* But this violates MCP’s synchronous request-response semantics

Therefore **EventBridge cannot be exposed to MCP clients**.



# **6. Reverse Proxies Would Solve Everything — But Aren’t Allowed**

Running your own L7 reverse proxy (Traefik, NGINX+Lua, custom Go/Node proxy) in ECS/Fargate would allow:

* Arbitrary body-based routing
* Custom WWW-Authenticate challenges
* Streaming multiparts
* OAuth 2.1 endpoints

But if the architecture requires **pure serverless (no always-on compute)**, ECS/Fargate is off the table.



# **7. Coordinating Distributed Lambdas for MCP Workers**

Each MCP method may require different IAM permissions.
Thus, mapping each tool to its own Lambda is desirable:

* `"fs/readFile"` Lambda reads S3
* `"aws/ec2/listInstances"` Lambda needs EC2 Describe permissions
* `"secrets/get"` Lambda requires SecretsManager read

But Lambda-to-Lambda dispatch is blocking and slow.
And distributing MCP workloads means:

1. Control plane Lambda receives the request
2. Dispatches to worker Lambda
3. Worker returns result
4. Control plane wraps JSON-RPC response

This resembles a lightweight RPC mesh inside the serverless environment.



# **8. Summary: Why Serverless MCP Is Difficult**

Building an MCP server on AWS Lambda/API Gateway is challenging because:

### **Protocol Expectations**

* OAuth 2.1 resource metadata
* Middleware-controlled WWW-Authenticate headers
* Streaming and multi-part responses
* Persistent session mechanics

### **AWS Limitations**

* API Gateway cannot route based on JSON body
* Cannot inject custom 401 headers
* No long-lived connections
* EventBridge is async-only
* Cognito lacks OAuth 2.1 protected resource support
* No built-in dynamic client discovery endpoints

### **Architectural Constraints**

* Must consolidate all MCP calls into a single Lambda dispatcher
* Must implement OAuth 2.1 flows manually
* Must orchestrate distributed workers for streaming functionality
* Must maintain IAM isolation per tool without a proxy tier



# **Conclusion**

A fully MCP-compliant, fully serverless implementation is possible—but it demands non-trivial engineering to compensate for missing features in AWS serverless primitives. You must:

* Implement OAuth 2.1 flows yourself
* Build a control-plane proxy Lambda
* Perform dynamic client discovery
* Route JSON-RPC internally (not at the gateway)
* Use distributed worker Lambdas to emulate MCP streaming
* Stitch together synchronous responses without breaking serverless execution limits

The end result is an elegant but complex architecture that stretches AWS Lambda, API Gateway, and Cognito far beyond their default capabilities—reinforcing how opinionated cloud-native serverless stacks can conflict with modern application protocols like MCP.

If your goal is interoperability with all MCP clients while keeping a strict serverless mandate, the design patterns explored in `cloudmcp` represent one of the few viable paths forward.


```
                       ┌───────────────────────────────────────────┐
                       │               MCP CLIENT                  │
                       │       (ChatGPT, IDE plugin, etc.)         │
                       └───────────────────────────────┬───────────┘
                                                       │ JSON-RPC over HTTPS
                                                       ▼
                         ┌────────────────────────────────────────┐
                         │          API GATEWAY (REST)            │
                         │   Single route: POST /jsonrpc          │
                         │   • No body-based routing              │
                         │   • Pass-through for all methods       │
                         └────────────────────────┬───────────────┘
                                                  │
                                  All requests -> │
                                                  ▼
                    ┌──────────────────────────────────────────────────┐
                    │      LAMBDA: MCP CONTROL PLANE / PROXY           │
                    │--------------------------------------------------│
                    │  Responsibilities:                               │
                    │   • Validate Bearer token (OAuth2.1)             │
                    │   • If missing/invalid → return MCP-compliant    │
                    │        401 with WWW-Authenticate header          │
                    │   • Serve /.well-known/oauth-protected-resource  │
                    │   • Implement dynamic client discovery           │
                    │   • Parse JSON-RPC (method, params, id)          │
                    │   • Dispatch to appropriate MCP worker Lambda    │
                    │   • Wrap worker output into JSON-RPC response    │
                    └───────────────┬──────────────────────────────────┘
                                    │
                 JSON-RPC internal  │ dispatch call (Lambda invoke)
                                    ▼
                ┌───────────────────────────────────────────────────────┐
                │           LAMBDA: MCP WORKER FUNCTIONS                │
                │-------------------------------------------------------│
                │ Each tool/method is isolated in its own Lambda:       │
                │                                                       │
                │   ┌─────────────────────────────────────────────┐     │
                │   │  λ tools/list                               │     │
                │   │  • Lists tools supported by this server     │     │
                │   │  • Minimal IAM                              │     │
                │   └─────────────────────────────────────────────┘     │
                │                                                       │
                │   ┌─────────────────────────────────────────────┐     │
                │   │  λ fs/readFile (S3)                         │     │
                │   │  • IAM: s3:GetObject                        │     │
                │   └─────────────────────────────────────────────┘     │
                │                                                       │
                │   ┌─────────────────────────────────────────────┐     │
                │   │  λ aws/ec2/listInstances                    │     │
                │   │  • IAM: ec2:Describe*                       │     │
                │   └─────────────────────────────────────────────┘     │
                │                                                       │
                │   (etc … each MCP tool = isolated Lambda)             │
                └───────────────┬───────────────────────────────────────┘
                                │
                 Worker returns │ result/error JSON
                                ▼
                    ┌────────────────────────────────────────────────┐
                    │  LAMBDA: MCP CONTROL PLANE                     │
                    │  • Wrap result into JSON-RPC response          │
                    │  • Maintain consistency for streaming/async    │
                    └─────────────────────────┬──────────────────────┘
                                              │
                                              ▼
                           ┌───────────────────────────────────────────┐
                           │               MCP CLIENT                  │
                           │         Receives JSON-RPC response        │
                           └───────────────────────────────────────────┘

```

```
                ┌────────────────────────────────────────┐
                │ MCP CONTROL PLANE / PROXY LAMBDA       │
                │----------------------------------------│
                │ if no Bearer token:                    │
                │   return:                              │
                │     401 Unauthorized                   │
                │     WWW-Authenticate: Bearer …         │
                │                                        │
                │ if GET /.well-known/oauth-protected-resource:│
                │   return RFC9728 metadata              │
                │                                        │
                │ if token present:                      │
                │   • fetch JWKS from Cognito            │
                │   • verify signature & claims          │
                │   • allow MCP execution                │
                └────────────────────────────────────────┘
```