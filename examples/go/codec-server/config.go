// Copyright (c) 2022-2026 Super Durable, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	authModeBearerToken        = "bearer-token"
	authModeTrustedProxyHeader = "trusted-proxy-header"

	// Spec defaults: 16 MiB + 64 KiB envelopes and 64 MiB plaintexts.
	defaultMaxEnvelopeBytes  = 16_842_752
	defaultMaxPlaintextBytes = 67_108_864
	// Fits one maximum envelope as base64 (about 22 MiB) plus JSON overhead.
	defaultMaxRequestBodyBytes = 32 << 20
	defaultMaxItemsPerRequest  = 1000
	// Caps decompression amplification: 1000 entries could otherwise decode to 64 MiB each.
	defaultMaxDecodedBytesPerRequest = 64 << 20
	minimumBearerTokenBytes          = 32
)

// payloadSizeLimits holds the decode and encode limits of protos/payload-encryption.md section 7.
type payloadSizeLimits struct {
	MaxEnvelopeBytes    int
	MaxEncodedTextBytes int
	MaxDataBytes        int
	MaxPlaintextBytes   int
}

func newPayloadSizeLimits(maxEnvelopeBytes int, maxPlaintextBytes int) payloadSizeLimits {
	return payloadSizeLimits{
		MaxEnvelopeBytes:    maxEnvelopeBytes,
		MaxEncodedTextBytes: 4 * ((maxEnvelopeBytes + 2) / 3),
		MaxDataBytes:        maxEnvelopeBytes,
		MaxPlaintextBytes:   maxPlaintextBytes,
	}
}

// codecServerConfig is the validated configuration of one codec server process.
type codecServerConfig struct {
	ListenAddress string
	// KeyFilePath and KeysJSON are the two key sources; exactly one is set.
	KeyFilePath           string
	KeysJSON              string
	AllowedOrigins        []string
	AllowCredentials      bool
	AuthMode              string
	BearerToken           string
	TrustedIdentityHeader string
	MaxRequestBodyBytes   int64
	MaxItemsPerRequest    int
	// MaxDecodedBytesPerRequest bounds the total Value JSON of one /decode response.
	MaxDecodedBytesPerRequest int
	PayloadSizeLimits         payloadSizeLimits
	ShutdownTimeout           time.Duration
}

