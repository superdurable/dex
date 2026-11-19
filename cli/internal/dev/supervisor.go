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
	"google.golang.org/protobuf/types/known/emptypb"
)

// serverVersionLookupTimeout bounds the one GetServerInfo call for the ready banner on a server that is already serving.
const serverVersionLookupTimeout = time.Second

type supervisor struct {
	cfg    *Config
	stdout io.Writer
	stderr io.Writer
}

type ownedListeners struct {
	dex net.Listener
	web net.Listener
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
	serverLogs, serverLogPath, err := s.openServerLogs()
	if err != nil {
		return err
	}
	resources.serverLogs = serverLogs
	process, err := startZDProcess(s.cfg, dataDirectory, dexAddress, serverLogs)
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
		s.printServerLogTail(serverLogs, serverLogPath)
		return err
	}
	resources.connection = dexConnection
	flowServiceClient := dexpb.NewFlowServiceClient(dexConnection)
	serverVersion := startServerVersionLookup(startupCtx, flowServiceClient)

	webServer, err := dexweb.NewServer(
		&dexweb.Config{
			BindAddress:               s.cfg.BindAddress,
			Port:                      s.cfg.WebPort,
			FlowRenderingDirectory:    s.cfg.FlowRenderingDirectory,
			ConnectorSetupEnabled:     true,
			ConnectorConfigDirectory:  s.cfg.ConnectorConfigDirectory,
			ConnectorReleaseOverrides: s.cfg.ConnectorReleaseOverrides,
			AllowedHosts:              dexweb.SplitAllowedHosts(s.cfg.AllowedHosts),
			WebVersion:                "dexcli " + s.cfg.version,
		},
		flowServiceClient,
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
		s.printServerLogTailAfterExit(exited, serverLogs, serverLogPath)
		return formatComponentExit(exited, "stopped before readiness")
	default:
	}
	s.printReady(webURL, dexAddress, serverVersion.versionIfAnswered(), dataDirectory, serverLogPath)
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
		s.printServerLogTailAfterExit(exited, serverLogs, serverLogPath)
		return formatComponentExit(exited, "stopped unexpectedly")
	}
}

// openServerLogs returns the router for dex-server-zd output and the path of the full server log.
func (s *supervisor) openServerLogs() (*serverLogRouter, string, error) {
	if s.cfg.isExplicit("server-log-folder") {
		// dex-server-zd writes this file itself through --log-dir.
		return newServerLogRouter(nil, s.stderr, s.cfg.ServerLogLevel), filepath.Join(s.cfg.LogDirectory, zdLogFileName), nil
	}
	logFile, logPath, err := createServerLogFile(s.cfg.ServerLogFileDirectory, s.cfg.DexPort)
	if err != nil {
		return nil, "", err
	}
	return newServerLogRouter(logFile, s.stderr, s.cfg.ServerLogLevel), logPath, nil
}

func (s *supervisor) printServerLogTailAfterExit(exited componentExit, serverLogs *serverLogRouter, serverLogPath string) {
	if exited.name == "Dex Server" {
		s.printServerLogTail(serverLogs, serverLogPath)
	}
}

// printServerLogTail shows the latest server lines, including those the terminal filter hid.
func (s *supervisor) printServerLogTail(serverLogs *serverLogRouter, serverLogPath string) {
	recentLines := serverLogs.RecentLines()
	fmt.Fprintf(s.stderr, "\nLast dex-server-zd output (full log: %s):\n", serverLogPath)
	for _, line := range recentLines {
		fmt.Fprintln(s.stderr, line)
	}
}

func formatComponentExit(exited componentExit, phase string) error {
	if exited.err == nil {
		return fmt.Errorf("%s %s", exited.name, phase)
	}
	return fmt.Errorf("%s %s: %w", exited.name, phase, exited.err)
}

