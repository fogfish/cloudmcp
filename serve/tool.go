//
// Copyright (C) 2025 Dmitry Kolesnikov
//
// This file may be modified and distributed under the terms
// of the MIT license.  See the LICENSE file for details.
// https://github.com/fogfish/cloudmcp
//

package serve

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Tool returns the factory of MCP server with the single typed tool. Use it
// for worker functions behind the dispatcher, one tool per function:
//
//	func main() {
//		lambda.Start(serve.Handler(
//			serve.Tool(&mcp.Tool{Name: "sayer", Description: "says hi"}, sayer.Sayer),
//		))
//	}
//
// The server's implementation name is the tool's name, the version is the
// version of the main module.
func Tool[In, Out any](tool *mcp.Tool, handler mcp.ToolHandlerFor[In, Out]) ServerFactory {
	server := mcp.NewServer(&mcp.Implementation{Name: tool.Name, Version: version()}, nil)
	mcp.AddTool(server, tool, handler)
	return Static(server)
}
