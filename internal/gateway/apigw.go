//
// Copyright (C) 2025 Dmitry Kolesnikov
//
// This file may be modified and distributed under the terms
// of the MIT license.  See the LICENSE file for details.
// https://github.com/fogfish/cloudmcp
//

package gateway

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"strings"

	"github.com/aws/aws-lambda-go/events"
)

// NewHttpRequest converts API Gateway proxy request to standard HTTP request
func NewHttpRequest(ctx context.Context, r *events.APIGatewayProxyRequest) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, r.HTTPMethod, r.Path, requestBody(r))
	if err != nil {
		return nil, err
	}

	for header, value := range r.Headers {
		req.Header.Set(header, value)
	}

	q := req.URL.Query()
	for key, val := range r.QueryStringParameters {
		q.Add(key, val)
	}
	req.URL.RawQuery = q.Encode()

	return req, nil
}

func requestBody(r *events.APIGatewayProxyRequest) io.ReadCloser {
	reader := strings.NewReader(r.Body)

	if r.IsBase64Encoded {
		return io.NopCloser(
			base64.NewDecoder(base64.StdEncoding, reader),
		)
	}

	return io.NopCloser(reader)
}

type ResponseWriter interface {
	http.ResponseWriter
	Value() *events.APIGatewayProxyResponse
}

// Create HTTP response writer compatible with API Gateway
func NewHttpResponse() ResponseWriter {
	return &writer{
		head: http.Header{},
	}
}

type writer struct {
	code int
	head http.Header
	wbuf bytes.Buffer
}

func (w *writer) WriteHeader(statusCode int) {
	w.code = statusCode
}

func (w *writer) Write(b []byte) (int, error) {
	return w.wbuf.Write(b)
}

func (w *writer) Header() http.Header { return w.head }

func (w *writer) Value() *events.APIGatewayProxyResponse {
	code := 200
	if w.code != 0 {
		code = w.code
	}

	return &events.APIGatewayProxyResponse{
		StatusCode:        code,
		MultiValueHeaders: w.head,
		Body:              w.wbuf.String(),
	}
}
