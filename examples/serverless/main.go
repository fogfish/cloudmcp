//
// Copyright (C) 2025 Dmitry Kolesnikov
//
// This file may be modified and distributed under the terms
// of the MIT license.  See the LICENSE file for details.
// https://github.com/fogfish/cloudmcp
//

package main

import (
	"github.com/aws/aws-cdk-go/awscdk/v2"
	"github.com/aws/aws-cdk-go/awscdk/v2/awslambda"
	"github.com/aws/jsii-runtime-go"
	"github.com/fogfish/cloudmcp"
	"github.com/fogfish/scud"
)

func main() {
	app := awscdk.NewApp(nil)

	stack := awscdk.NewStack(app, jsii.String("cloudmcp-serverless"), nil)

	// the host application's conventions of functions and log groups
	scud.SetDefaults(stack,
		&scud.Defaults{LogGroupPrefix: "/cloudmcp-serverless"},
	)

	gateway := scud.NewGateway(stack, jsii.String("Gateway"),
		&scud.GatewayProps{
			Cors: cloudmcp.Cors("http://localhost:6274"),
		},
	)
	awscdk.NewCfnOutput(stack, jsii.String("Host"),
		&awscdk.CfnOutputProps{Value: gateway.RestAPI.ApiEndpoint()},
	)

	// worker functions, a tool per function with own IAM role
	sayer := scud.NewFunctionGo(stack, jsii.String("Sayer"),
		&scud.FunctionGoProps{
			SourceCodeModule: "github.com/fogfish/cloudmcp",
			SourceCodeLambda: "examples/serverless/cmd/sayer",
		},
	)

	clock := scud.NewFunctionGo(stack, jsii.String("Clock"),
		&scud.FunctionGoProps{
			SourceCodeModule: "github.com/fogfish/cloudmcp",
			SourceCodeLambda: "examples/serverless/cmd/clock",
		},
	)

	// the single MCP endpoint aggregating tools of workers
	cloudmcp.NewDispatcher(stack, jsii.String("Dispatcher"),
		&cloudmcp.DispatcherProps{
			Name:             "serverless",
			Route:            "/mcp",
			MetricsNamespace: "cloudmcp/serverless",
			Workers:          []awslambda.IFunction{sayer, clock},
		},
	).AllowAccessApiKey(
		gateway.NewAuthorizerBasic("access", "secret"),
	)

	app.Synth(nil)
}
