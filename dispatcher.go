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

	"github.com/aws/aws-cdk-go/awscdk/v2"
	apigw2 "github.com/aws/aws-cdk-go/awscdk/v2/awsapigatewayv2"
	"github.com/aws/aws-cdk-go/awscdk/v2/awslambda"
	"github.com/aws/constructs-go/constructs/v10"
	"github.com/aws/jsii-runtime-go"
	"github.com/fogfish/cloudmcp/dispatch"
	"github.com/fogfish/scud"
)

// DispatcherProps defines properties of the dispatcher, the single MCP server
// aggregating tools of worker functions.
type DispatcherProps struct {
	// Properties of the dispatcher function (optional).
	// The timeout defaults to 29 seconds, it must be shorter than 30 seconds.
	*awslambda.FunctionProps

	// Implementation name and version reported to clients,
	// default is the construct id and the version of cloudmcp.
	Name, Version string

	// CloudWatch namespace of request metrics (EMF), disabled if empty.
	MetricsNamespace string

	// Route of the dispatcher, default is /mcp.
	Route string

	// Payload format of the integration, default is 2.0.
	PayloadFormatVersion apigw2.PayloadFormatVersion

	// Worker functions running serve.Handler (e.g. serve.Tool).
	// Tool names must be unique across workers.
	Workers []awslambda.IFunction
}

// Dispatcher is L3 construct of MCP server, which lists tools of worker
// functions and forwards tools/call to them through Lambda Invoke. Workers
// keep their own IAM roles (least privilege per tool), clients connect to
// the dispatcher's route only. The dispatcher is a Server, grant access to
// its route with Allow* methods.
type Dispatcher struct {
	*Server
	workers []awslambda.IFunction
}

// NewDispatcher defines the dispatcher function.
func NewDispatcher(scope constructs.Construct, id *string, props *DispatcherProps) *Dispatcher {
	if props == nil {
		props = &DispatcherProps{}
	}

	name := props.Name
	if name == "" {
		name = *id
	}
	vsn := props.Version
	if vsn == "" {
		vsn = Version
	}

	fprops := withTimeout(*id, props.FunctionProps)
	env := map[string]*string{}
	if fprops.Environment != nil {
		for k, v := range *fprops.Environment {
			env[k] = v
		}
	}
	env[dispatch.EnvName] = jsii.String(name)
	env[dispatch.EnvVersion] = jsii.String(vsn)
	if props.MetricsNamespace != "" {
		env[dispatch.EnvMetrics] = jsii.String(props.MetricsNamespace)
	}
	fprops.Environment = &env

	f := scud.NewFunctionGo(scope, id,
		&scud.FunctionGoProps{
			SourceCodeModule:  "github.com/fogfish/cloudmcp",
			SourceCodeLambda:  "cmd/dispatcher",
			SourceCodeVersion: Version,
			FunctionProps:     fprops,
		},
	)

	d := &Dispatcher{
		Server: &Server{
			Route:    route(props.Route),
			Function: f,
			payload:  payload(props.PayloadFormatVersion),
		},
	}

	for _, w := range props.Workers {
		d.AddWorker(w)
	}

	return d
}

// AddWorker adds the worker function to the dispatcher, granting
// the dispatcher to invoke it.
func (d *Dispatcher) AddWorker(worker awslambda.IFunction) *Dispatcher {
	if worker == nil {
		panic(fmt.Errorf("cloudmcp: dispatcher: worker is nil"))
	}

	worker.GrantInvoke(d.Function)
	d.workers = append(d.workers, worker)

	arns := make([]*string, 0, len(d.workers))
	for _, w := range d.workers {
		arns = append(arns, w.FunctionArn())
	}
	d.Function.AddEnvironment(
		jsii.String(dispatch.EnvWorkers),
		awscdk.Fn_Join(jsii.String(","), &arns),
		nil,
	)

	return d
}
