// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Sustainable Use License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package dev

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"time"

	"github.com/superdurable/dex/cli/internal/update"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	dexweb "github.com/superdurable/dex/web"
	"github.com/superdurable/dex/web/assets"
)

type supervisor struct {
	cfg    *Config
	stdout io.Writer
	stderr io.Writer
}

type ownedListeners struct {
	dex net.Listener
	web net.Listener
	// agentMediaStore is nil when the agent media store is off.
	agentMediaStore net.Listener
}

type componentExit struct {
	name string
	err  error
}

func newSupervisor(cfg *Config, stdout io.Writer, stderr io.Writer) *supervisor {
	if cfg == nil {
		panic("dev config must not be nil")
	}
	if stdout == nil || stderr == nil {
		panic("dev output writers must not be nil")
	}
	return &supervisor{cfg: cfg, stdout: stdout, stderr: stderr}
}

func (s *supervisor) Run(ctx context.Context) (runErr error) {
	agentMediaStoreOffReason := s.disableAgentMediaStoreUnlessZDServesIt(ctx)
	listeners, err := reserveOwnedListeners(s.cfg)
	if err != nil {
		return err
	}
	resources := &zdRunResources{ctx: ctx, shutdownTimeout: s.cfg.ShutdownTimeout, listeners: listeners}
	defer resources.closeInto(&runErr)
	if s.cfg.isExplicit("server-log-folder") {
		directory, err := filepath.Abs(s.cfg.LogDirectory)
		if err != nil {
			return fmt.Errorf("resolve server log directory: %w", err)
		}
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return fmt.Errorf("create server log directory: %w", err)
		}
		s.cfg.LogDirectory = directory
	}
	if s.cfg.StateDirectory == "" {
		return fmt.Errorf("Dex state directory is required")
	}
	dataDirectory := s.cfg.zdDataDirectory()
	if err := os.MkdirAll(dataDirectory, 0o700); err != nil {
		return fmt.Errorf("create zd state directory: %w", err)
	}
	dexAddress := listeners.dex.Addr().String()
	if err := closeListener(listeners.dex); err != nil {
		return err
	}
	listeners.dex = nil
	agentMediaStoreAddress := ""
	if listeners.agentMediaStore != nil {
		agentMediaStoreAddress = listeners.agentMediaStore.Addr().String()
		if err := closeListener(listeners.agentMediaStore); err != nil {
			return err
		}
		listeners.agentMediaStore = nil
	}
	process, err := startZDProcess(s.cfg, dataDirectory, dexAddress, agentMediaStoreAddress, s.stderr)
	if err != nil {
		return err
	}
	resources.process = process

	startupCtx, cancelStartup := context.WithTimeout(ctx, s.cfg.ZDStartupTimeout)
	defer cancelStartup()
	dexConnection, err := waitForZD(startupCtx, dexAddress, process)
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
	resources.connection = dexConnection

	webServer, err := dexweb.NewServer(
		&dexweb.Config{
			BindAddress:               s.cfg.BindAddress,
			Port:                      s.cfg.WebPort,
			FlowRenderingDirectory:    s.cfg.FlowRenderingDirectory,
			ConnectorSetupEnabled:     true,
			ConnectorConfigDirectory:  s.cfg.ConnectorConfigDirectory,
			ConnectorReleaseOverrides: s.cfg.ConnectorReleaseOverrides,
			AllowedHosts:              dexweb.SplitAllowedHosts(s.cfg.AllowedHosts),
		},
		dexpb.NewFlowServiceClient(dexConnection),
		assets.Files,
	)
	if err != nil {
		return err
	}
	resources.web = webServer
	webURL := "http://" + listeners.web.Addr().String()
	var webListener interface{ Serve(net.Listener) error } = webServer
	var connectorsMCP *connectorsMCPManager
	if s.isConnectorsMCPServed() {
		connectorsMCP, webListener, err = s.mountConnectorsMCP(webServer, webURL+connectorsMCPMountPath, resources)
		if err != nil {
			return err
		}
	}
	componentExits := make(chan componentExit, 2)
	go func() { componentExits <- componentExit{name: "Dex Web", err: webListener.Serve(listeners.web)} }()
	go func() {
		<-process.done
		componentExits <- componentExit{name: "Dex Server", err: process.Err()}
	}()
	if err := waitForComponentHTTP(startupCtx, webURL+"/healthz", componentExits); err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
	select {
	case exited := <-componentExits:
		return formatComponentExit(exited, "stopped before readiness")
	default:
	}
	s.printReady(webURL, dexAddress, dataDirectory, agentMediaStoreAddress, agentMediaStoreOffReason)
	if connectorsMCP != nil {
		connectorsMCP.Start(ctx)
		resources.connectorsMCP = connectorsMCP
	}
	s.printUpdateNotice(ctx)
	if s.cfg.OpenBrowser {
		if err := openBrowser(webURL); err != nil {
			return err
		}
	}
	select {
	case <-ctx.Done():
		return nil
	case exited := <-componentExits:
		return formatComponentExit(exited, "stopped unexpectedly")
	}
}

