// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package integ

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v3/jwt"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"github.com/superdurable/dex/server-zd/integ/internal/testconfig"
	"github.com/superdurable/dex/server-zd/integ/workflow/basic"
	"google.golang.org/grpc/codes"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

func TestGeneralAuthenticationTemporal(t *testing.T) {
	testGeneralAuthentication(t)
}

func testGeneralAuthentication(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	token := "public-" + uuid.NewString()
	hash := sha256.Sum256([]byte(token))
	tokenFile := filepath.Join(t.TempDir(), "public-tokens.yaml")
	require.NoError(t, os.WriteFile(tokenFile, []byte("tokens:\n  - sha256: "+hex.EncodeToString(hash[:])+"\n"), 0o600))
	issuer := newTestJWTIssuer(t)
	testConfig := serverZDTestConfig{

		Auth: &testconfig.AuthConfig{Authenticators: []testconfig.AuthenticatorConfig{
			{Kind: testconfig.AuthenticatorKindStaticTokens, File: tokenFile},
			{Kind: testconfig.AuthenticatorKindJWT, Issuer: issuer.URL, Audiences: []string{"dex"}, JWKSURL: issuer.URL + "/keys"},
		}},
		Credential: token,
	}
	var runtime *integRuntime

	// server-zd has no internal listener; its pull Workers send the public credential.
	runtime = startDexService(t, testConfig)

	call := func(credential string) codes.Code {
		_, err := newFlowClientWithCredential(t, runtime.PublicAddress, credential).GetServerInfo(ctx, &emptypb.Empty{})
		return status.Code(err)
	}
	require.Equal(t, codes.Unauthenticated, call(""))
	require.Equal(t, codes.Unauthenticated, call("wrong-"+uuid.NewString()))
	require.Equal(t, codes.OK, call(token))
	health, err := healthpb.NewHealthClient(newDexClientConnectionWithCredential(t, runtime.PublicAddress, "", testconfig.DefaultGrpcMaxMessageBytes)).Check(ctx, &healthpb.HealthCheckRequest{})
	require.NoError(t, err)
	require.Equal(t, healthpb.HealthCheckResponse_SERVING, health.GetStatus())
	jwtToken := func(expiry time.Time, audience string) string {
		return signTestJWT(t, issuer.key(authenticationTestKeyID), authenticationTestKeyID, jwt.Claims{
			Issuer: issuer.URL, Subject: "caller", Audience: jwt.Audience{audience}, Expiry: jwt.NewNumericDate(expiry),
		})
	}
	require.Equal(t, codes.OK, call(jwtToken(time.Now().Add(time.Hour), "dex")))
	require.Equal(t, codes.OK, call(signTestJWT(t, issuer.key(authenticationTestKeyID), authenticationTestKeyID, jwt.Claims{
		Issuer: issuer.URL, Audience: jwt.Audience{"dex"}, Expiry: jwt.NewNumericDate(time.Now().Add(time.Hour)),
	})))
	require.Equal(t, codes.Unauthenticated, call(jwtToken(time.Now().Add(-time.Hour), "dex")))
	require.Equal(t, codes.Unauthenticated, call(jwtToken(time.Now().Add(time.Hour), "wrong")))

	workerTarget := startWorker(t, basic.NewHandler())
	flowID := basic.FlowType + "-" + uuid.NewString()
	_, err = runtime.FlowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId: newRequestID(), FlowId: flowID, FlowType: basic.FlowType,
		FlowTimeoutSeconds: 100, StartStepType: basic.Step1, StepInput: jsonObjValue("authenticated input"),
		FlowStartOptions: withWorkerTarget(&dexpb.FlowStartOptions{
			FlowConfigOverride: minimumContinueAsNewAsyncDurabilityConfig(),
		}, workerTarget),
	})
	require.NoError(t, err)
	result, err := runtime.FlowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{FlowId: flowID, NeedsResults: true, WaitTimeSeconds: 20})
	require.NoError(t, err)
	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_COMPLETED, result.GetFlowStatus())

	return

}
