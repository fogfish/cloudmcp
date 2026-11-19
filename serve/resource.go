//
// Copyright (C) 2025 Dmitry Kolesnikov
//
// This file may be modified and distributed under the terms
// of the MIT license.  See the LICENSE file for details.
// https://github.com/fogfish/cloudmcp
//

package serve

import (
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
)

// DefaultMetadataPath is the well-known path of protected resource metadata (RFC 9728).
const DefaultMetadataPath = "/.well-known/oauth-protected-resource"

// ResourceServer configures MCP authorization compliance: the function serves
// protected resource metadata (RFC 9728) and answers unauthenticated requests
// with 401 and "WWW-Authenticate: Bearer resource_metadata=...", so that
// OAuth-capable MCP clients discover how to authenticate.
//
// API Gateway's own 401 cannot carry the challenge, the route shall either
// be public (use WithVerifier to verify bearer tokens in the function) or be
// protected by an annotating Lambda authorizer that always allows the request
// and passes its decision in the context (see Authenticated).
type ResourceServer struct {
	// Metadata served at Path (required).
	Metadata *oauthex.ProtectedResourceMetadata

	// Path of the metadata, default is DefaultMetadataPath. Sub-paths are
	// served too (RFC 9728 §3.1, e.g. /.well-known/oauth-protected-resource/mcp).
	Path string

	// Absolute URL of the metadata used by the challenge, default is
	// https://{Host}{Path} of the request.
	URL string

	// Scopes required to access the server, advertised by the challenge.
	Scopes []string

	// Authenticated tells whether the gateway authenticated the caller,
	// default is TokenInfo with non-empty UserID. Use it with an annotating
	// authorizer, e.g. checking info.Extra["decision"].
	Authenticated func(*auth.TokenInfo) bool
}

// WithResourceServer enables MCP authorization compliance (see ResourceServer).
func WithResourceServer(rs ResourceServer) Option {
	return func(c *config) {
		if rs.Path == "" {
			rs.Path = DefaultMetadataPath
		}
		rs.Path = "/" + strings.Trim(rs.Path, "/")
		c.resource = &rs
	}
}

func (rs *ResourceServer) isMetadata(r *http.Request) bool {
	return r.URL.Path == rs.Path || strings.HasPrefix(r.URL.Path, rs.Path+"/")
}

func (rs *ResourceServer) metadataURL(r *http.Request) string {
	if rs.URL != "" {
		return rs.URL
	}
	return fmt.Sprintf("https://%s%s", r.Host, rs.Path)
}

func (rs *ResourceServer) authenticated(info *auth.TokenInfo) bool {
	if info == nil {
		return false
	}

	if rs.Authenticated != nil {
		if !rs.Authenticated(info) {
			return false
		}
	} else if info.UserID == "" {
		return false
	}

	for _, s := range rs.Scopes {
		if !slices.Contains(info.Scopes, s) {
			return false
		}
	}

	return true
}

func (rs *ResourceServer) challenge(w http.ResponseWriter, r *http.Request) {
	params := []string{fmt.Sprintf("resource_metadata=%q", rs.metadataURL(r))}
	if len(rs.Scopes) > 0 {
		params = append(params, fmt.Sprintf("scope=%q", strings.Join(rs.Scopes, " ")))
	}
	w.Header().Set("WWW-Authenticate", "Bearer "+strings.Join(params, ", "))
	http.Error(w, "unauthorized", http.StatusUnauthorized)
}

func (c *config) withResourceMetadata(next http.Handler) http.Handler {
	metadata := auth.ProtectedResourceMetadataHandler(c.resource.Metadata)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c.resource.isMetadata(r) {
			metadata.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}
