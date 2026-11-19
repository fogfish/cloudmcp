//
// Copyright (C) 2025 Dmitry Kolesnikov
//
// This file may be modified and distributed under the terms
// of the MIT license.  See the LICENSE file for details.
// https://github.com/fogfish/cloudmcp
//

package cloudmcp

import "github.com/fogfish/scud"

// Headers of MCP protocol, browser clients send and read them.
var (
	CorsAllowHeaders = []string{
		"Authorization",
		"Content-Type",
		"Accept",
		"Mcp-Session-Id",
		"Mcp-Protocol-Version",
		"Mcp-Method",
		"Mcp-Name",
		"Last-Event-ID",
	}

	CorsExposeHeaders = []string{
		"Mcp-Session-Id",
		"Mcp-Protocol-Version",
		"WWW-Authenticate",
	}
)

// Cors returns CORS configuration of the gateway for browser MCP clients
// (e.g. MCP Inspector), allowing MCP protocol headers.
//
//	scud.NewGateway(stack, jsii.String("Gateway"),
//		&scud.GatewayProps{Cors: cloudmcp.Cors("https://example.com")},
//	)
func Cors(origins ...string) *scud.Cors {
	return &scud.Cors{
		AllowOrigins:  origins,
		AllowHeaders:  CorsAllowHeaders,
		ExposeHeaders: CorsExposeHeaders,
	}
}
