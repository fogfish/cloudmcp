//
// Copyright (C) 2025 Dmitry Kolesnikov
//
// This file may be modified and distributed under the terms
// of the MIT license.  See the LICENSE file for details.
// https://github.com/fogfish/cloudmcp
//

package main

import (
	"github.com/fogfish/cloudmcp"
)

func main() {
	cloudmcp.New("examples/helloworld/lambda").
		Name("helloworld").
		Hostless().
		AccessApiKey("access", "secret").
		Build()
}
