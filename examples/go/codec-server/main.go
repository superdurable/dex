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

// Command dex-codec-server is a deployable codec server for Dex Web and dexcli.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/superdurable/dex/sdk-go/dex"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	// The SDK's key file permission warning uses slog.Default.
	slog.SetDefault(logger)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := runCodecServer(ctx, os.Args[1:], os.LookupEnv, logger); err != nil {
		logger.Error("codec server stopped", "error", err)
		os.Exit(1)
	}
}

func runCodecServer(
	ctx context.Context,
	arguments []string,
	lookupEnvironment func(string) (string, bool),
	logger *slog.Logger,
) error {
	config, err := loadCodecServerConfig(arguments, lookupEnvironment)
	if err != nil {
		return err
	}
	server, err := newPayloadCodecHTTPServer(config, logger)
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", config.ListenAddress)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", config.ListenAddress, err)
	}
	httpServer := &http.Server{
		Handler:           server.routes(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    64 << 10,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- httpServer.Serve(listener) }()
	logger.Info("codec server listening",
		"address", listener.Addr().String(),
		"auth_mode", config.AuthMode,
		"allowed_origins", config.AllowedOrigins,
	)

	select {
	case err := <-serveErr:
		return err
	case <-ctx.Done():
	}
	logger.Info("codec server shutting down", "timeout", config.ShutdownTimeout.String())
	shutdownCtx, cancel := context.WithTimeout(context.Background(), config.ShutdownTimeout)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	if err := <-serveErr; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// newPayloadCodecHTTPServer wires keys, codec, authentication, and CORS from config.
func newPayloadCodecHTTPServer(config codecServerConfig, logger *slog.Logger) (*payloadCodecHTTPServer, error) {
	keyProvider, err := newPayloadEncryptionKeyProvider(config)
	if err != nil {
		return nil, err
	}
	// No compression: ciphertext length would reveal how well mixed secret and attacker content compresses.
	codec, err := dex.NewAESGCMPayloadCodec(keyProvider, dex.AESGCMPayloadCodecOptions{
		MaxDecompressedBytes: config.PayloadSizeLimits.MaxPlaintextBytes,
		MaxDataBytes:         config.PayloadSizeLimits.MaxDataBytes,
	})
	if err != nil {
		return nil, err
	}
	authenticator, err := newRequestAuthenticator(config)
	if err != nil {
		return nil, err
	}
	return &payloadCodecHTTPServer{
		authenticator:             authenticator,
		codecSelector:             singleTenantPayloadCodecSelector{codec: codec},
		corsOrigins:               newCORSOriginAllowList(config.AllowedOrigins, config.AllowCredentials),
		limits:                    config.PayloadSizeLimits,
		maxRequestBodyBytes:       config.MaxRequestBodyBytes,
		maxItemsPerRequest:        config.MaxItemsPerRequest,
		maxDecodedBytesPerRequest: config.MaxDecodedBytesPerRequest,
		logger:                    logger,
	}, nil
}
