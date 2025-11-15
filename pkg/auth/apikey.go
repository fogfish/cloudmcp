//
// Copyright (C) 2025 Dmitry Kolesnikov
//
// This file may be modified and distributed under the terms
// of the MIT license.  See the LICENSE file for details.
// https://github.com/fogfish/cloudmcp
//

package auth

import (
	"encoding/base64"
	"errors"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Configure API Key authentication for MCP client
type ConfigApiKey struct {
	// Endpoint URL of MCP server
	Url string

	// API Access & Secret keys
	Access, Secret string

	// Custom HTTP client (if nil, default client will be used)
	Client *http.Client
}

// NewApiKey creates MCP transport with API Key authentication.
func NewTransportApiKey(spec ConfigApiKey) (*mcp.StreamableClientTransport, error) {
	if len(spec.Url) == 0 {
		return nil, errors.New("missing URL config")
	}

	digest := base64.RawStdEncoding.EncodeToString([]byte(spec.Access + ":" + spec.Secret))

	sock := &apikeyTransport{
		digest: digest,
		socket: http.DefaultTransport,
	}

	if spec.Client != nil && spec.Client.Transport != nil {
		sock.socket = spec.Client.Transport
	}

	if spec.Client == nil {
		spec.Client = &http.Client{}
	}
	spec.Client.Transport = sock

	return &mcp.StreamableClientTransport{
		Endpoint:   spec.Url,
		HTTPClient: spec.Client,
	}, nil
}

type apikeyTransport struct {
	digest string
	socket http.RoundTripper
}

func (api *apikeyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.Header.Add("Authorization", "Basic "+api.digest)
	return api.socket.RoundTrip(req)
}
