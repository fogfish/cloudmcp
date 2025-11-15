//
// Copyright (C) 2025 Dmitry Kolesnikov
//
// This file may be modified and distributed under the terms
// of the MIT license.  See the LICENSE file for details.
// https://github.com/fogfish/cloudmcp
//

package auth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Configure IAM authentication for MCP client
type ConfigIAM struct {
	// Endpoint URL of MCP server
	Url string

	// AWS IAM Role to assume
	Role string

	// Optional External ID for AssumeRole operation
	ExternalID string

	// AWS SDK configuration (if nil, default config will be used)
	Config *aws.Config

	// Custom HTTP client (if nil, default client will be used)
	Client *http.Client
}

// NewTransportIAM creates MCP transport with AWS IAM authentication.
func NewTransportIAM(spec ConfigIAM) (*mcp.StreamableClientTransport, error) {
	if len(spec.Url) == 0 {
		return nil, errors.New("missing URL config")
	}

	if spec.Config == nil {
		conf, err := config.LoadDefaultConfig(context.Background())
		if err != nil {
			return nil, err
		}
		spec.Config = &conf
	}

	if spec.Role != "" {
		assumed, err := config.LoadDefaultConfig(context.Background(),
			config.WithCredentialsProvider(
				aws.NewCredentialsCache(
					stscreds.NewAssumeRoleProvider(sts.NewFromConfig(*spec.Config), spec.Role,
						func(aro *stscreds.AssumeRoleOptions) {
							if spec.ExternalID != "" {
								aro.ExternalID = aws.String(spec.ExternalID)
							}
						},
					),
				),
			),
		)
		if err != nil {
			return nil, err
		}
		spec.Config = &assumed
	}

	sock := &iamTransport{
		config: *spec.Config,
		signer: v4.NewSigner(),
		socket: http.DefaultTransport,
	}

	if spec.Client != nil && spec.Client.Transport != nil {
		sock.socket = spec.Client.Transport
	}

	if spec.Client == nil {
		spec.Client = &http.Client{}
	}
	spec.Client.Transport = sock

	return &mcp.StreamableClientTransport{
		Endpoint:   spec.Url,
		HTTPClient: spec.Client,
	}, nil
}

type iamTransport struct {
	config aws.Config
	signer *v4.Signer
	socket http.RoundTripper
}

func (api *iamTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	credential, err := api.config.Credentials.Retrieve(req.Context())
	if err != nil {
		return nil, err
	}

	hash := "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	if req.Body != nil {
		buf := &bytes.Buffer{}
		hasher := sha256.New()
		stream := io.TeeReader(req.Body, hasher)
		if _, err := io.Copy(buf, stream); err != nil {
			return nil, err
		}
		hash = hex.EncodeToString(hasher.Sum(nil))

		req.Body.Close()
		req.Body = io.NopCloser(buf)
	}

	err = api.signer.SignHTTP(
		req.Context(),
		credential,
		req,
		hash,
		"execute-api",
		api.config.Region,
		time.Now(),
	)
	if err != nil {
		return nil, err
	}

	return api.socket.RoundTrip(req)
}
