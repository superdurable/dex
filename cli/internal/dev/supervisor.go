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
	process, err := startZDProcess(s.cfg, dataDirectory, dexAddress, s.stderr)
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
		},
		dexpb.NewFlowServiceClient(dexConnection),
		assets.Files,
	)
	if err != nil {
		return err
	}
	resources.web = webServer
	componentExits := make(chan componentExit, 2)
	go func() { componentExits <- componentExit{name: "Dex Web", err: webServer.Serve(listeners.web)} }()
	go func() {
		<-process.done
		componentExits <- componentExit{name: "Dex Server", err: process.Err()}
	}()
	webURL := "http://" + listeners.web.Addr().String()
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
	s.printReady(webURL, dexAddress, dataDirectory)
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

func (s *supervisor) printReady(webURL string, dexAddress string, serverDataDirectory string) {
	fmt.Fprintln(s.stdout)
	fmt.Fprintln(s.stdout, "Dex development environment is ready")
	fmt.Fprintln(s.stdout)
	fmt.Fprintf(s.stdout, "Dex Web:       %s\n", webURL)
	fmt.Fprintf(s.stdout, "Dex Server:    %s\n", dexAddress)
	fmt.Fprintf(s.stdout, "State dir:     %s\n", serverDataDirectory)
	if s.cfg.LogDirectory != "" {
		fmt.Fprintf(s.stdout, "Server log folder: %s\n", s.cfg.LogDirectory)
	}
	fmt.Fprintln(s.stdout)
	fmt.Fprintln(s.stdout, "Press Ctrl+C to stop.")
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
