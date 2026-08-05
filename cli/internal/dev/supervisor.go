// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
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

	"github.com/superdurable/dex/config"
	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/service"
	"github.com/superdurable/dex/service/bootstrap"
	"github.com/superdurable/dex/service/common/ptr"
	dexweb "github.com/superdurable/dex/web"
	"github.com/superdurable/dex/web/assets"
	enumspb "go.temporal.io/api/enums/v1"
	operatorservicepb "go.temporal.io/api/operatorservice/v1"
	temporalclient "go.temporal.io/sdk/client"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
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
	blobStoreDirectory, temporaryBlobStore, err := s.prepareBlobStoreDirectory()
	if err != nil {
		return err
	}
	if temporaryBlobStore {
		defer func() {
			if cleanupErr := os.RemoveAll(blobStoreDirectory); cleanupErr != nil {
				runErr = errors.Join(runErr, fmt.Errorf("remove temporary blob store: %w", cleanupErr))
			}
		}()
	}
	listeners, err := reserveOwnedListeners(s.cfg)
	if err != nil {
		return err
	}
	defer func() {
		runErr = errors.Join(runErr, listeners.Close())
	}()

	var temporal *temporalProcess
	if s.cfg.TemporalAddress == "" {
		temporal, err = startTemporalProcess(s.cfg, s.stdout, s.stderr)
		if err != nil {
			return err
		}
		defer func() {
			runErr = errors.Join(runErr, temporal.Stop(s.cfg.ShutdownTimeout))
		}()
	}

	startupCtx, cancelStartup := context.WithTimeout(ctx, s.cfg.StartupTimeout)
	defer cancelStartup()
	temporalClient, err := waitForTemporal(startupCtx, s.cfg, temporal)
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
	if err := validateSearchAttributes(startupCtx, temporalClient, s.cfg.TemporalNamespace); err != nil {
		temporalClient.Close()
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
	if temporal != nil {
		temporalWebURL := "http://" + net.JoinHostPort(s.cfg.BindAddress, strconv.Itoa(s.cfg.TemporalUIPort))
		if err := waitForHTTP(startupCtx, temporalWebURL, temporal); err != nil {
			temporalClient.Close()
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("wait for Temporal Web: %w", err)
		}
	}
	temporalClient.Close()

	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()
	componentExits := make(chan componentExit, 2)
	dexRuntime, err := s.startDexRuntime(runCtx, listeners.dex, componentExits, blobStoreDirectory)
	if err != nil {
		return err
	}

	dexConnection, err := waitForDex(startupCtx, listeners.dex.Addr().String(), componentExits)
	if err != nil {
		cancelRun()
		dexRuntime.Close()
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
	defer func() {
		runErr = errors.Join(runErr, dexConnection.Close())
	}()

	webServer := dexweb.NewServer(
		&dexweb.Config{BindAddress: s.cfg.BindAddress, Port: s.cfg.WebPort},
		dexpb.NewFlowServiceClient(dexConnection),
		assets.Files,
	)
	go func() {
		componentExits <- componentExit{name: "Dex Web", err: webServer.Serve(listeners.web)}
	}()
	webURL := "http://" + listeners.web.Addr().String()
	if err := waitForComponentHTTP(startupCtx, webURL+"/healthz", componentExits); err != nil {
		if ctx.Err() != nil {
			return s.shutdown(runCtx, cancelRun, webServer, dexRuntime, nil)
		}
		return s.shutdown(runCtx, cancelRun, webServer, dexRuntime, err)
	}

	s.printReady(webURL, listeners.dex.Addr().String())
	if s.cfg.OpenBrowser {
		if err := openBrowser(webURL); err != nil {
			return s.shutdown(runCtx, cancelRun, webServer, dexRuntime, err)
		}
	}

	select {
	case <-ctx.Done():
		runErr = nil
	case exited := <-componentExits:
		if exited.err == nil {
			runErr = fmt.Errorf("%s stopped unexpectedly", exited.name)
		} else {
			runErr = fmt.Errorf("%s stopped unexpectedly: %w", exited.name, exited.err)
		}
	case <-temporalDone(temporal):
		if temporal.Err() == nil {
			runErr = fmt.Errorf("Temporal stopped unexpectedly")
		} else {
			runErr = fmt.Errorf("Temporal stopped unexpectedly: %w", temporal.Err())
		}
	}
	return s.shutdown(runCtx, cancelRun, webServer, dexRuntime, runErr)
}

func (s *supervisor) prepareBlobStoreDirectory() (string, bool, error) {
	if s.cfg.BlobStoreDirectory != "" {
		directory, err := filepath.Abs(s.cfg.BlobStoreDirectory)
		if err != nil {
			return "", false, fmt.Errorf("resolve blob store directory: %w", err)
		}
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return "", false, fmt.Errorf("create blob store directory: %w", err)
		}
		return directory, false, nil
	}
	if s.cfg.TemporalDBFilename != "" {
		directory, err := filepath.Abs(s.cfg.TemporalDBFilename + ".dex-blobs")
		if err != nil {
			return "", false, fmt.Errorf("resolve blob store directory: %w", err)
		}
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return "", false, fmt.Errorf("create blob store directory: %w", err)
		}
		return directory, false, nil
	}
	directory, err := os.MkdirTemp("", "dexcli-blobs-*")
	if err != nil {
		return "", false, fmt.Errorf("create temporary blob store: %w", err)
	}
	if s.cfg.TemporalAddress != "" {
		if _, err := fmt.Fprintln(
			s.stderr,
			"Warning: step inputs and large values will not survive dexcli restart; use --blob-store-dir to persist them.",
		); err != nil {
			return "", false, errors.Join(
				fmt.Errorf("write blob store warning: %w", err),
				os.RemoveAll(directory),
			)
		}
	}
	return directory, true, nil
}

