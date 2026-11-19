//
// Copyright (C) 2025 Dmitry Kolesnikov
//
// This file may be modified and distributed under the terms
// of the MIT license.  See the LICENSE file for details.
// https://github.com/fogfish/cloudmcp
//

// MCP server function used by tests of constructs
package main

import (
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/fogfish/cloudmcp/serve"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "v0.0.0"}, nil)
	lambda.Start(serve.Handler(serve.Static(server)))
}
