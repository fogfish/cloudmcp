//
// Copyright (C) 2025 Dmitry Kolesnikov
//
// This file may be modified and distributed under the terms
// of the MIT license.  See the LICENSE file for details.
// https://github.com/fogfish/cloudmcp
//

// Worker function serving the single tool
package main

import (
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/fogfish/cloudmcp/examples/serverless/sayer"
	"github.com/fogfish/cloudmcp/serve"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	lambda.Start(
		serve.Handler(
			serve.Tool(&mcp.Tool{Name: "sayer", Description: "says hi"}, sayer.Sayer),
		),
	)
}
