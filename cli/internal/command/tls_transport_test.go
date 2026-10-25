// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package command

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

// tlsTestCertificateHost is the only name in the test certificate, which has no IP SAN.
const tlsTestCertificateHost = "localhost"

func TestTLSTransportVerifiesServerAndSendsAPIKeyAccessToken(t *testing.T) {
	clearCLITransportEnvironment(t)
	caPath, certificate := newTLSTestCertificate(t, tlsTestCertificateHost)
	service, address := startTLSCredentialTestService(t, certificate)
	_, port, err := net.SplitHostPort(address)
	require.NoError(t, err)
	endpoint := startCLITokenEndpoint(t, func(requestNumber int, writer http.ResponseWriter) {
		writeCLIAccessToken(writer, fmt.Sprintf("cli-access-token-%d", requestNumber), 3600)
	})
	apiKeyPath := filepath.Join(t.TempDir(), "api-key")
	require.NoError(t, os.WriteFile(apiKeyPath, []byte(cliTestAPIKey), 0o600))
	apiKeyFlags := []string{"--api-key-file", apiKeyPath, "--token-url", endpoint.tokenURL(), "--server-id", cliTestServerID}

	require.NoError(t, executeCLI(append([]string{"health", "--server", net.JoinHostPort(tlsTestCertificateHost, port),
		"--tls", "--tls-ca-file", caPath}, apiKeyFlags...)...), "the verification name defaults to the --server host")
	require.NoError(t, executeCLI(append([]string{"--tls", "--tls-server-name", tlsTestCertificateHost,
		"api", "call", "HealthCheck", "--server", address, "--tls-ca-file", caPath}, apiKeyFlags...)...))
	t.Setenv("DEX_TLS", "true")
	t.Setenv("DEX_TLS_CA_FILE", caPath)
	t.Setenv("DEX_TLS_SERVER_NAME", tlsTestCertificateHost)
	require.NoError(t, executeCLI(append([]string{"health", "--server", address}, apiKeyFlags...)...))
	require.Equal(t, []string{"Bearer cli-access-token-1", "Bearer cli-access-token-2", "Bearer cli-access-token-3"},
		service.recordedHeaders(), "every access token reached the TLS-only server")
}

func TestTLSTransportRejectsUnverifiedServers(t *testing.T) {
	caPath, certificate := newTLSTestCertificate(t, tlsTestCertificateHost)
	service, address := startTLSCredentialTestService(t, certificate)
	for _, rejected := range []struct {
		name      string
		arguments []string
		message   string
	}{
		{
			name:      "certificate without an IP SAN for the --server host",
			arguments: []string{"--tls", "--tls-ca-file", caPath},
			message:   "tls: failed to verify certificate",
		},
		{
			name:      "wrong server name",
			arguments: []string{"--tls", "--tls-ca-file", caPath, "--tls-server-name", "wrong-name.invalid"},
			message:   "tls: failed to verify certificate",
		},
		{
			name:      "issuer missing from the system roots",
			arguments: []string{"--tls", "--tls-server-name", tlsTestCertificateHost},
			message:   "tls: failed to verify certificate",
		},
		{
			name:      "plaintext against a TLS server",
			arguments: nil,
			message:   "Unavailable",
		},
	} {
		t.Run(rejected.name, func(t *testing.T) {
			clearCLITransportEnvironment(t)
			err := executeCLI(append([]string{"health", "--server", address, "--timeout", "5s"}, rejected.arguments...)...)
			require.ErrorContains(t, err, rejected.message)
			require.Equal(t, 1, ExitCode(err))
		})
	}
	require.Empty(t, service.recordedHeaders(), "an unverified connection carries no FlowService call")
}

