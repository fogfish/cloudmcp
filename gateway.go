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
	"os"
	"path"
	"runtime/debug"
	"strings"

	"github.com/aws/aws-cdk-go/awscdk/v2"
	"github.com/aws/aws-cdk-go/awscdk/v2/awslambda"
	"github.com/aws/aws-cdk-go/awscdk/v2/awslogs"
	"github.com/aws/jsii-runtime-go"
	"github.com/fogfish/scud"
)

// Gateway is the high-level builder of standalone MCP server deployment:
// a stack with API Gateway, authorizer and the server function.
//
//	cloudmcp.New("cmd/mcp").
//		Hostless().
//		AccessApiKey("access", "secret").
//		Build()
//
// Use NewServer within an existing CDK application instead.
type Gateway struct {
	lambda  string
	module  string
	name    string
	route   string
	gwprops *scud.GatewayProps
	cors    []string
	fprops  *awslambda.FunctionProps

	access   func(*Server, *scud.Gateway, awscdk.Stack)
	metadata bool
}

// New creates the builder for MCP server, whose main package is at the given
// path relative to the main module (e.g. "cmd/mcp"). The main is written by
// the project:
//
//	func main() {
//		lambda.Start(serve.Handler(server.New))
//	}
func New(lambda string) *Gateway {
	return &Gateway{
		lambda: strings.Trim(lambda, "/"),
		name:   path.Base(lambda),
	}
}

// Module of the server's source code, default is the main module of
// the CDK application.
func (c *Gateway) Module(module string) *Gateway {
	c.module = module
	return c
}

// Name of the stack, default is the base name of the server's path.
// Logs are written to the log group /app/{name}.
func (c *Gateway) Name(name string) *Gateway {
	c.name = name
	return c
}

// Route of the server, default is /mcp (see ServerProps.Route).
func (c *Gateway) Route(route string) *Gateway {
	c.route = route
	return c
}

// Function configures properties of the server function (e.g. memory,
// environment). The timeout must be shorter than 30 seconds.
func (c *Gateway) Function(props *awslambda.FunctionProps) *Gateway {
	c.fprops = props
	return c
}

// Hostless configures gateway without custom domain, the default API Gateway
// host is used.
func (c *Gateway) Hostless() *Gateway {
	c.gwprops = &scud.GatewayProps{}
	return c
}

// Host configures gateway with custom domain and TLS certificate ARN.
func (c *Gateway) Host(host, certificate string) *Gateway {
	c.gwprops = &scud.GatewayProps{
		Host:   jsii.String(host),
		TlsArn: jsii.String(certificate),
	}
	return c
}

// Gateway configures gateway with custom properties.
func (c *Gateway) Gateway(props *scud.GatewayProps) *Gateway {
	c.gwprops = props
	return c
}

// Cors enables CORS for browser MCP clients from the origins,
// allowing MCP protocol headers.
func (c *Gateway) Cors(origins ...string) *Gateway {
	c.cors = origins
	return c
}

// AccessPublic configures public access (no authentication).
func (c *Gateway) AccessPublic() *Gateway {
	c.access = func(s *Server, gw *scud.Gateway, _ awscdk.Stack) {
		s.AllowAccessPublic(gw.NewAuthorizerPublic())
	}
	return c
}

// AccessApiKey configures API key access, using basic authentication with
// access and secret keys.
func (c *Gateway) AccessApiKey(access, secret string) *Gateway {
	c.access = func(s *Server, gw *scud.Gateway, _ awscdk.Stack) {
		s.AllowAccessApiKey(gw.NewAuthorizerBasic(access, secret))
	}
	return c
}

// AccessAwsCognito configures AWS Cognito access, using given user pool ARN
// and optional list of app clients.
func (c *Gateway) AccessAwsCognito(cognitoArn string, clients ...string) *Gateway {
	c.access = func(s *Server, gw *scud.Gateway, _ awscdk.Stack) {
		s.AllowAccessJWT(gw.NewAuthorizerCognito(cognitoArn, clients...))
	}
	return c
}

