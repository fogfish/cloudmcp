//
// Copyright (C) 2025 Dmitry Kolesnikov
//
// This file may be modified and distributed under the terms
// of the MIT license.  See the LICENSE file for details.
// https://github.com/fogfish/cloudmcp
//

package server

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type Input struct {
	Name string `json:"name" jsonschema:"the name of the person saying the greeting"`
}

type Output struct {
	Greeting string `json:"greeting" jsonschema:"the greeting to tell to the user"`
	Author   string `json:"author" jsonschema:"the author of the greeting"`
}

func Sayer(ctx context.Context, req *mcp.CallToolRequest, input Input) (*mcp.CallToolResult, Output, error) {
	return nil, Output{Greeting: "Hello World!", Author: input.Name}, nil
}

func HelloWorld() (*mcp.Server, error) {
	server := mcp.NewServer(
		&mcp.Implementation{Name: "hello world", Version: "v1.0.0"},
		nil,
	)
	mcp.AddTool(server, &mcp.Tool{Name: "sayer", Description: "says Hello World!"}, Sayer)

	return server, nil
}