func formatComponentExit(exited componentExit, phase string) error {
	if exited.err == nil {
		return fmt.Errorf("%s %s", exited.name, phase)
	}
	return fmt.Errorf("%s %s: %w", exited.name, phase, exited.err)
}

// disableAgentMediaStoreUnlessZDServesIt turns the store off for a dex-server-zd that predates it.
// It returns why the store is off, or an empty string when it is served or not requested.
func (s *supervisor) disableAgentMediaStoreUnlessZDServesIt(ctx context.Context) string {
	if !s.cfg.IsAgentMediaStoreEnabled {
		return ""
	}
	isServed, err := zdServesApplicationS3(ctx, s.cfg.ZDBinary)
	if err != nil {
		s.cfg.IsAgentMediaStoreEnabled = false
		return err.Error()
	}
	if !isServed {
		s.cfg.IsAgentMediaStoreEnabled = false
		return s.cfg.ZDBinary + " does not serve it; use the dex-server-zd bundled with this dexcli"
	}
	return ""
}

func (s *supervisor) printReady(
	webURL string,
	dexAddress string,
	serverDataDirectory string,
	agentMediaStoreAddress string,
	agentMediaStoreOffReason string,
) {
	fmt.Fprintln(s.stdout)
	fmt.Fprintln(s.stdout, "Dex development environment is ready")
	fmt.Fprintln(s.stdout)
	fmt.Fprintf(s.stdout, "Dex Web:       %s\n", webURL)
	switch {
	case s.isConnectorsMCPServed():
		fmt.Fprintf(s.stdout, "Connectors MCP: %s\n", webURL+connectorsMCPMountPath)
	case s.cfg.ConnectorsMCP.IsEnabled:
		fmt.Fprintln(s.stdout, "Connectors MCP: off, because --bind-address is not a loopback address")
	}
	switch {
	case agentMediaStoreAddress != "":
		fmt.Fprintln(s.stdout, agentMediaStoreReadyLine("http://"+agentMediaStoreAddress))
	case agentMediaStoreOffReason != "":
		fmt.Fprintf(s.stdout, "Agent media store: off, because %s\n", agentMediaStoreOffReason)
	}
	fmt.Fprintf(s.stdout, "Dex Server:    %s\n", dexAddress)
	fmt.Fprintf(s.stdout, "State dir:     %s\n", serverDataDirectory)
	if s.cfg.LogDirectory != "" {
		fmt.Fprintf(s.stdout, "Server log folder: %s\n", s.cfg.LogDirectory)
	}
	fmt.Fprintln(s.stdout)
	fmt.Fprintln(s.stdout, "Press Ctrl+C to stop.")
}

// isConnectorsMCPServed reports whether /mcp is mounted; its tools carry no credentials, so only loopback binds serve it.
func (s *supervisor) isConnectorsMCPServed() bool {
	bindIP := net.ParseIP(s.cfg.BindAddress)
	return s.cfg.ConnectorsMCP.IsEnabled && bindIP != nil && bindIP.IsLoopback()
}

// mountConnectorsMCP returns the Connectors MCP manager and the server that serves Dex Web with /mcp mounted.
func (s *supervisor) mountConnectorsMCP(
	webServer *dexweb.Server,
	proxyURL string,
	resources *zdRunResources,
) (*connectorsMCPManager, *webListenerServer, error) {
	if err := s.cfg.ConnectorsMCP.resolvePaths(s.cfg.StateDirectory, s.cfg.ConnectorConfigDirectory); err != nil {
		return nil, nil, err
	}
	proxy := newConnectorsMCPProxy()
	webListener := newWebListenerServer(webServer, proxy)
	resources.webListener = webListener
	resolver := newConnectorsMCPReleaseResolver(
		&s.cfg.ConnectorsMCP, &http.Client{}, runtime.GOOS+"-"+runtime.GOARCH, s.stderr)
	manager := newConnectorsMCPManager(
		&s.cfg.ConnectorsMCP, resolver, newLoopbackHTTPClient(), proxy, s.stderr, proxyURL)
	return manager, webListener, nil
}

