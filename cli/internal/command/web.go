// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package command

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	dexweb "github.com/superdurable/dex/web"
	"github.com/superdurable/dex/web/assets"
)

const (
	defaultWebBindAddress = "127.0.0.1"
	defaultWebPort        = 8802
	webShutdownTimeout    = 10 * time.Second
	webOperation          = "web"
)

type webOptions struct {
	bindAddress            string
	webPort                int
	flowRenderingDirectory string
	shouldOpenBrowser      bool
}

func (a *App) executeWeb(ctx context.Context, args []string, options options) error {
	flags := newFlagSet("dexcli web", a.stderr)
	addCommonFlags(flags, &options)
	parsedWebOptions := webOptions{
		bindAddress:       defaultWebBindAddress,
		webPort:           defaultWebPort,
		shouldOpenBrowser: true,
	}
	flags.StringVar(&parsedWebOptions.bindAddress, "bind-address", parsedWebOptions.bindAddress, "Dex Web bind address")
	flags.IntVar(&parsedWebOptions.webPort, "web-port", parsedWebOptions.webPort, "Dex Web port")
	flags.StringVar(
		&parsedWebOptions.flowRenderingDirectory,
		"flow-rendering-dir",
		"",
		"directory containing Flow Definition Graph JSON files",
	)
	flags.BoolVar(&parsedWebOptions.shouldOpenBrowser, "open", parsedWebOptions.shouldOpenBrowser, "open Dex Web after startup")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printWebUsage(a.stdout)
			return nil
		}
		return newUsageError(webOperation, err)
	}
	if flags.NArg() != 0 {
		return newUsageError(webOperation, fmt.Errorf("unexpected arguments: %v", flags.Args()))
	}
	if err := options.validate(); err != nil {
		return newUsageError(webOperation, err)
	}
	if parsedWebOptions.webPort < 0 || parsedWebOptions.webPort > 65535 {
		return newUsageError(webOperation, fmt.Errorf("web-port must be between 0 and 65535"))
	}
	return a.serveWeb(ctx, options, parsedWebOptions)
}

// serveWeb keeps the FlowService connection for the server lifetime; --timeout bounds only startup checks.
func (a *App) serveWeb(ctx context.Context, options options, webOptions webOptions) (runErr error) {
	connection, err := newServerConnection(options, "FlowService")
	if err != nil {
		return newOperationError("connect", err)
	}
	defer func() {
		if closeErr := connection.Close(); closeErr != nil {
			runErr = errors.Join(runErr, newOperationError("close", closeErr))
		}
	}()
	client := dexpb.NewFlowServiceClient(connection)
	if err := checkWebFlowServiceHealth(ctx, options, client); err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
	a.warnIncompatibleServerProtocol(ctx, options, client)
	if ctx.Err() != nil {
		return nil
	}
	server, err := dexweb.NewServer(
		&dexweb.Config{
			BindAddress:            webOptions.bindAddress,
			Port:                   webOptions.webPort,
			FlowRenderingDirectory: webOptions.flowRenderingDirectory,
			ConnectorSetupEnabled:  false,
		},
		client,
		assets.Files,
	)
	if err != nil {
		return newOperationError(webOperation, err)
	}
	listenAddress := net.JoinHostPort(webOptions.bindAddress, strconv.Itoa(webOptions.webPort))
	listener, err := net.Listen("tcp", listenAddress)
	if err != nil {
		return newOperationError(webOperation, fmt.Errorf("listen for Dex Web on %s: %w", listenAddress, err))
	}
	serverErrors := make(chan error, 1)
	go func() { serverErrors <- server.Serve(listener) }()
	if !isLoopbackHost(webOptions.bindAddress) {
		fmt.Fprintf(
			a.stderr,
			"warning: Dex Web has no authentication and listens on %s; anyone who can reach this port can operate Flows on %s\n",
			listener.Addr().String(),
			options.server,
		)
	}
	webURL := "http://" + listener.Addr().String()
	a.printWebReady(webURL, options.server)
	if webOptions.shouldOpenBrowser {
		if err := a.openBrowser(webURL); err != nil {
			fmt.Fprintf(a.stderr, "warning: open Dex Web in a browser: %v\n", err)
		}
	}
	select {
	case serveErr := <-serverErrors:
		return newOperationError(webOperation, fmt.Errorf("Dex Web stopped unexpectedly: %w", serveErr))
	case <-ctx.Done():
	}
	return shutdownWebServer(server, serverErrors)
}

