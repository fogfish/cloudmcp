//
// Copyright (C) 2025 Dmitry Kolesnikov
//
// This file may be modified and distributed under the terms
// of the MIT license.  See the LICENSE file for details.
// https://github.com/fogfish/cloudmcp
//

package auth_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fogfish/cloudmcp/pkg/auth"
)

func roundTrip(t *testing.T, client *http.Client) string {
	t.Helper()

	var header string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header = r.Header.Get("Authorization")
	}))
	defer ts.Close()

	req, _ := http.NewRequest("POST", ts.URL, nil)
	if _, err := client.Do(req); err != nil {
		t.Fatal(err)
	}
	if req.Header.Get("Authorization") != "" {
		t.Errorf("request is modified")
	}

	return header
}

func TestApiKey(t *testing.T) {
	transport, err := auth.NewTransportApiKey(auth.ConfigApiKey{Url: "https://example.com", Access: "access", Secret: "secret1"})
	if err != nil {
		t.Fatal(err)
	}

	// padded base64 (RFC 7617)
	if v := roundTrip(t, transport.HTTPClient); v != "Basic YWNjZXNzOnNlY3JldDE=" {
		t.Errorf("unexpected header %q", v)
	}
}

func TestBearer(t *testing.T) {
	transport, err := auth.NewTransportBearer(auth.ConfigBearer{Url: "https://example.com", Token: "key"})
	if err != nil {
		t.Fatal(err)
	}

	if v := roundTrip(t, transport.HTTPClient); v != "Bearer key" {
		t.Errorf("unexpected header %q", v)
	}
}

func TestConfig(t *testing.T) {
	if _, err := auth.NewTransportApiKey(auth.ConfigApiKey{}); err == nil {
		t.Errorf("missing url is accepted")
	}
	if _, err := auth.NewTransportBearer(auth.ConfigBearer{Url: "https://example.com"}); err == nil {
		t.Errorf("missing token is accepted")
	}
}