// newLoopbackHTTPClient returns a client that never sends loopback requests through an HTTP proxy.
func newLoopbackHTTPClient() *http.Client {
	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		panic("http.DefaultTransport is not an *http.Transport")
	}
	loopbackTransport := transport.Clone()
	loopbackTransport.Proxy = nil
	return &http.Client{Transport: loopbackTransport}
}

func (s *supervisor) printUpdateNotice(ctx context.Context) {
	go func() {
		update.PrintNotice(ctx, s.stderr, s.cfg.version)
	}()
}

func reserveOwnedListeners(cfg *Config) (*ownedListeners, error) {
	allocated := make(map[string]string)
	dexListener, err := bindServicePort("Dex Server", cfg.isExplicit("dex-port"), cfg.BindAddress, &cfg.DexPort, allocated)
	if err != nil {
		return nil, err
	}
	listeners := &ownedListeners{dex: dexListener}
	webListener, err := bindServicePort("Dex Web", cfg.isExplicit("web-port"), cfg.BindAddress, &cfg.WebPort, allocated)
	if err != nil {
		return nil, errors.Join(err, listeners.Close())
	}
	listeners.web = webListener
	if cfg.IsAgentMediaStoreEnabled {
		mediaListener, err := bindServicePort(
			"Agent media store", cfg.isExplicit("agent-media-store-port"), agentMediaStoreHost, &cfg.AgentMediaStorePort, allocated)
		if err != nil {
			return nil, errors.Join(err, listeners.Close())
		}
		listeners.agentMediaStore = mediaListener
	}
	return listeners, nil
}

func bindServicePort(
	name string,
	explicit bool,
	host string,
	port *int,
	allocated map[string]string,
) (net.Listener, error) {
	for candidate := *port; candidate <= 65535; candidate++ {
		address := net.JoinHostPort(host, strconv.Itoa(candidate))
		if owner, exists := allocated[address]; exists {
			if explicit {
				return nil, fmt.Errorf("%s and %s cannot both use %s", owner, name, address)
			}
			continue
		}
		if isAddressReachable(address) {
			if explicit {
				return nil, fmt.Errorf("cannot start %s: %s is already in use", name, address)
			}
			continue
		}
		listener, err := net.Listen("tcp", address)
		if err != nil {
			if explicit {
				return nil, fmt.Errorf("cannot start %s: %s is already in use: %w", name, address, err)
			}
			continue
		}
		*port = candidate
		allocated[address] = name
		return listener, nil
	}
	return nil, fmt.Errorf("cannot start %s: no available port from %d", name, *port)
}

func isAddressReachable(address string) bool {
	connection, err := net.DialTimeout("tcp", address, 100*time.Millisecond)
	if err != nil {
		return false
	}
	// Closing the probe does not change that the port accepted a connection.
	_ = connection.Close()
	return true
}

func closeListener(listener net.Listener) error {
	if listener == nil {
		return nil
	}
	if err := listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		return err
	}
	return nil
}

func (l *ownedListeners) Close() error {
	return errors.Join(closeListener(l.agentMediaStore), closeListener(l.web), closeListener(l.dex))
}

func waitForComponentHTTP(ctx context.Context, url string, exits <-chan componentExit) error {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var lastErr error
	for {
		lastErr = checkHTTP(ctx, url)
		if lastErr == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return errors.Join(ctx.Err(), lastErr)
		case exited := <-exits:
			return fmt.Errorf("%s exited before readiness: %w", exited.name, exited.err)
		case <-ticker.C:
		}
	}
}

func checkHTTP(ctx context.Context, url string) error {
	requestCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	_, readErr := io.Copy(io.Discard, response.Body)
	closeErr := response.Body.Close()
	if readErr != nil || closeErr != nil {
		return errors.Join(readErr, closeErr)
	}
	if response.StatusCode < 200 || response.StatusCode >= 400 {
		return fmt.Errorf("%s returned HTTP %d", url, response.StatusCode)
	}
	return nil
}

func openBrowser(url string) error {
	var command *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		command = exec.Command("open", url)
	case "linux":
		command = exec.Command("xdg-open", url)
	default:
		return fmt.Errorf("--open is not supported on %s", runtime.GOOS)
	}
	if err := command.Start(); err != nil {
		return fmt.Errorf("open Dex Web: %w", err)
	}
	if err := command.Process.Release(); err != nil {
		return fmt.Errorf("release browser process: %w", err)
	}
	return nil
}