func checkWebFlowServiceHealth(ctx context.Context, options options, client dexpb.FlowServiceClient) error {
	callCtx, cancel := requestContext(ctx, options)
	defer cancel()
	if _, err := client.HealthCheck(callCtx, emptyRequest()); err != nil {
		return newOperationError(
			webOperation,
			fmt.Errorf("Dex server at --server %s is unreachable or unhealthy; check the server and any SSH tunnel: %w", options.server, err),
		)
	}
	return nil
}

// warnIncompatibleServerProtocol warns instead of failing so Dex Web can still show what the server returns.
func (a *App) warnIncompatibleServerProtocol(ctx context.Context, options options, client dexpb.FlowServiceClient) {
	callCtx, cancel := requestContext(ctx, options)
	defer cancel()
	serverInfo, err := client.GetServerInfo(callCtx, emptyRequest())
	if ctx.Err() != nil {
		return
	}
	if err != nil {
		fmt.Fprintf(a.stderr, "warning: cannot check Server protocol compatibility: GetServerInfo failed: %v\n", err)
		return
	}
	if _, err := negotiateServerProtocol(serverInfo); err != nil {
		fmt.Fprintf(
			a.stderr,
			"warning: dexcli %s may be incompatible with Server %s: %v\n",
			a.artifactVersion,
			serverInfo.GetServerVersion(),
			err,
		)
	}
}

func (a *App) printWebReady(webURL string, serverAddress string) {
	fmt.Fprintln(a.stdout, "Dex Web is ready")
	fmt.Fprintln(a.stdout)
	fmt.Fprintf(a.stdout, "Dex Web:       %s\n", webURL)
	fmt.Fprintf(a.stdout, "Dex Server:    %s\n", serverAddress)
	fmt.Fprintln(a.stdout)
	fmt.Fprintln(a.stdout, "Press Ctrl+C to stop.")
}

func shutdownWebServer(server *dexweb.Server, serverErrors <-chan error) error {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), webShutdownTimeout)
	defer cancel()
	shutdownErr := server.Shutdown(shutdownCtx)
	serveErr := <-serverErrors
	if errors.Is(serveErr, http.ErrServerClosed) {
		serveErr = nil
	}
	if err := errors.Join(shutdownErr, serveErr); err != nil {
		return newOperationError(webOperation, fmt.Errorf("stop Dex Web: %w", err))
	}
	return nil
}

func printWebUsage(output io.Writer) {
	fmt.Fprintln(output, "Usage: dexcli [global flags] web [flags]")
	fmt.Fprintln(output)
	fmt.Fprintln(output, "Serve Dex Web against the existing Dex server given by --server.")
	fmt.Fprintln(output, "Dex Web has no authentication; keep it on a loopback address.")
	fmt.Fprintln(output)
	fmt.Fprintln(output, "Flags:")
	fmt.Fprintln(output, "  --bind-address address      Dex Web bind address (default 127.0.0.1)")
	fmt.Fprintln(output, "  --web-port port             Dex Web port (default 8802)")
	fmt.Fprintln(output, "  --flow-rendering-dir path   directory containing Flow Definition Graph JSON files")
	fmt.Fprintln(output, "  --open                      open Dex Web in the default browser (default true; --open=false disables)")
	fmt.Fprintln(output)
	fmt.Fprintln(output, "--timeout bounds only the startup checks, not the running Dex Web.")
	fmt.Fprintln(output, "A running dexcli dev also uses port 8802; pass another --web-port to run both.")
}