// serverVersionLookup is one GetServerInfo call that runs while Dex Web starts, so startup never waits for it.
type serverVersionLookup struct {
	// result receives the one answer; its buffer lets the goroutine finish when nobody reads it.
	result chan string
}

// startServerVersionLookup starts the lookup; it ends within serverVersionLookupTimeout or when ctx ends.
func startServerVersionLookup(ctx context.Context, client dexpb.FlowServiceClient) *serverVersionLookup {
	lookup := &serverVersionLookup{result: make(chan string, 1)}
	go func() { lookup.result <- describeServerVersion(ctx, client) }()
	return lookup
}

// versionIfAnswered returns the version when the lookup already answered, or "" at once otherwise.
func (l *serverVersionLookup) versionIfAnswered() string {
	select {
	case version := <-l.result:
		return version
	default:
		return ""
	}
}

// describeServerVersion returns the server version and protocol range for the ready banner, or "" when unknown.
func describeServerVersion(ctx context.Context, client dexpb.FlowServiceClient) string {
	lookupCtx, cancel := context.WithTimeout(ctx, serverVersionLookupTimeout)
	defer cancel()
	serverInfo, err := client.GetServerInfo(lookupCtx, &emptypb.Empty{})
	if err != nil {
		// The banner is informational; a slow or failed lookup omits the line instead of failing startup.
		return ""
	}
	return formatServerVersion(serverInfo)
}

// formatServerVersion returns "<version> (protocol <minimum>-<current>)", or "" when the server reports no version.
func formatServerVersion(serverInfo *dexpb.ServerInfo) string {
	version := serverInfo.GetServerVersion()
	if version == "" {
		return ""
	}
	minimum, current := serverInfo.GetMinimumSupportedProtocolVersion(), serverInfo.GetCurrentProtocolVersion()
	switch {
	case current == 0:
		return version
	case minimum == 0 || minimum == current:
		return fmt.Sprintf("%s (protocol %d)", version, current)
	default:
		return fmt.Sprintf("%s (protocol %d-%d)", version, minimum, current)
	}
}

func (s *supervisor) printReady(webURL string, dexAddress string, serverVersion string, serverDataDirectory string, serverLogPath string) {
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
	fmt.Fprintf(s.stdout, "Dex Server:    %s\n", dexAddress)
	if serverVersion != "" {
		fmt.Fprintf(s.stdout, "Server version: %s\n", serverVersion)
	}
	fmt.Fprintf(s.stdout, "State dir:     %s\n", serverDataDirectory)
	fmt.Fprintf(s.stdout, "Logs:          %s\n", serverLogPath)
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
	manager := newConnectorsMCPManagerForThisPlatform(&s.cfg.ConnectorsMCP, proxy, s.stderr, proxyURL, connectorsMCPHint)
	return manager, webListener, nil
}

func (s *supervisor) printUpdateNotice(ctx context.Context) {
	go func() {
		update.PrintNotice(ctx, s.stderr, s.cfg.version)
	}()
}

func reserveOwnedListeners(cfg *Config) (*ownedListeners, error) {
	allocated := make(map[string]string)
	dexListener, err := bindServicePort("Dex Server", cfg.isExplicit("dex-port"), cfg, &cfg.DexPort, allocated)
	if err != nil {
		return nil, err
	}
	listeners := &ownedListeners{dex: dexListener}
	webListener, err := bindServicePort("Dex Web", cfg.isExplicit("web-port"), cfg, &cfg.WebPort, allocated)
	if err != nil {
		return nil, errors.Join(err, listeners.Close())
	}
	listeners.web = webListener
	return listeners, nil
}

func bindServicePort(
	name string,
	explicit bool,
	cfg *Config,
	port *int,
	allocated map[string]string,
) (net.Listener, error) {
	for candidate := *port; candidate <= 65535; candidate++ {
		address := net.JoinHostPort(cfg.BindAddress, strconv.Itoa(candidate))
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
	return errors.Join(closeListener(l.web), closeListener(l.dex))
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