func TestTLSSettingsOverridePlaintextDefaultOnlyWhenEnabled(t *testing.T) {
	clearCLITransportEnvironment(t)
	service, address := startCredentialTestService(t)
	t.Setenv("DEX_CREDENTIAL", "plaintext-token")
	require.NoError(t, executeCLI("health", "--server", address), "plaintext stays the default")
	err := executeCLI("health", "--server", address, "--tls", "--timeout", "5s")
	require.ErrorContains(t, err, "Unavailable", "TLS never falls back to plaintext")
	t.Setenv("DEX_TLS", "true")
	require.NoError(t, executeCLI("health", "--server", address, "--tls=false"), "--tls=false overrides DEX_TLS")
	require.Equal(t, []string{"Bearer plaintext-token", "Bearer plaintext-token"}, service.recordedHeaders())
}

func TestTLSSettingsRejectInvalidConfiguration(t *testing.T) {
	service, address := startCredentialTestService(t)
	notPEMPath := filepath.Join(t.TempDir(), "not-pem.crt")
	require.NoError(t, os.WriteFile(notPEMPath, []byte("not a certificate"), 0o600))
	for _, invalid := range []struct {
		name             string
		environment      map[string]string
		arguments        []string
		message          string
		expectedExitCode int
	}{
		{
			name:             "unparsable DEX_TLS",
			environment:      map[string]string{"DEX_TLS": "sometimes"},
			message:          `DEX_TLS must be a boolean such as true or false, not "sometimes"`,
			expectedExitCode: 2,
		},
		{
			name:             "CA file without TLS",
			arguments:        []string{"--tls-ca-file", notPEMPath},
			message:          "--tls-ca-file and --tls-server-name (DEX_TLS_CA_FILE, DEX_TLS_SERVER_NAME) require --tls or DEX_TLS=true",
			expectedExitCode: 2,
		},
		{
			name:             "server name environment without TLS",
			environment:      map[string]string{"DEX_TLS_SERVER_NAME": tlsTestCertificateHost},
			message:          "require --tls or DEX_TLS=true",
			expectedExitCode: 2,
		},
		{
			name:             "CA file without a PEM certificate",
			arguments:        []string{"--tls", "--tls-ca-file", notPEMPath},
			message:          "contains no PEM certificate",
			expectedExitCode: 1,
		},
		{
			name:             "missing CA file",
			arguments:        []string{"--tls", "--tls-ca-file", filepath.Join(t.TempDir(), "missing.crt")},
			message:          "read TLS CA file",
			expectedExitCode: 1,
		},
	} {
		t.Run(invalid.name, func(t *testing.T) {
			clearCLITransportEnvironment(t)
			for name, value := range invalid.environment {
				t.Setenv(name, value)
			}
			err := executeCLI(append([]string{"health", "--server", address}, invalid.arguments...)...)
			require.ErrorContains(t, err, invalid.message)
			require.Equal(t, invalid.expectedExitCode, ExitCode(err))
		})
	}
	require.Empty(t, service.recordedHeaders(), "invalid TLS settings fail before any FlowService call")
}

func clearCLITransportEnvironment(t *testing.T) {
	t.Helper()
	clearCLICredentialEnvironment(t)
	for _, name := range []string{"DEX_TLS", "DEX_TLS_CA_FILE", "DEX_TLS_SERVER_NAME"} {
		t.Setenv(name, "")
	}
}

// startTLSCredentialTestService serves only TLS, so every recorded header arrived over TLS.
func startTLSCredentialTestService(t *testing.T, certificate tls.Certificate) (*credentialTestService, string) {
	t.Helper()
	return startCredentialTestService(t, grpc.Creds(credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{certificate},
		MinVersion:   tls.VersionTLS12,
	})))
}

// newTLSTestCertificate returns the path of a self-signed CA certificate for dnsName and its server key pair.
func newTLSTestCertificate(t *testing.T, dnsName string) (string, tls.Certificate) {
	t.Helper()
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: dnsName},
		DNSNames:              []string{dnsName},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	certificateDER, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	require.NoError(t, err)
	caPath := filepath.Join(t.TempDir(), "ca.pem")
	require.NoError(t, os.WriteFile(caPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificateDER}), 0o600))
	return caPath, tls.Certificate{Certificate: [][]byte{certificateDER}, PrivateKey: privateKey}
}
