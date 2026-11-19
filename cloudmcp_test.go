//
// Copyright (C) 2025 Dmitry Kolesnikov
//
// This file may be modified and distributed under the terms
// of the MIT license.  See the LICENSE file for details.
// https://github.com/fogfish/cloudmcp
//

package cloudmcp_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/aws/aws-cdk-go/awscdk/v2"
	"github.com/aws/aws-cdk-go/awscdk/v2/assertions"
	"github.com/aws/aws-cdk-go/awscdk/v2/awslambda"
	"github.com/aws/jsii-runtime-go"
	"github.com/fogfish/cloudmcp"
	"github.com/fogfish/it/v2"
	"github.com/fogfish/scud"
)

const module = "github.com/fogfish/cloudmcp"

func newStack() awscdk.Stack {
	app := awscdk.NewApp(nil)
	return awscdk.NewStack(app, jsii.String("Test"),
		&awscdk.StackProps{
			Env: &awscdk.Environment{
				Account: jsii.String("000000000000"),
				Region:  jsii.String("eu-west-1"),
			},
		},
	)
}

func newServerProps(route string) *cloudmcp.ServerProps {
	return &cloudmcp.ServerProps{
		FunctionGoProps: &scud.FunctionGoProps{
			SourceCodeModule: module,
			SourceCodeLambda: "internal/test/server",
		},
		Route: route,
	}
}

func resources(template assertions.Template, kind string) []map[string]any {
	seq := []map[string]any{}
	for _, r := range *template.FindResources(jsii.String(kind), nil) {
		spec, _ := (*r)["Properties"].(map[string]any)
		seq = append(seq, spec)
	}
	return seq
}

func routeKeys(template assertions.Template) []string {
	keys := []string{}
	for _, r := range resources(template, "AWS::ApiGatewayV2::Route") {
		keys = append(keys, r["RouteKey"].(string))
	}
	slices.Sort(keys)
	return keys
}

func panics(f func()) (ok bool) {
	defer func() { ok = recover() != nil }()
	f()
	return
}

func toJSON(x any) string {
	b, _ := json.Marshal(x)
	return string(b)
}

func TestServerRoute(t *testing.T) {
	stack := newStack()
	gw := scud.NewGateway(stack, jsii.String("Gateway"), &scud.GatewayProps{})

	authorizer := scud.NewFunctionGo(stack, jsii.String("Authorizer"),
		&scud.FunctionGoProps{SourceCodeModule: module, SourceCodeLambda: "internal/test/authorizer"},
	)

	server := cloudmcp.NewServer(stack, jsii.String("Server"), newServerProps("/{instance}"))
	server.AllowAccessLambda(gw.NewAuthorizerLambda(authorizer, nil))

	template := assertions.Template_FromStack(stack, nil)

	integrations := resources(template, "AWS::ApiGatewayV2::Integration")
	it.Then(t).
		Should(it.Equal(server.Route, "/{instance}")).
		Should(it.Seq(routeKeys(template)).Equal("DELETE /{instance}", "GET /{instance}", "POST /{instance}")).
		Should(it.Equal(len(integrations), 1)).
		Should(it.Equal(integrations[0]["PayloadFormatVersion"].(string), "2.0"))

	for _, r := range resources(template, "AWS::ApiGatewayV2::Route") {
		it.Then(t).Should(it.Equal(r["AuthorizationType"].(string), "CUSTOM"))
	}
}

func TestServerDefaultRoute(t *testing.T) {
	stack := newStack()
	gw := scud.NewGateway(stack, jsii.String("Gateway"), &scud.GatewayProps{})

	cloudmcp.NewServer(stack, jsii.String("Server"), newServerProps("")).
		AllowAccessPublic(gw.NewAuthorizerPublic())

	template := assertions.Template_FromStack(stack, nil)
	it.Then(t).
		Should(it.Seq(routeKeys(template)).Equal("DELETE /mcp", "GET /mcp", "POST /mcp"))
}

func TestServerRootRoute(t *testing.T) {
	stack := newStack()
	gw := scud.NewGateway(stack, jsii.String("Gateway"), &scud.GatewayProps{})

	cloudmcp.NewServer(stack, jsii.String("Server"), newServerProps("/")).
		AllowAccessApiKey(gw.NewAuthorizerBasic("access", "secret"))

	template := assertions.Template_FromStack(stack, nil)
	it.Then(t).
		Should(it.Seq(routeKeys(template)).Equal("DELETE /", "GET /", "POST /"))
}

func TestServerTimeout(t *testing.T) {
	stack := newStack()
	cloudmcp.NewServer(stack, jsii.String("Server"), newServerProps(""))

	template := assertions.Template_FromStack(stack, nil)
	template.HasResourceProperties(jsii.String("AWS::Lambda::Function"),
		map[string]any{"Timeout": 29},
	)
}

