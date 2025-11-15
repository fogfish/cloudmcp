//
// Copyright (C) 2025 Dmitry Kolesnikov
//
// This file may be modified and distributed under the terms
// of the MIT license.  See the LICENSE file for details.
// https://github.com/fogfish/cloudmcp
//

package main

import (
	"os"

	"github.com/aws/aws-cdk-go/awscdk/v2"
	"github.com/aws/jsii-runtime-go"
	"github.com/fogfish/cloudmcp"
	"github.com/fogfish/cloudmcp/examples/serverless/sayer"
	"github.com/fogfish/scud"
)

func main() {
	app := awscdk.NewApp(nil)

	stack := awscdk.NewStack(app, jsii.String("CloudMCP"),
		&awscdk.StackProps{
			Env: &awscdk.Environment{
				Account: jsii.String(os.Getenv("CDK_DEFAULT_ACCOUNT")),
				Region:  jsii.String(os.Getenv("CDK_DEFAULT_REGION")),
			},
		},
	)

	gateway := scud.NewGateway(stack, jsii.String("Gateway"),
		&scud.GatewayProps{},
	)
	awscdk.NewCfnOutput(stack, jsii.String("Host"),
		&awscdk.CfnOutputProps{Value: gateway.RestAPI.ApiEndpoint()},
	)

	authroizer := gateway.NewAuthorizerBasic("access", "secret")

	f := cloudmcp.NewFunction(stack, jsii.String("Worker"),
		cloudmcp.NewFunctionProps(sayer.Sayer, "says hi",
			&scud.FunctionGoProps{
				SourceCodeModule: "github.com/fogfish/cloudmcp",
				SourceCodeLambda: "cmd/cloudmcp/sayer",
			},
		),
	)
	f.AllowAccessApiKey(authroizer)

	app.Synth(nil)
}