// loadCodecServerConfig reads flags that default to DEX_CODEC_SERVER_* variables.
// Secrets never come from flags, which leak into process lists.
func loadCodecServerConfig(arguments []string, lookupEnvironment func(string) (string, bool)) (codecServerConfig, error) {
	environment := func(name string, fallback string) string {
		if value, found := lookupEnvironment(name); found && value != "" {
			return value
		}
		return fallback
	}
	maxRequestBodyBytes, err := strconv.ParseInt(
		environment("DEX_CODEC_SERVER_MAX_REQUEST_BODY_BYTES", strconv.Itoa(defaultMaxRequestBodyBytes)), 10, 64)
	if err != nil {
		return codecServerConfig{}, errors.New("DEX_CODEC_SERVER_MAX_REQUEST_BODY_BYTES is not an integer")
	}
	integerSettings := map[string]int{
		"DEX_CODEC_SERVER_MAX_ITEMS_PER_REQUEST":         defaultMaxItemsPerRequest,
		"DEX_CODEC_SERVER_MAX_DECODED_BYTES_PER_REQUEST": defaultMaxDecodedBytesPerRequest,
		"DEX_CODEC_SERVER_MAX_ENVELOPE_BYTES":            defaultMaxEnvelopeBytes,
		"DEX_CODEC_SERVER_MAX_PLAINTEXT_BYTES":           defaultMaxPlaintextBytes,
	}
	for name, fallback := range integerSettings {
		parsed, err := strconv.Atoi(environment(name, strconv.Itoa(fallback)))
		if err != nil {
			return codecServerConfig{}, fmt.Errorf("%s is not an integer", name)
		}
		integerSettings[name] = parsed
	}
	allowCredentials, err := strconv.ParseBool(environment("DEX_CODEC_SERVER_ALLOW_CREDENTIALS", "false"))
	if err != nil {
		return codecServerConfig{}, errors.New("DEX_CODEC_SERVER_ALLOW_CREDENTIALS is not a boolean")
	}
	shutdownTimeout, err := time.ParseDuration(environment("DEX_CODEC_SERVER_SHUTDOWN_TIMEOUT", "15s"))
	if err != nil {
		return codecServerConfig{}, errors.New("DEX_CODEC_SERVER_SHUTDOWN_TIMEOUT is not a duration")
	}

	flags := flag.NewFlagSet("dex-codec-server", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	listenAddress := flags.String("listen-address", environment("DEX_CODEC_SERVER_LISTEN_ADDRESS", ":8080"),
		"HTTP listen address. Put an HTTPS reverse proxy in front of it.")
	keyFilePath := flags.String("key-file", environment("DEX_CODEC_SERVER_KEY_FILE", ""),
		"Path of the Dex payload key file. DEX_CODEC_SERVER_KEYS_JSON is the alternative.")
	allowedOrigins := flags.String("allowed-origins", environment("DEX_CODEC_SERVER_ALLOWED_ORIGINS", ""),
		"Comma-separated Dex Web origins allowed by CORS, such as https://dex-web.example.com.")
	flags.BoolVar(&allowCredentials, "allow-credentials", allowCredentials,
		"Allow browsers to send cookies, for an SSO proxy that authenticates with cookies.")
	authMode := flags.String("auth-mode", environment("DEX_CODEC_SERVER_AUTH_MODE", ""),
		"bearer-token or trusted-proxy-header.")
	bearerTokenFile := flags.String("bearer-token-file", environment("DEX_CODEC_SERVER_BEARER_TOKEN_FILE", ""),
		"File holding the bearer token. DEX_CODEC_SERVER_BEARER_TOKEN is the alternative.")
	trustedIdentityHeader := flags.String("trusted-identity-header",
		environment("DEX_CODEC_SERVER_TRUSTED_IDENTITY_HEADER", ""),
		"Header that the upstream SSO proxy sets to the authenticated user, such as X-Forwarded-Email.")
	flags.Int64Var(&maxRequestBodyBytes, "max-request-body-bytes", maxRequestBodyBytes, "Request body limit in bytes.")
	maxItemsPerRequest := flags.Int("max-items-per-request",
		integerSettings["DEX_CODEC_SERVER_MAX_ITEMS_PER_REQUEST"], "Values or envelopes per request.")
	maxDecodedBytesPerRequest := flags.Int("max-decoded-bytes-per-request",
		integerSettings["DEX_CODEC_SERVER_MAX_DECODED_BYTES_PER_REQUEST"], "Total decoded Value JSON per /decode request.")
	maxEnvelopeBytes := flags.Int("max-envelope-bytes",
		integerSettings["DEX_CODEC_SERVER_MAX_ENVELOPE_BYTES"], "Serialized envelope limit in bytes.")
	maxPlaintextBytes := flags.Int("max-plaintext-bytes",
		integerSettings["DEX_CODEC_SERVER_MAX_PLAINTEXT_BYTES"], "Decrypted value limit in bytes.")
	flags.DurationVar(&shutdownTimeout, "shutdown-timeout", shutdownTimeout, "Graceful shutdown timeout.")
	if err := flags.Parse(arguments); err != nil {
		return codecServerConfig{}, err
	}

	config := codecServerConfig{
		ListenAddress:             *listenAddress,
		KeyFilePath:               *keyFilePath,
		KeysJSON:                  environment("DEX_CODEC_SERVER_KEYS_JSON", ""),
		AllowCredentials:          allowCredentials,
		AuthMode:                  *authMode,
		TrustedIdentityHeader:     strings.TrimSpace(*trustedIdentityHeader),
		MaxRequestBodyBytes:       maxRequestBodyBytes,
		MaxItemsPerRequest:        *maxItemsPerRequest,
		MaxDecodedBytesPerRequest: *maxDecodedBytesPerRequest,
		PayloadSizeLimits:         newPayloadSizeLimits(*maxEnvelopeBytes, *maxPlaintextBytes),
		ShutdownTimeout:           shutdownTimeout,
	}
	config.AllowedOrigins, err = parseAllowedOrigins(*allowedOrigins)
	if err != nil {
		return codecServerConfig{}, err
	}
	if config.AuthMode == authModeBearerToken {
		config.BearerToken, err = readBearerToken(environment("DEX_CODEC_SERVER_BEARER_TOKEN", ""), *bearerTokenFile)
		if err != nil {
			return codecServerConfig{}, err
		}
	}
	if err := config.validate(); err != nil {
		return codecServerConfig{}, err
	}
	return config, nil
}

func (config codecServerConfig) validate() error {
	if (config.KeyFilePath == "") == (config.KeysJSON == "") {
		return errors.New("set exactly one key source: -key-file (DEX_CODEC_SERVER_KEY_FILE) or DEX_CODEC_SERVER_KEYS_JSON")
	}
	if len(config.AllowedOrigins) == 0 {
		return errors.New("set -allowed-origins (DEX_CODEC_SERVER_ALLOWED_ORIGINS) to the Dex Web origins")
	}
	switch config.AuthMode {
	case authModeBearerToken:
		if len(config.BearerToken) < minimumBearerTokenBytes {
			return fmt.Errorf("the bearer token must have at least %d bytes", minimumBearerTokenBytes)
		}
	case authModeTrustedProxyHeader:
		if config.TrustedIdentityHeader == "" {
			return errors.New("trusted-proxy-header mode needs -trusted-identity-header")
		}
	default:
		return fmt.Errorf("-auth-mode must be %s or %s; the codec server never runs without authentication",
			authModeBearerToken, authModeTrustedProxyHeader)
	}
	if config.MaxRequestBodyBytes <= 0 || config.MaxItemsPerRequest <= 0 || config.MaxDecodedBytesPerRequest <= 0 {
		return errors.New("request body, item, and decoded-bytes limits must be positive")
	}
	if config.ShutdownTimeout <= 0 {
		return errors.New("-shutdown-timeout must be positive")
	}
	if config.PayloadSizeLimits.MaxEnvelopeBytes <= 0 || config.PayloadSizeLimits.MaxPlaintextBytes <= 0 {
		return errors.New("envelope and plaintext limits must be positive")
	}
	return nil
}

// parseAllowedOrigins accepts only exact scheme://host[:port] origins and rejects "*".
func parseAllowedOrigins(commaSeparated string) ([]string, error) {
	var origins []string
	for _, entry := range strings.Split(commaSeparated, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if strings.Contains(entry, "*") {
			return nil, fmt.Errorf("allowed origin %q: wildcards are not allowed; list each Dex Web origin", entry)
		}
		parsed, err := url.Parse(entry)
		if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" ||
			(parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.User != nil {
			return nil, fmt.Errorf("allowed origin %q must look like https://dex-web.example.com", entry)
		}
		origins = append(origins, strings.ToLower(parsed.Scheme+"://"+parsed.Host))
	}
	return origins, nil
}

func readBearerToken(environmentToken string, tokenFilePath string) (string, error) {
	if (environmentToken == "") == (tokenFilePath == "") {
		return "", errors.New(
			"bearer-token mode needs exactly one of DEX_CODEC_SERVER_BEARER_TOKEN or -bearer-token-file")
	}
	if environmentToken != "" {
		return environmentToken, nil
	}
	content, err := os.ReadFile(tokenFilePath)
	if err != nil {
		return "", fmt.Errorf("read bearer token file: %w", err)
	}
	return strings.TrimSpace(string(content)), nil
}
