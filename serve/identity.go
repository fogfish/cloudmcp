//
// Copyright (C) 2025 Dmitry Kolesnikov
//
// This file may be modified and distributed under the terms
// of the MIT license.  See the LICENSE file for details.
// https://github.com/fogfish/cloudmcp
//

package serve

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
)

// Identity of the caller as established by AWS API Gateway. The adapter
// builds it from the request's context, the client cannot change it.
type Identity struct {
	// Context of Lambda authorizer (payload 1.0: RequestContext.Authorizer,
	// payload 2.0: RequestContext.Authorizer.Lambda).
	Authorizer map[string]any

	// Claims and scopes of JWT (or Cognito) authorizer.
	Claims map[string]any
	Scopes []string

	// IAM principal (user ARN) of IAM authorizer.
	Principal string

	// Path parameters of the route (e.g. {instance} of /{instance}).
	PathParameters map[string]string
}

type identityKey struct{}

// IdentityFromContext returns identity established by API Gateway, nil when
// the request did not come through API Gateway.
func IdentityFromContext(ctx context.Context) *Identity {
	if id, ok := ctx.Value(identityKey{}).(*Identity); ok {
		return id
	}
	return nil
}

// WithIdentityContext attaches the identity to the context, use it for testing
// or for custom adapters.
func WithIdentityContext(ctx context.Context, id *Identity) context.Context {
	return context.WithValue(ctx, identityKey{}, id)
}

// DefaultTokenInfo maps the identity into go-sdk's auth.TokenInfo:
//
//   - UserID is the first non-empty of the authorizer context keys
//     "principal", "sub", "principalId", the JWT "sub" claim or IAM principal.
//   - Scopes are the authorizer context key "scope" or "scopes" (space
//     separated string or list), or the JWT scopes.
//   - Extra holds every key of the authorizer context, JWT claims at
//     "claims" and path parameters at "path".
//
// TokenInfo is produced for anonymous callers too, its UserID is empty.
func DefaultTokenInfo(id *Identity) *auth.TokenInfo {
	info := &auth.TokenInfo{Extra: map[string]any{}}

	for k, v := range id.Authorizer {
		info.Extra[k] = v
	}
	if len(id.Claims) > 0 {
		info.Extra["claims"] = id.Claims
	}
	if id.PathParameters != nil {
		info.Extra["path"] = id.PathParameters
	} else {
		info.Extra["path"] = map[string]string{}
	}

	info.UserID = firstString(
		id.Authorizer["principal"],
		id.Authorizer["sub"],
		id.Authorizer["principalId"],
		id.Claims["sub"],
		id.Principal,
	)

	switch {
	case id.Authorizer["scope"] != nil:
		info.Scopes = toScopes(id.Authorizer["scope"])
	case id.Authorizer["scopes"] != nil:
		info.Scopes = toScopes(id.Authorizer["scopes"])
	default:
		info.Scopes = id.Scopes
	}

	return info
}

func firstString(seq ...any) string {
	for _, x := range seq {
		if x == nil {
			continue
		}
		if s := fmt.Sprint(x); s != "" {
			return s
		}
	}
	return ""
}

func toScopes(x any) []string {
	switch v := x.(type) {
	case string:
		return strings.Fields(v)
	case []string:
		return v
	case []any:
		seq := make([]string, 0, len(v))
		for _, s := range v {
			seq = append(seq, fmt.Sprint(s))
		}
		return seq
	default:
		return strings.Fields(fmt.Sprint(v))
	}
}

//------------------------------------------------------------------------------

// TokenInfo forwarded by the dispatcher to the worker.
type forwardedKey struct{}

// the TokenInfo established for the request, consumed by the gateway verifier
type gatewayInfoKey struct{}

// the original Authorization header of the request
type authorizationKey struct{}

// The go-sdk sets TokenInfo only through auth.RequireBearerToken, which
// requires "Authorization: Bearer". The gateway has already authorized the
// request (or the route is public), the bridge passes the established
// TokenInfo through the middleware using a synthetic bearer token and
// restores the original Authorization header before the MCP server.
const bridgeToken = "cloudmcp-gateway"

func gatewayVerifier(ctx context.Context, _ string, _ *http.Request) (*auth.TokenInfo, error) {
	if info, ok := ctx.Value(gatewayInfoKey{}).(*auth.TokenInfo); ok {
		return info, nil
	}
	return nil, auth.ErrInvalidToken
}

func (c *config) withIdentity(next http.Handler) http.Handler {
	restore := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Header.Del("Authorization")
		if orig, ok := r.Context().Value(authorizationKey{}).([]string); ok {
			for _, v := range orig {
				r.Header.Add("Authorization", v)
			}
		}
		next.ServeHTTP(w, r)
	})

	bridge := auth.RequireBearerToken(gatewayVerifier,
		&auth.RequireBearerTokenOptions{AllowMissingExpiration: true},
	)(restore)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		id := IdentityFromContext(ctx)

		// bearer token is verified by the function
		if c.verifier != nil {
			c.withVerifier(id, next).ServeHTTP(w, r)
			return
		}

		var info *auth.TokenInfo
		switch {
		case ctx.Value(forwardedKey{}) != nil:
			info = ctx.Value(forwardedKey{}).(*auth.TokenInfo)
		case id != nil:
			info = c.identity(id)
		}

		if c.resource != nil && !c.resource.authenticated(info) {
			c.resource.challenge(w, r)
			return
		}

		if info == nil {
			next.ServeHTTP(w, r)
			return
		}

		if deadline, ok := ctx.Deadline(); ok && info.Expiration.IsZero() {
			info.Expiration = deadline
		}
		if info.Expiration.IsZero() {
			// the go-sdk treats the expired token as invalid
			info.Expiration = time.Now().Add(15 * time.Minute)
		}

		ctx = context.WithValue(ctx, gatewayInfoKey{}, info)
		ctx = context.WithValue(ctx, authorizationKey{}, r.Header.Values("Authorization"))
		r = r.Clone(ctx)
		r.Header.Set("Authorization", "Bearer "+bridgeToken)

		bridge.ServeHTTP(w, r)
	})
}

func (c *config) withVerifier(id *Identity, next http.Handler) http.Handler {
	verifier := c.verifier
	if id != nil {
		verifier = func(ctx context.Context, token string, req *http.Request) (*auth.TokenInfo, error) {
			info, err := c.verifier(ctx, token, req)
			if err != nil || info == nil {
				return info, err
			}
			if info.Extra == nil {
				info.Extra = map[string]any{}
			}
			if id.PathParameters != nil {
				info.Extra["path"] = id.PathParameters
			}
			return info, nil
		}
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		opts := &auth.RequireBearerTokenOptions{}
		if c.resource != nil {
			opts.ResourceMetadataURL = c.resource.metadataURL(r)
			opts.Scopes = c.resource.Scopes
		}
		auth.RequireBearerToken(verifier, opts)(next).ServeHTTP(w, r)
	})
}