func TestServerTimeoutTooLong(t *testing.T) {
	stack := newStack()
	props := newServerProps("")
	props.FunctionProps = &awslambda.FunctionProps{Timeout: awscdk.Duration_Seconds(jsii.Number(30))}

	it.Then(t).Should(
		it.True(panics(func() { cloudmcp.NewServer(stack, jsii.String("Server"), props) })),
	)
}

func TestServerHostDefaults(t *testing.T) {
	stack := newStack()
	scud.SetDefaults(stack, &scud.Defaults{LogGroupPrefix: "/host/stack"})

	cloudmcp.NewServer(stack, jsii.String("Server"), newServerProps(""))

	template := assertions.Template_FromStack(stack, nil)
	groups := resources(template, "AWS::Logs::LogGroup")
	it.Then(t).
		Should(it.Equal(len(groups), 1)).
		Should(it.String(toJSON(groups[0]["LogGroupName"])).Contain("/host/stack/"))
}

func TestServerResourceMetadata(t *testing.T) {
	stack := newStack()
	gw := scud.NewGateway(stack, jsii.String("Gateway"), &scud.GatewayProps{})

	cloudmcp.NewServer(stack, jsii.String("Server"), newServerProps("")).
		AllowAccessJWT(gw.NewAuthorizerJwt("https://auth.example.com", "mcp"), "mcp:read").
		ServeResourceMetadata(gw.NewAuthorizerPublic())

	template := assertions.Template_FromStack(stack, nil)
	it.Then(t).
		Should(it.Seq(routeKeys(template)).Equal(
			"DELETE /mcp",
			"GET /.well-known/oauth-protected-resource",
			"GET /.well-known/oauth-protected-resource/{proxy+}",
			"GET /mcp",
			"POST /mcp",
		))
}

func TestDispatcher(t *testing.T) {
	stack := newStack()
	gw := scud.NewGateway(stack, jsii.String("Gateway"), &scud.GatewayProps{})

	worker := scud.NewFunctionGo(stack, jsii.String("Worker"),
		&scud.FunctionGoProps{SourceCodeModule: module, SourceCodeLambda: "internal/test/server"},
	)

	cloudmcp.NewDispatcher(stack, jsii.String("Dispatcher"),
		&cloudmcp.DispatcherProps{
			Name:             "dispatcher",
			MetricsNamespace: "test",
			Workers:          []awslambda.IFunction{worker},
		},
	).AllowAccessPublic(gw.NewAuthorizerPublic())

	template := assertions.Template_FromStack(stack, nil)

	var env string
	for _, f := range resources(template, "AWS::Lambda::Function") {
		if s := toJSON(f["Environment"]); strings.Contains(s, "CLOUDMCP_WORKERS") {
			env = s
		}
	}

	policies := toJSON(resources(template, "AWS::IAM::Policy"))

	it.Then(t).
		Should(it.String(env).Contain("CLOUDMCP_NAME")).
		Should(it.String(env).Contain("CLOUDMCP_METRICS")).
		Should(it.String(policies).Contain("lambda:InvokeFunction")).
		Should(it.Seq(routeKeys(template)).Equal("DELETE /mcp", "GET /mcp", "POST /mcp"))
}

func TestBuilder(t *testing.T) {
	app := awscdk.NewApp(nil)
	stack := cloudmcp.New("internal/test/server").
		Module(module).
		Name("builder").
		Route("/{instance}").
		Hostless().
		Cors("https://example.com").
		AccessApiKey("access", "secret").
		ResourceMetadata().
		Stack(app)

	template := assertions.Template_FromStack(stack, nil)

	groups := resources(template, "AWS::Logs::LogGroup")
	apis := toJSON(resources(template, "AWS::ApiGatewayV2::Api"))

	it.Then(t).
		Should(it.Equal(len(groups), 1)).
		Should(it.Equal(groups[0]["LogGroupName"].(string), "/app/builder")).
		Should(it.String(apis).Contain("Mcp-Session-Id")).
		Should(it.String(apis).Contain("Mcp-Protocol-Version")).
		Should(it.Seq(routeKeys(template)).Equal(
			"DELETE /{instance}",
			"GET /.well-known/oauth-protected-resource",
			"GET /.well-known/oauth-protected-resource/{proxy+}",
			"GET /{instance}",
			"POST /{instance}",
		))
}

func TestBuilderNoAccess(t *testing.T) {
	it.Then(t).Should(
		it.True(panics(func() { cloudmcp.New("internal/test/server").Module(module).Stack(awscdk.NewApp(nil)) })),
	)
}

func TestCors(t *testing.T) {
	cors := cloudmcp.Cors("https://example.com")
	it.Then(t).
		Should(it.Seq(cors.AllowOrigins).Equal("https://example.com")).
		Should(it.Seq(cors.AllowHeaders).Contain("Mcp-Session-Id")).
		Should(it.Seq(cors.ExposeHeaders).Contain("Mcp-Session-Id"))
}
