// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.temporal.io/sdk/converter"
)

const (
	defaultAddress            = "127.0.0.1:8888"
	temporalCloudWebOrigin    = "https://cloud.temporal.io"
	serverReadHeaderTimeout   = 5 * time.Second
	serverShutdownTimeout     = 5 * time.Second
	corsAllowedMethods        = "POST, GET, OPTIONS"
	corsAllowedHeaders        = "X-Namespace, Content-Type"
	corsRequestedMethodHeader = "Access-Control-Request-Method"
	corsAllowedOriginHeader   = "Access-Control-Allow-Origin"
	corsAllowedMethodsHeader  = "Access-Control-Allow-Methods"
	corsAllowedHeadersHeader  = "Access-Control-Allow-Headers"
)

type codecServerConfig struct {
	address string
}

func main() {
	shutdownContext, stopWaitingForShutdown := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
	)
	defer stopWaitingForShutdown()

	if err := runCodecServer(shutdownContext, os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}

func runCodecServer(shutdownContext context.Context, arguments []string) error {
	codecServerConfig, err := parseCodecServerConfig(arguments)
	if err != nil {
		return err
	}
	if err := validateLoopbackAddress(codecServerConfig.address); err != nil {
		return err
	}

	listener, err := net.Listen("tcp", codecServerConfig.address)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", codecServerConfig.address, err)
	}

	codecHTTPServer := &http.Server{
		Addr:              codecServerConfig.address,
		Handler:           newCodecServerHTTPHandler(),
		ReadHeaderTimeout: serverReadHeaderTimeout,
	}
	serveErrorChannel := make(chan error, 1)
	go func() {
		serveErrorChannel <- codecHTTPServer.Serve(listener)
	}()

	log.Printf("Temporal protobuf Codec Server listening at http://%s", listener.Addr())
	select {
	case serveErr := <-serveErrorChannel:
		if errors.Is(serveErr, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve codec requests: %w", serveErr)
	case <-shutdownContext.Done():
	}

	shutdownTimeoutContext, cancelShutdown := context.WithTimeout(context.Background(), serverShutdownTimeout)
	defer cancelShutdown()
	if err := codecHTTPServer.Shutdown(shutdownTimeoutContext); err != nil {
		return fmt.Errorf("shut down codec server: %w", err)
	}
	log.Print("Temporal protobuf Codec Server stopped")
	return nil
}

func parseCodecServerConfig(arguments []string) (codecServerConfig, error) {
	flagSet := flag.NewFlagSet("temporal-protobuf-codec-server", flag.ContinueOnError)
	address := flagSet.String("address", defaultAddress, "loopback address to listen on")
	if err := flagSet.Parse(arguments); err != nil {
		return codecServerConfig{}, fmt.Errorf("parse flags: %w", err)
	}
	if flagSet.NArg() != 0 {
		return codecServerConfig{}, fmt.Errorf("unexpected arguments: %v", flagSet.Args())
	}
	return codecServerConfig{address: *address}, nil
}

func validateLoopbackAddress(address string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("parse address %q: %w", address, err)
	}
	if host == "localhost" {
		return nil
	}
	ipAddress := net.ParseIP(host)
	if ipAddress == nil || !ipAddress.IsLoopback() {
		return fmt.Errorf("address %q must use a loopback host", address)
	}
	return nil
}

func newCodecServerHTTPHandler() http.Handler {
	codecHandler := converter.NewPayloadCodecHTTPHandler(newDexProtobufCodec())
	router := http.NewServeMux()
	router.Handle("/decode", codecHandler)
	router.Handle("/encode", codecHandler)
	router.HandleFunc("GET /healthz", handleCodecServerHealthCheck)
	return withTemporalCloudCORS(router)
}

func handleCodecServerHealthCheck(responseWriter http.ResponseWriter, _ *http.Request) {
	responseWriter.Header().Set("Content-Type", "text/plain; charset=utf-8")
	responseWriter.WriteHeader(http.StatusOK)
	if _, err := responseWriter.Write([]byte("ok\n")); err != nil {
		log.Printf("write health response: %v", err)
	}
}

func withTemporalCloudCORS(nextHandler http.Handler) http.Handler {
	return http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		origin := request.Header.Get("Origin")
		if origin != "" && origin != temporalCloudWebOrigin {
			http.Error(responseWriter, "origin is not allowed", http.StatusForbidden)
			return
		}
		if origin == temporalCloudWebOrigin {
			responseWriter.Header().Add("Vary", "Origin")
			responseWriter.Header().Set(corsAllowedOriginHeader, temporalCloudWebOrigin)
			responseWriter.Header().Set(corsAllowedMethodsHeader, corsAllowedMethods)
			responseWriter.Header().Set(corsAllowedHeadersHeader, corsAllowedHeaders)
		}
		if request.Method == http.MethodOptions {
			if requestedMethod := request.Header.Get(corsRequestedMethodHeader); requestedMethod != "" && requestedMethod != http.MethodPost && requestedMethod != http.MethodGet {
				http.Error(responseWriter, "requested method is not allowed", http.StatusForbidden)
				return
			}
			responseWriter.WriteHeader(http.StatusNoContent)
			return
		}
		nextHandler.ServeHTTP(responseWriter, request)
	})
}
