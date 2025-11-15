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
	"path/filepath"
	"reflect"
	"runtime"
	"strings"

	"github.com/aws/aws-cdk-go/awscdk/v2"
	"github.com/aws/aws-cdk-go/awscdk/v2/awslambda"
	"github.com/aws/aws-cdk-go/awscdk/v2/awslogs"
	"github.com/aws/jsii-runtime-go"
	"github.com/fogfish/scud"
)

// Serverless MCP Gateway, wraps MCP Server as defined by official Go SDK
// into AWS API Gateway and Lambda deployment.
type Gateway struct {
	f        Factory
	app      awscdk.App
	stack    awscdk.Stack
	loggroup awslogs.LogGroup

	gateway *scud.Gateway
	authpub *scud.AuthorizerPublic
	authkey *scud.AuthorizerBasic
	authjwt *scud.AuthorizerJwt
}

// Creates new Gateway builder for given MCP Server factory
func New(f Factory) *Gateway {
	name := servername(f)

	c := &Gateway{f: f}
	c.app = awscdk.NewApp(nil)

	c.stack = awscdk.NewStack(c.app, jsii.String(name),
		&awscdk.StackProps{
			Env: &awscdk.Environment{
				Account: jsii.String(os.Getenv("CDK_DEFAULT_ACCOUNT")),
				Region:  jsii.String(os.Getenv("CDK_DEFAULT_REGION")),
			},
		},
	)

	c.loggroup = awslogs.NewLogGroup(c.stack, jsii.String("Logs"),
		&awslogs.LogGroupProps{
			LogGroupName:  jsii.String(fmt.Sprintf("/app/%s", name)),
			RemovalPolicy: awscdk.RemovalPolicy_DESTROY,
			Retention:     awslogs.RetentionDays_FIVE_DAYS,
		},
	)

	return c
}

// Configures gateway without custom domain, default API Gateway host will be used.
func (c *Gateway) Hostless() *Gateway {
	c.gateway = scud.NewGateway(c.stack, jsii.String("Gateway"),
		&scud.GatewayProps{},
	)

	return c
}

// Configures gateway with custom domain and TLS certificate ARN.
func (c *Gateway) Host(host, certificate string) *Gateway {
	c.gateway = scud.NewGateway(c.stack, jsii.String("Gateway"),
		&scud.GatewayProps{
			Host:   jsii.String(host),
			TlsArn: jsii.String(certificate),
		},
	)

	return c
}

// Configures gateway with custom properties.
func (c *Gateway) Gateway(props *scud.GatewayProps) *Gateway {
	c.gateway = scud.NewGateway(c.stack, jsii.String("Gateway"), props)
	return c
}

// Configures gateway with public access (no authentication).
func (c *Gateway) AccessPublic() *Gateway {
	c.authpub = c.gateway.NewAuthorizerPublic()
	return c
}

// Configures gateway with API Key access, using basic digest
// authentication with access and secret keys.
func (c *Gateway) AccessApiKey(access, secret string) *Gateway {
	c.authkey = c.gateway.NewAuthorizerBasic(access, secret)
	return c
}

// Configures gateway with AWS Cognito access, using given user pool ARN
// and optional list of app clients.
func (c *Gateway) AccessAwsCognito(cognitoArn string, clients ...string) *Gateway {
	c.authjwt = c.gateway.NewAuthorizerCognito(cognitoArn, clients...)
	return c
}

// Configures gateway with JWT access, using given issuer and optional
// list of audiences.
func (c *Gateway) AccessJWT(issuer string, audience ...string) *Gateway {
	c.authjwt = c.gateway.NewAuthorizerJwt(issuer, audience...)

	c.authpub = c.gateway.NewAuthorizerPublic()

	f := scud.NewFunctionGo(c.stack, jsii.String("JWKS"),
		&scud.FunctionGoProps{
			SourceCodeModule: "github.com/fogfish/cloudmcp",
			SourceCodeLambda: "/internal/oauth2",
			FunctionProps: &awslambda.FunctionProps{
				LogGroup: c.loggroup,
				Timeout:  awscdk.Duration_Minutes(jsii.Number(5)),
			},
		},
	)

	c.authpub.AddResource("/oauth2", f)

	return c
}

func (c *Gateway) Build() {
	if c.gateway == nil {
		c.Hostless()
	}

	module, lambda := sourcecode(c.f)
	server := NewServer(c.stack, jsii.String(filepath.Base(lambda)),
		NewServerProps(c.f, &scud.FunctionGoProps{
			SourceCodeModule: module,
			SourceCodeLambda: lambda,
			FunctionProps: &awslambda.FunctionProps{
				LogGroup: c.loggroup,
				Timeout:  awscdk.Duration_Minutes(jsii.Number(5)),
			},
		}),
	)

	switch {
	case c.authjwt != nil:
		server.AllowAccessJWT(c.authjwt)
	case c.authkey != nil:
		server.AllowAccessApiKey(c.authkey)
	case c.authpub != nil:
		server.AllowAccessPublic(c.authpub)
	default:
		panic("no authorizer defined for server")
	}

	awscdk.NewCfnOutput(c.stack, jsii.String("Host"),
		&awscdk.CfnOutputProps{Value: c.gateway.RestAPI.ApiEndpoint()},
	)

	c.app.Synth(nil)
}

func servername(f any) string {
	fptr := reflect.ValueOf(f).Pointer()
	fobj := runtime.FuncForPC(fptr)
	if fobj == nil {
		panic(fmt.Errorf("failed to discover function metadata"))
	}

	name := fobj.Name()
	serv := filepath.Ext(name)[1:]

	return serv
}

func sourcecode(f any) (string, string) {
	fptr := reflect.ValueOf(f).Pointer()
	fobj := runtime.FuncForPC(fptr)
	if fobj == nil {
		panic(fmt.Errorf("failed to discover function metadata"))
	}

	name := fobj.Name()
	path := strings.TrimSuffix(name, filepath.Ext(name))

	segs := strings.Split(path, "/")
	for i := len(segs) - 1; i >= 0; i-- {
		subpath := strings.Join(segs[:i], "/")
		gomod := filepath.Join(subpath, "go.mod")
		if _, err := os.Stat(rootSourceCode(gomod)); err == nil {
			return subpath, strings.Join(segs[i:], "/")
		}
	}

	panic(fmt.Errorf("failed to go.mod for function %s", path))
}

func rootSourceCode(sourceCodeModule string) string {
	sourceCode := os.Getenv("GITHUB_WORKSPACE")
	if sourceCode == "" {
		sourceCode = filepath.Join(os.Getenv("GOPATH"), "src", sourceCodeModule)
	}

	return sourceCode
}
