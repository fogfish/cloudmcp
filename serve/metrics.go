//
// Copyright (C) 2025 Dmitry Kolesnikov
//
// This file may be modified and distributed under the terms
// of the MIT license.  See the LICENSE file for details.
// https://github.com/fogfish/cloudmcp
//

package serve

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// WithMetrics emits CloudWatch metrics of every MCP request using
// the Embedded Metric Format (EMF), written to stdout (Lambda's log).
//
// Metrics "Requests" (Count), "Errors" (Count) and "Latency" (Milliseconds)
// have dimensions [Method] and [Method, Tool] (tools/call). A request is an
// error if HTTP status is not 2xx, the JSON-RPC response is an error or the
// tool result has isError.
func WithMetrics(namespace string) Option {
	return func(c *config) {
		c.metrics = &metrics{namespace: namespace, w: stdout}
	}
}

type metrics struct {
	namespace string
	mu        sync.Mutex
	w         io.Writer
}

func (m *metrics) handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cc := &call{}
		r = r.WithContext(context.WithValue(r.Context(), callKey{}, cc))
		rec := &recorder{ResponseWriter: w}

		t := time.Now()
		next.ServeHTTP(rec, r)
		latency := time.Since(t)

		if cc.Method == "" {
			// not a JSON-RPC request (e.g. metadata, rejected or notification)
			if rec.status() < 400 {
				return
			}
			cc.Method = "(invalid)"
		}

		m.emit(cc, rec.failed(), latency)
	})
}

func (m *metrics) emit(cc *call, failed bool, latency time.Duration) {
	dimensions := [][]string{{"Method"}}
	if cc.Tool != "" {
		dimensions = append(dimensions, []string{"Method", "Tool"})
	}

	errors := 0
	if failed {
		errors = 1
	}

	record := map[string]any{
		"_aws": map[string]any{
			"Timestamp": time.Now().UnixMilli(),
			"CloudWatchMetrics": []any{
				map[string]any{
					"Namespace":  m.namespace,
					"Dimensions": dimensions,
					"Metrics": []any{
						map[string]string{"Name": "Requests", "Unit": "Count"},
						map[string]string{"Name": "Errors", "Unit": "Count"},
						map[string]string{"Name": "Latency", "Unit": "Milliseconds"},
					},
				},
			},
		},
		"Method":   cc.Method,
		"Requests": 1,
		"Errors":   errors,
		"Latency":  float64(latency.Microseconds()) / 1000.0,
	}
	if cc.Tool != "" {
		record["Tool"] = cc.Tool
	}

	b, err := json.Marshal(record)
	if err != nil {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.w.Write(append(b, '\n'))
}

// response recorder, keeps the JSON body to detect JSON-RPC errors
type recorder struct {
	http.ResponseWriter
	code int
	body bytes.Buffer
}

const maxRecordedBody = 1 << 20

func (r *recorder) WriteHeader(code int) {
	if r.code == 0 {
		r.code = code
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *recorder) Write(b []byte) (int, error) {
	if r.code == 0 {
		r.code = http.StatusOK
	}
	if r.body.Len()+len(b) <= maxRecordedBody {
		r.body.Write(b)
	}
	return r.ResponseWriter.Write(b)
}

func (r *recorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (r *recorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

func (r *recorder) status() int {
	if r.code == 0 {
		return http.StatusOK
	}
	return r.code
}

func (r *recorder) failed() bool {
	if r.status() >= 400 {
		return true
	}

	body := r.body.Bytes()
	// event stream (server mode), the last data line holds the response
	if strings.HasPrefix(r.Header().Get("Content-Type"), "text/event-stream") {
		if i := bytes.LastIndex(body, []byte("data:")); i >= 0 {
			body = bytes.TrimSpace(bytes.SplitN(body[i+5:], []byte("\n"), 2)[0])
		}
	}

	var resp struct {
		Error  json.RawMessage `json:"error"`
		Result struct {
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	if json.Unmarshal(body, &resp) != nil {
		return false
	}

	return len(resp.Error) > 0 || resp.Result.IsError
}
