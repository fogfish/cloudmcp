//
// Copyright (C) 2025 Dmitry Kolesnikov
//
// This file may be modified and distributed under the terms
// of the MIT license.  See the LICENSE file for details.
// https://github.com/fogfish/cloudmcp
//

package gateway

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/aws/aws-lambda-go/events"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
)

// Control plane of JSON-RPC Gateway
type Controller interface {
	ServeHTTP(w http.ResponseWriter, req *http.Request)
}

// Serverless JSON-RPC Gateway. The gateway translates AWS API Gateway calls into
// HTTP requests understood by MCP JSON-RPC server and routes them to different
// lambda functions.
type Gateway struct {
	ctrl Controller
}

// Create new JSON-RPC Serverless Gateway
func New(ctrl Controller) *Gateway {
	return &Gateway{ctrl: ctrl}
}

// Serve handles incoming API Gateway requests and routes them to MCP JSON-RPC server.
func (gw *Gateway) Serve(ctx context.Context, req *events.APIGatewayProxyRequest) (*events.APIGatewayProxyResponse, error) {
	// In the context of MCP protocol, GET implies a setup of a streaming connection,
	// which is not supported in lambda proxy.
	if req.HTTPMethod == "GET" {
		return &events.APIGatewayProxyResponse{
			StatusCode: 405,
		}, nil
	}

	if len(req.Body) == 0 {
		return gw.serveCtrl(ctx, req)
	}

	msg, err := jsonrpc.DecodeMessage([]byte(req.Body))
	if err != nil {
		slog.Error("bad json-rpc message", "err", err)
		return nil, err
	}
	slog.Debug("received json-rpc message", "msg", msg)

	// TODO: handle json-rpc switching message
	// switch v := msg.(type) {
	// case *jsonrpc.Request:
	// 	if v.Method == "tools/call" {
	// 		// TODO: return
	// 	}
	// }

	return gw.serveCtrl(ctx, req)
}

func (gw *Gateway) serveCtrl(ctx context.Context, req *events.APIGatewayProxyRequest) (*events.APIGatewayProxyResponse, error) {
	input, err := NewHttpRequest(ctx, req)
	if err != nil {
		slog.Error("bad http request", "err", err)
		return nil, err
	}

	reply := NewHttpResponse()
	gw.ctrl.ServeHTTP(reply, input)

	return reply.Value(), nil
}
