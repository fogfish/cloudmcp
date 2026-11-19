//
// Copyright (C) 2025 Dmitry Kolesnikov
//
// This file may be modified and distributed under the terms
// of the MIT license.  See the LICENSE file for details.
// https://github.com/fogfish/cloudmcp
//

package cloudmcp

import (
	"fmt"
	"strings"

	"github.com/aws/aws-cdk-go/awscdk/v2"
	apigw2 "github.com/aws/aws-cdk-go/awscdk/v2/awsapigatewayv2"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsiam"
	"github.com/aws/aws-cdk-go/awscdk/v2/awslambda"
	"github.com/aws/constructs-go/constructs/v10"
	"github.com/aws/jsii-runtime-go"
	"github.com/fogfish/scud"
)

// DefaultRoute of MCP server
const DefaultRoute = "/mcp"

// DefaultTimeout of MCP server function, shorter than the API Gateway HTTP API
// integration timeout (30 seconds).
var DefaultTimeout = awscdk.Duration_Seconds(jsii.Number(29))

// maximum integration timeout of API Gateway HTTP API
const maxTimeoutSeconds = 30

// Methods of MCP route. The function answers GET and DELETE with 405
// (stateless servers do not support event streams and sessions).
var methods = []apigw2.HttpMethod{
	apigw2.HttpMethod_POST,
	apigw2.HttpMethod_GET,
	apigw2.HttpMethod_DELETE,
}

// ServerProps defines properties of MCP server as Lambda function.
// The function's main is written by the project:
//
//	func main() {
//		lambda.Start(serve.Handler(server.New))
//	}
type ServerProps struct {
	// The function, built from SourceCodeModule/SourceCodeLambda.
	// The timeout defaults to 29 seconds, it must be shorter than 30 seconds.
	// The log group and other defaults follow scud.SetDefaults of the scope.
	*scud.FunctionGoProps

	// Route of the server (e.g. /mcp, /{instance} or /), default is /mcp.
	// The route may contain path parameters, they are available to tools at
	// req.Extra.TokenInfo.Extra["path"].
	Route string

	// Payload format of the integration, default is 2.0.
	// The function accepts both 1.0 and 2.0.
	PayloadFormatVersion apigw2.PayloadFormatVersion
}

// Server is L3 construct of MCP server as AWS Lambda function.
type Server struct {
	Route    string
	Function awslambda.Function
	payload  apigw2.PayloadFormatVersion
}

// NewServer defines MCP server as Lambda function.
func NewServer(scope constructs.Construct, id *string, props *ServerProps) *Server {
	if props == nil || props.FunctionGoProps == nil {
		panic(fmt.Errorf("cloudmcp: server %s: FunctionGoProps is required", *id))
	}

	spec := *props.FunctionGoProps
	spec.FunctionProps = withTimeout(*id, spec.FunctionProps)

	return &Server{
		Route:    route(props.Route),
		Function: scud.NewFunctionGo(scope, id, &spec),
		payload:  payload(props.PayloadFormatVersion),
	}
}

func (c *Server) props() *scud.RouteProps {
	return &scud.RouteProps{
		Handler:              c.Function,
		Methods:              methods,
		PayloadFormatVersion: c.payload,
	}
}

// AllowAccessPublic grants public access to the server.
func (c *Server) AllowAccessPublic(api *scud.AuthorizerPublic) *Server {
	api.AddRoute(c.Route, c.props())
	return c
}

// AllowAccessApiKey grants access to the server via API key authorizer.
func (c *Server) AllowAccessApiKey(api *scud.AuthorizerBasic) *Server {
	api.AddRoute(c.Route, c.props())
	return c
}

// AllowAccessJWT grants access to the server via JWT (or Cognito) authorizer,
// requiring the given OAuth scopes.
func (c *Server) AllowAccessJWT(api *scud.AuthorizerJwt, scope ...string) *Server {
	props := c.props()
	props.AuthorizationScopes = scope
	api.AddRoute(c.Route, props)
	return c
}

// AllowAccessIAM grants access to the server via IAM authorizer for the principal.
func (c *Server) AllowAccessIAM(api *scud.AuthorizerIAM, principal awsiam.IGrantable) *Server {
	props := c.props()
	props.Grantee = principal
	api.AddRoute(c.Route, props)
	return c
}

// AllowAccessLambda grants access to the server via the project's own Lambda
// authorizer (scud.Gateway.NewAuthorizerLambda). The authorizer's context is
// available to tools at req.Extra.TokenInfo (see serve.DefaultTokenInfo).
func (c *Server) AllowAccessLambda(api *scud.AuthorizerLambda) *Server {
	api.AddRoute(c.Route, c.props())
	return c
}

// ServeResourceMetadata routes the protected resource metadata (RFC 9728) to
// the server as public routes, see serve.WithResourceServer.
func (c *Server) ServeResourceMetadata(api *scud.AuthorizerPublic) *Server {
	for _, path := range []string{MetadataPath, MetadataPath + "/{proxy+}"} {
		api.AddRoute(path,
			&scud.RouteProps{
				Handler:              c.Function,
				Methods:              []apigw2.HttpMethod{apigw2.HttpMethod_GET},
				PayloadFormatVersion: c.payload,
			},
		)
	}
	return c
}

// MetadataPath is the well-known path of protected resource metadata.
const MetadataPath = "/.well-known/oauth-protected-resource"

//------------------------------------------------------------------------------

func route(r string) string {
	if r == "" {
		return DefaultRoute
	}
	if !strings.HasPrefix(r, "/") {
		r = "/" + r
	}
	return r
}

func payload(v apigw2.PayloadFormatVersion) apigw2.PayloadFormatVersion {
	if v == nil {
		return apigw2.PayloadFormatVersion_VERSION_2_0()
	}
	return v
}

func withTimeout(id string, props *awslambda.FunctionProps) *awslambda.FunctionProps {
	var spec awslambda.FunctionProps
	if props != nil {
		spec = *props
	}

	if spec.Timeout == nil {
		spec.Timeout = DefaultTimeout
	}

	if sec, ok := seconds(spec.Timeout); ok && sec >= maxTimeoutSeconds {
		panic(fmt.Errorf("cloudmcp: function %s: timeout %vs must be shorter than API Gateway integration timeout (%ds)", id, sec, maxTimeoutSeconds))
	}

	return &spec
}

// seconds of the duration, false if the duration is unresolved token
func seconds(d awscdk.Duration) (sec float64, ok bool) {
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	return *d.ToSeconds(nil), true
}
