//
// Copyright (C) 2025 Dmitry Kolesnikov
//
// This file may be modified and distributed under the terms
// of the MIT license.  See the LICENSE file for details.
// https://github.com/fogfish/cloudmcp
//

// Lambda function running the MCP server
package main

import (
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/fogfish/cloudmcp/examples/helloworld/server"
	"github.com/fogfish/cloudmcp/serve"
)

func main() {
	lambda.Start(
		serve.Handler(serve.Static(server.New())),
	)
}