// AccessJWT configures JWT access, using given issuer and optional list of
// audiences.
func (c *Gateway) AccessJWT(issuer string, audience ...string) *Gateway {
	c.access = func(s *Server, gw *scud.Gateway, _ awscdk.Stack) {
		s.AllowAccessJWT(gw.NewAuthorizerJwt(issuer, audience...))
	}
	return c
}

// AccessLambda configures the project's own Lambda authorizer, whose main
// package is at the given path relative to the module (use package
// github.com/fogfish/scud/authorizer to implement it).
func (c *Gateway) AccessLambda(lambda string, props *scud.AuthorizerLambdaProps) *Gateway {
	c.access = func(s *Server, gw *scud.Gateway, stack awscdk.Stack) {
		f := scud.NewFunctionGo(stack, jsii.String("Authorizer"),
			&scud.FunctionGoProps{
				SourceCodeModule: c.module,
				SourceCodeLambda: strings.Trim(lambda, "/"),
			},
		)
		s.AllowAccessLambda(gw.NewAuthorizerLambda(f, props))
	}
	return c
}

// ResourceMetadata routes protected resource metadata (RFC 9728) to the server
// as public routes. The server enables it with serve.WithResourceServer.
func (c *Gateway) ResourceMetadata() *Gateway {
	c.metadata = true
	return c
}

// Stack defines the stack of the server within the application.
func (c *Gateway) Stack(app awscdk.App) awscdk.Stack {
	if c.access == nil {
		panic(fmt.Errorf("cloudmcp: no access is defined for server %s", c.lambda))
	}

	if c.module == "" {
		c.module = mainModule()
	}

	stack := awscdk.NewStack(app, jsii.String(c.name),
		&awscdk.StackProps{
			Env: &awscdk.Environment{
				Account: getenv("CDK_DEFAULT_ACCOUNT"),
				Region:  getenv("CDK_DEFAULT_REGION"),
			},
		},
	)

	scud.SetDefaults(stack,
		&scud.Defaults{
			LogGroup: awslogs.NewLogGroup(stack, jsii.String("Logs"),
				&awslogs.LogGroupProps{
					LogGroupName:  jsii.String(fmt.Sprintf("/app/%s", c.name)),
					RemovalPolicy: awscdk.RemovalPolicy_DESTROY,
					Retention:     awslogs.RetentionDays_FIVE_DAYS,
				},
			),
		},
	)

	gwprops := c.gwprops
	if gwprops == nil {
		gwprops = &scud.GatewayProps{}
	}
	if len(c.cors) > 0 {
		props := *gwprops
		props.Cors = Cors(c.cors...)
		gwprops = &props
	}
	gateway := scud.NewGateway(stack, jsii.String("Gateway"), gwprops)

	server := NewServer(stack, jsii.String("Server"),
		&ServerProps{
			FunctionGoProps: &scud.FunctionGoProps{
				SourceCodeModule: c.module,
				SourceCodeLambda: c.lambda,
				FunctionProps:    c.fprops,
			},
			Route: c.route,
		},
	)

	c.access(server, gateway, stack)

	if c.metadata {
		server.ServeResourceMetadata(gateway.NewAuthorizerPublic())
	}

	awscdk.NewCfnOutput(stack, jsii.String("Host"),
		&awscdk.CfnOutputProps{Value: gateway.RestAPI.ApiEndpoint()},
	)
	awscdk.NewCfnOutput(stack, jsii.String("Route"),
		&awscdk.CfnOutputProps{Value: jsii.String(server.Route)},
	)

	return stack
}

// Build defines and synthesizes the application.
func (c *Gateway) Build() {
	app := awscdk.NewApp(nil)
	c.Stack(app)
	app.Synth(nil)
}

// environment variable, nil if undefined (environment-agnostic stack)
func getenv(key string) *string {
	if v := os.Getenv(key); v != "" {
		return jsii.String(v)
	}
	return nil
}

func mainModule() string {
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Path != "" {
		return info.Main.Path
	}
	panic(fmt.Errorf("cloudmcp: unable to discover main module, use Module(...)"))
}
