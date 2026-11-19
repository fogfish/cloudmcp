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
	"context"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/fogfish/cloudmcp/serve"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type Input struct{}

type Output struct {
	Time string `json:"time" jsonschema:"current time (RFC 3339)"`
}

func Now(context.Context, *mcp.CallToolRequest, Input) (*mcp.CallToolResult, Output, error) {
	return nil, Output{Time: time.Now().UTC().Format(time.RFC3339)}, nil
}

func main() {
	lambda.Start(
		serve.Handler(
			serve.Tool(
				&mcp.Tool{
					Name:        "now",
					Description: "tells the current time",
					Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
				},
				Now,
			),
		),
	)
}
