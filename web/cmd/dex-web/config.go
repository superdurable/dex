// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/superdurable/dex/web"
)

type commandConfig struct {
	// web defaults to HTTP on 127.0.0.1:8802, without definitions or Worker routing overrides.
	web web.Config
	// flowServiceAddress defaults to 127.0.0.1:8801, the gRPC target this process connects to.
	flowServiceAddress string
	// isTLSEnabled defaults false and enables verified TLS for the FlowService connection.
	isTLSEnabled bool
	// tlsCAFile defaults empty, using system trust roots without additional PEM certificates.
	tlsCAFile string
	// tlsServerName defaults empty, verifying the FlowService target host.
	tlsServerName string
	// credentialFile defaults empty, sending no bearer credential.
	credentialFile string
	// shouldPrintVersion defaults false and exits after printing the build version when enabled.
	shouldPrintVersion bool
}

type commandEnvironmentDefaults struct {
	flags *flag.FlagSet
	err   error
}

func parseCommandConfig(arguments []string, output io.Writer) (*commandConfig, error) {
	cfg := &commandConfig{}
	flags := flag.NewFlagSet("dex-web", flag.ContinueOnError)
	flags.SetOutput(output)
	flags.StringVar(&cfg.web.BindAddress, "bind-address", "127.0.0.1", "HTTP bind address")
	flags.IntVar(&cfg.web.Port, "port", web.DefaultPort, "HTTP bind port")
	flags.StringVar(&cfg.flowServiceAddress, "flow-service-address", "127.0.0.1:8801", "FlowService gRPC target")
	flags.StringVar(&cfg.web.FlowRenderingDirectory, "flow-rendering-dir", "", "Flow Definition Graph JSON directory")
	flags.BoolVar(&cfg.web.IsStartFlowWorkerTargetHeadless, "start-flow-worker-target-headless", false, "use headless routing for address targets")
	flags.BoolVar(&cfg.web.IsStartFlowWorkerTargetPull, "start-flow-worker-target-pull", false, "default to pull Worker targets")
	flags.BoolVar(&cfg.isTLSEnabled, "tls", false, "enable FlowService TLS")
	flags.StringVar(&cfg.tlsCAFile, "tls-ca-file", "", "additional PEM CA certificates (requires TLS)")
	flags.StringVar(&cfg.tlsServerName, "tls-server-name", "", "TLS server name override (requires TLS)")
	flags.StringVar(&cfg.credentialFile, "flow-service-credential-file", "", "rotating bearer token file")
	flags.BoolVar(&cfg.shouldPrintVersion, "version", false, "print version and exit")
	environmentDefaults := &commandEnvironmentDefaults{flags: flags}
	flags.VisitAll(environmentDefaults.applyDefault)
	if environmentDefaults.err != nil {
		return nil, environmentDefaults.err
	}
	if err := flags.Parse(arguments); err != nil {
		return nil, err
	}
	if flags.NArg() != 0 {
		return nil, fmt.Errorf("unexpected arguments: %s", strings.Join(flags.Args(), " "))
	}
	if cfg.shouldPrintVersion {
		return cfg, nil
	}
	if cfg.web.Port < 1 || cfg.web.Port > 65535 {
		return nil, fmt.Errorf("port must be between 1 and 65535: %s", strconv.Itoa(cfg.web.Port))
	}
	if strings.TrimSpace(cfg.flowServiceAddress) == "" {
		return nil, fmt.Errorf("FlowService address must not be empty")
	}
	if !cfg.isTLSEnabled && (cfg.tlsCAFile != "" || cfg.tlsServerName != "") {
		return nil, fmt.Errorf("TLS CA file and server name require --tls")
	}
	return cfg, nil
}

func (defaults *commandEnvironmentDefaults) applyDefault(option *flag.Flag) {
	if option.Name == "version" {
		return
	}
	environmentName := map[string]string{
		"bind-address":                      "DEX_WEB_BIND_ADDRESS",
		"port":                              "DEX_WEB_HTTP_PORT_NUMBER",
		"flow-service-address":              "DEX_WEB_FLOW_SERVICE_ADDRESS",
		"flow-rendering-dir":                "DEX_WEB_FLOW_RENDERING_DIRECTORY",
		"start-flow-worker-target-headless": "DEX_WEB_START_FLOW_WORKER_TARGET_HEADLESS",
		"start-flow-worker-target-pull":     "DEX_WEB_START_FLOW_WORKER_TARGET_PULL",
		"tls":                               "DEX_WEB_TLS",
		"tls-ca-file":                       "DEX_WEB_TLS_CA_FILE",
		"tls-server-name":                   "DEX_WEB_TLS_SERVER_NAME",
		"flow-service-credential-file":      "DEX_WEB_FLOW_SERVICE_CREDENTIAL_FILE",
	}[option.Name]
	if value, exists := os.LookupEnv(environmentName); exists {
		if err := defaults.flags.Set(option.Name, value); err != nil {
			defaults.err = fmt.Errorf("%s: %w", environmentName, err)
		}
	}
}