func (s *supervisor) startDexRuntime(
	ctx context.Context,
	listener net.Listener,
	exits chan<- componentExit,
	blobStoreDirectory string,
) (*bootstrap.Runtime, error) {
	dexConfig := &config.Config{
		Log: config.Logger{
			Stdout:   true,
			Level:    "info",
			Encoding: "console",
		},
		Api: config.ApiConfig{Port: s.cfg.DexPort},
		ExternalStorage: config.ExternalStorageConfig{
			Enabled:                true,
			LazyLoading:            ptr.Any(true),
			ThresholdInBytes:       1024,
			HistoryRetentionInDays: 1,
			SupportedStorages: []config.BlobStorageConfig{{
				Status:         config.StorageStatusActive,
				StorageId:      "dexcli-local",
				StorageType:    config.StorageTypeLocal,
				LocalDirectory: blobStoreDirectory,
				CleanupStrategy: config.CleanupStrategy{
					CleanupStrategyType:    config.CleanupStrategyTypeAfterAllRunsDeleted,
					CleanupFrequencyInDays: 1,
				},
			}},
		},
		Interpreter: config.Interpreter{
			Temporal: &config.TemporalConfig{
				HostPort:  s.cfg.temporalAddress(),
				Namespace: s.cfg.TemporalNamespace,
			},
			InterpreterActivityConfig: config.InterpreterActivityConfig{
				InternalServiceTarget: listener.Addr().String(),
			},
		},
	}
	dexRuntime, err := bootstrap.New(dexConfig, &bootstrap.Options{
		Services:        bootstrap.Services{API: true, Interpreter: true},
		APIListener:     listener,
		ShutdownTimeout: s.cfg.ShutdownTimeout,
	})
	if err != nil {
		return nil, fmt.Errorf("create Dex Server: %w", err)
	}
	go func() {
		exits <- componentExit{name: "Dex Server", err: dexRuntime.Run(ctx)}
	}()
	return dexRuntime, nil
}

func (s *supervisor) shutdown(
	ctx context.Context,
	cancel context.CancelFunc,
	webServer *dexweb.Server,
	dexRuntime *bootstrap.Runtime,
	runErr error,
) error {
	shutdownCtx, cancelShutdown := context.WithTimeout(context.WithoutCancel(ctx), s.cfg.ShutdownTimeout)
	defer cancelShutdown()
	webErr := webServer.Shutdown(shutdownCtx)
	cancel()
	dexRuntime.Close()
	if webErr != nil && !errors.Is(webErr, context.Canceled) {
		runErr = errors.Join(runErr, fmt.Errorf("stop Dex Web: %w", webErr))
	}
	return runErr
}

func (s *supervisor) printReady(webURL string, dexAddress string) {
	fmt.Fprintln(s.stdout)
	fmt.Fprintln(s.stdout, "Dex development environment is ready")
	fmt.Fprintln(s.stdout)
	fmt.Fprintf(s.stdout, "Dex Web:       %s\n", webURL)
	fmt.Fprintf(s.stdout, "Dex Server:    %s\n", dexAddress)
	if s.cfg.TemporalAddress == "" {
		fmt.Fprintf(s.stdout, "Temporal Web:  http://%s\n", net.JoinHostPort(s.cfg.BindAddress, strconv.Itoa(s.cfg.TemporalUIPort)))
		fmt.Fprintf(s.stdout, "Temporal:      %s\n", s.cfg.temporalAddress())
	} else {
		fmt.Fprintf(s.stdout, "Temporal:      %s (external)\n", s.cfg.TemporalAddress)
	}
	fmt.Fprintln(s.stdout)
	fmt.Fprintln(s.stdout, "Press Ctrl+C to stop.")
}

func reserveOwnedListeners(cfg *Config) (*ownedListeners, error) {
	dexListener, err := listenFor("Dex Server", cfg.ownedAddresses()["Dex Server"])
	if err != nil {
		return nil, err
	}
	listeners := &ownedListeners{dex: dexListener}
	webListener, err := listenFor("Dex Web", cfg.ownedAddresses()["Dex Web"])
	if err != nil {
		closeErr := listeners.Close()
		return nil, errors.Join(err, closeErr)
	}
	listeners.web = webListener
	if cfg.TemporalAddress != "" {
		return listeners, nil
	}
	for _, name := range []string{"Temporal", "Temporal Web"} {
		probe, err := listenFor(name, cfg.ownedAddresses()[name])
		if err != nil {
			return nil, errors.Join(err, listeners.Close())
		}
		if err := probe.Close(); err != nil {
			return nil, errors.Join(fmt.Errorf("release %s port: %w", name, err), listeners.Close())
		}
	}
	return listeners, nil
}

