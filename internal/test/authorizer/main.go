//
// Copyright (C) 2025 Dmitry Kolesnikov
//
// This file may be modified and distributed under the terms
// of the MIT license.  See the LICENSE file for details.
// https://github.com/fogfish/cloudmcp
//

// Lambda authorizer used by tests of constructs
package main

import (
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/fogfish/scud/authorizer"
)

func main() {
	lambda.Start(func(req authorizer.Request) (authorizer.Response, error) {
		return authorizer.Allow(map[string]any{"principal": "test"}), nil
	})
}
