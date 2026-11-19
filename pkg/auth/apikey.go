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

// NewTransportApiKey creates MCP transport with API Key authentication,
// sending access and secret keys as standard (RFC 7617) Basic credentials.
func NewTransportApiKey(spec ConfigApiKey) (*mcp.StreamableClientTransport, error) {
	if len(spec.Url) == 0 {
		return nil, errors.New("missing URL config")
	}

	digest := base64.StdEncoding.EncodeToString([]byte(spec.Access + ":" + spec.Secret))

	return newTransport(spec.Url, spec.Client, "Basic "+digest), nil
}

// Configure Bearer token authentication for MCP client
type ConfigBearer struct {
	// Endpoint URL of MCP server
	Url string

	// Bearer token (e.g. API key issued as bearer token)
	Token string

	// Custom HTTP client (if nil, default client will be used)
	Client *http.Client
}

// NewTransportBearer creates MCP transport with Bearer token authentication.
func NewTransportBearer(spec ConfigBearer) (*mcp.StreamableClientTransport, error) {
	if len(spec.Url) == 0 {
		return nil, errors.New("missing URL config")
	}

	if len(spec.Token) == 0 {
		return nil, errors.New("missing token config")
	}

	return newTransport(spec.Url, spec.Client, "Bearer "+spec.Token), nil
}

func newTransport(url string, client *http.Client, authorization string) *mcp.StreamableClientTransport {
	sock := &headerTransport{
		authorization: authorization,
		socket:        http.DefaultTransport,
	}

	if client != nil && client.Transport != nil {
		sock.socket = client.Transport
	}

	if client == nil {
		client = &http.Client{}
	}
	client.Transport = sock

	return &mcp.StreamableClientTransport{
		Endpoint:   url,
		HTTPClient: client,
	}
}

type headerTransport struct {
	authorization string
	socket        http.RoundTripper
}

func (api *headerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// RoundTrip must not modify the request
	req = req.Clone(req.Context())
	req.Header.Set("Authorization", api.authorization)
	return api.socket.RoundTrip(req)
}
