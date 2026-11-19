//
// Copyright (C) 2025 Dmitry Kolesnikov
//
// This file may be modified and distributed under the terms
// of the MIT license.  See the LICENSE file for details.
// https://github.com/fogfish/cloudmcp
//

package serve

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fogfish/it/v2"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type none struct{}

func TestMetrics(t *testing.T) {
	buf := &bytes.Buffer{}
	stdout = buf

	server := mcp.NewServer(&mcp.Implementation{Name: "t", Version: "v"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "ok"},
		func(context.Context, *mcp.CallToolRequest, none) (*mcp.CallToolResult, none, error) {
			return nil, none{}, nil
		},
	)
	mcp.AddTool(server, &mcp.Tool{Name: "fail"},
		func(context.Context, *mcp.CallToolRequest, none) (*mcp.CallToolResult, none, error) {
			return nil, none{}, context.Canceled
		},
	)

	h := HTTPHandler(Static(server),
		WithMetrics("test"),
		WithHTTPOptions(&mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true}),
	)

	post := func(body string) {
		r := httptest.NewRequest("POST", "/", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Accept", "application/json, text/event-stream")
		r.Header.Set("Mcp-Protocol-Version", "2025-06-18")
		h.ServeHTTP(httptest.NewRecorder(), r)
	}

	post(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"ok","arguments":{}}}`)
	post(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"fail","arguments":{}}}`)
	post(`{"jsonrpc":"2.0","id":3,"method":"tools/list"}`)
	post(`garbage`)

	type record struct {
		AWS struct {
			CloudWatchMetrics []struct {
				Namespace  string
				Dimensions [][]string
			}
		} `json:"_aws"`
		Method   string
		Tool     string
		Requests int
		Errors   int
	}

	var seq []record
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		var r record
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("invalid EMF %q: %v", line, err)
		}
		seq = append(seq, r)
	}

	it.Then(t).
		Should(it.Equal(len(seq), 4)).
		Should(it.Equal(seq[0].AWS.CloudWatchMetrics[0].Namespace, "test")).
		Should(it.Equal(len(seq[0].AWS.CloudWatchMetrics[0].Dimensions), 2)).
		Should(it.Equal(seq[0].Method, "tools/call")).
		Should(it.Equal(seq[0].Tool, "ok")).
		Should(it.Equal(seq[0].Errors, 0)).
		Should(it.Equal(seq[1].Tool, "fail")).
		Should(it.Equal(seq[1].Errors, 1)).
		Should(it.Equal(seq[2].Method, "tools/list")).
		Should(it.Equal(len(seq[2].AWS.CloudWatchMetrics[0].Dimensions), 1)).
		Should(it.Equal(seq[3].Method, "(invalid)")).
		Should(it.Equal(seq[3].Errors, 1))
}