func listenFor(name string, address string) (net.Listener, error) {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return nil, fmt.Errorf("cannot start %s: %s is already in use: %w", name, address, err)
	}
	return listener, nil
}

func (l *ownedListeners) Close() error {
	var closeErr error
	for _, listener := range []net.Listener{l.web, l.dex} {
		if listener == nil {
			continue
		}
		if err := listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			closeErr = errors.Join(closeErr, err)
		}
	}
	return closeErr
}

func waitForTemporal(
	ctx context.Context,
	cfg *Config,
	process *temporalProcess,
) (temporalclient.Client, error) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var lastErr error
	for {
		temporalClient, err := temporalclient.Dial(temporalclient.Options{
			HostPort:  cfg.temporalAddress(),
			Namespace: cfg.TemporalNamespace,
			ConnectionOptions: temporalclient.ConnectionOptions{
				GetSystemInfoTimeout: time.Second,
			},
		})
		if err == nil {
			checkCtx, cancel := context.WithTimeout(ctx, time.Second)
			_, err = temporalClient.CheckHealth(checkCtx, &temporalclient.CheckHealthRequest{})
			cancel()
			if err == nil {
				return temporalClient, nil
			}
			temporalClient.Close()
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("wait for Temporal at %s: %w", cfg.temporalAddress(), errors.Join(ctx.Err(), lastErr))
		case <-temporalDone(process):
			return nil, fmt.Errorf("Temporal exited before readiness: %w", process.Err())
		case <-ticker.C:
		}
	}
}

func validateSearchAttributes(ctx context.Context, temporalClient temporalclient.Client, namespace string) error {
	response, err := temporalClient.OperatorService().ListSearchAttributes(
		ctx,
		&operatorservicepb.ListSearchAttributesRequest{Namespace: namespace},
	)
	if err != nil {
		return fmt.Errorf("list Temporal search attributes for namespace %q: %w", namespace, err)
	}
	requiredAttributes := []struct {
		name          string
		attributeType enumspb.IndexedValueType
	}{
		{service.SearchAttributeDexWorkflowType, enumspb.INDEXED_VALUE_TYPE_KEYWORD},
		{service.SearchAttributeActiveStepTypes, enumspb.INDEXED_VALUE_TYPE_KEYWORD_LIST},
	}
	for _, requiredAttribute := range requiredAttributes {
		attributeType, exists := response.GetCustomAttributes()[requiredAttribute.name]
		if !exists {
			return fmt.Errorf(
				"Temporal namespace %q is missing search attribute %s (%s)",
				namespace,
				requiredAttribute.name,
				requiredAttribute.attributeType,
			)
		}
		if attributeType != requiredAttribute.attributeType {
			return fmt.Errorf(
				"Temporal search attribute %s must be %s, got %s",
				requiredAttribute.name,
				requiredAttribute.attributeType,
				attributeType,
			)
		}
	}
	return nil
}

func waitForDex(
	ctx context.Context,
	address string,
	exits <-chan componentExit,
) (*grpc.ClientConn, error) {
	connection, err := grpc.NewClient(
		address,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(config.DefaultGrpcMaxMessageBytes),
			grpc.MaxCallSendMsgSize(config.DefaultGrpcMaxMessageBytes),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("create Dex Server client: %w", err)
	}
	healthClient := healthpb.NewHealthClient(connection)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var lastErr error
	for {
		checkCtx, cancel := context.WithTimeout(ctx, time.Second)
		response, checkErr := healthClient.Check(checkCtx, &healthpb.HealthCheckRequest{
			Service: dexpb.FlowService_ServiceDesc.ServiceName,
		})
		cancel()
		if checkErr == nil && response.GetStatus() == healthpb.HealthCheckResponse_SERVING {
			return connection, nil
		}
		lastErr = checkErr
		select {
		case <-ctx.Done():
			closeErr := connection.Close()
			return nil, errors.Join(fmt.Errorf("wait for Dex Server at %s: %w", address, errors.Join(ctx.Err(), lastErr)), closeErr)
		case exited := <-exits:
			closeErr := connection.Close()
			return nil, errors.Join(fmt.Errorf("%s exited before readiness: %w", exited.name, exited.err), closeErr)
		case <-ticker.C:
		}
	}
}

func waitForHTTP(ctx context.Context, url string, process *temporalProcess) error {
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
		case <-temporalDone(process):
			return fmt.Errorf("Temporal exited before Web readiness: %w", process.Err())
		case <-ticker.C:
		}
	}
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

func temporalDone(process *temporalProcess) <-chan struct{} {
	if process == nil {
		return nil
	}
	return process.Done()
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
