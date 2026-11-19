//
// Copyright (C) 2025 Dmitry Kolesnikov
//
// This file may be modified and distributed under the terms
// of the MIT license.  See the LICENSE file for details.
// https://github.com/fogfish/cloudmcp
//

// The dispatcher function deployed by cloudmcp.NewDispatcher.
package main

import (
	"context"
	"os"
	"strings"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/config"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/fogfish/cloudmcp/dispatch"
	"github.com/fogfish/cloudmcp/serve"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	ctx := context.Background()

	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		panic(err)
	}

	server, err := dispatch.New(ctx,
		dispatch.Config{
			Implementation: &mcp.Implementation{
				Name:    os.Getenv(dispatch.EnvName),
				Version: os.Getenv(dispatch.EnvVersion),
			},
			Workers: strings.Split(os.Getenv(dispatch.EnvWorkers), ","),
			Client:  awslambda.NewFromConfig(cfg),
		},
	)
	if err != nil {
		panic(err)
	}

	opts := []serve.Option{}
	if ns := os.Getenv(dispatch.EnvMetrics); ns != "" {
		opts = append(opts, serve.WithMetrics(ns))
	}

	lambda.Start(serve.Handler(serve.Static(server), opts...))
}
