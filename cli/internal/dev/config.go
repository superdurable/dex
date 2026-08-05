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
	"flag"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"
)

const (
	defaultBindAddress       = "127.0.0.1"
	defaultDexPort           = 8801
	defaultWebPort           = 8802
	defaultTemporalPort      = 7233
	defaultTemporalUIPort    = 8233
	defaultTemporalNamespace = "default"
)

type Config struct {
	// BindAddress defaults to 127.0.0.1 for all owned listeners.
	BindAddress string
	// DexPort defaults to 8801 for the Dex gRPC server.
	DexPort int
	// WebPort defaults to 8802 for Dex Web.
	WebPort int
	// TemporalAddress selects external Temporal when non-empty. Default is local mode.
	TemporalAddress string
	// TemporalNamespace defaults to default.
	TemporalNamespace string
	// TemporalPort defaults to 7233 in local mode.
	TemporalPort int
	// TemporalUIPort defaults to 8233 in local mode.
	TemporalUIPort int
	// TemporalDBFilename defaults to in-memory storage.
	TemporalDBFilename string
	// BlobStoreDirectory persists Dex blobs when set. Default follows the Temporal database lifecycle.
	BlobStoreDirectory string
	// OpenBrowser defaults false and opens Dex Web after readiness.
	OpenBrowser bool
	// StartupTimeout defaults to 45 seconds.
	StartupTimeout time.Duration
	// ShutdownTimeout defaults to 10 seconds.
	ShutdownTimeout time.Duration

	explicitLocalFlags map[string]bool
}

func defaultConfig() *Config {
	return &Config{
		BindAddress:       defaultBindAddress,
		DexPort:           defaultDexPort,
		WebPort:           defaultWebPort,
		TemporalNamespace: defaultTemporalNamespace,
		TemporalPort:      defaultTemporalPort,
		TemporalUIPort:    defaultTemporalUIPort,
		StartupTimeout:    45 * time.Second,
		ShutdownTimeout:   10 * time.Second,
	}
}

func parseConfig(args []string, output io.Writer) (*Config, error) {
	cfg := defaultConfig()
	flags := flag.NewFlagSet("dexcli dev", flag.ContinueOnError)
	flags.SetOutput(output)
	flags.StringVar(&cfg.BindAddress, "bind-address", cfg.BindAddress, "address for local services")
	flags.IntVar(&cfg.DexPort, "dex-port", cfg.DexPort, "Dex gRPC port")
	flags.IntVar(&cfg.WebPort, "web-port", cfg.WebPort, "Dex Web port")
	flags.StringVar(&cfg.TemporalAddress, "temporal-address", "", "external Temporal host:port")
	flags.StringVar(&cfg.TemporalNamespace, "temporal-namespace", cfg.TemporalNamespace, "Temporal namespace")
	flags.IntVar(&cfg.TemporalPort, "temporal-port", cfg.TemporalPort, "local Temporal gRPC port")
	flags.IntVar(&cfg.TemporalUIPort, "temporal-ui-port", cfg.TemporalUIPort, "local Temporal Web port")
	flags.StringVar(&cfg.TemporalDBFilename, "temporal-db-filename", "", "local Temporal SQLite file")
	flags.StringVar(&cfg.BlobStoreDirectory, "blob-store-dir", "", "Dex blob storage directory")
	flags.BoolVar(&cfg.OpenBrowser, "open", false, "open Dex Web after startup")
	flags.Usage = func() {
		fmt.Fprintln(output, "Usage: dexcli dev [flags]")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		return nil, err
	}
	if flags.NArg() != 0 {
		return nil, fmt.Errorf("unexpected arguments: %s", strings.Join(flags.Args(), " "))
	}
	cfg.explicitLocalFlags = make(map[string]bool)
	flags.Visit(func(item *flag.Flag) {
		switch item.Name {
		case "temporal-port", "temporal-ui-port", "temporal-db-filename":
			cfg.explicitLocalFlags[item.Name] = true
		}
	})
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) validate() error {
	if net.ParseIP(c.BindAddress) == nil {
		return fmt.Errorf("bind address must be an IP address: %q", c.BindAddress)
	}
	for name, port := range map[string]int{
		"dex-port":         c.DexPort,
		"web-port":         c.WebPort,
		"temporal-port":    c.TemporalPort,
		"temporal-ui-port": c.TemporalUIPort,
	} {
		if port < 1 || port > 65535 {
			return fmt.Errorf("%s must be between 1 and 65535", name)
		}
	}
	if c.TemporalNamespace == "" {
		return fmt.Errorf("temporal namespace is required")
	}
	if c.TemporalAddress != "" {
		if strings.Contains(c.TemporalAddress, "://") {
			return fmt.Errorf("temporal address must be host:port, not a URL")
		}
		if _, _, err := net.SplitHostPort(c.TemporalAddress); err != nil {
			return fmt.Errorf("temporal address must be host:port: %w", err)
		}
		for _, name := range []string{"temporal-port", "temporal-ui-port", "temporal-db-filename"} {
			if c.explicitLocalFlags[name] {
				return fmt.Errorf("--%s cannot be used with --temporal-address", name)
			}
		}
	}
	return validateDistinctAddresses(c.ownedAddresses())
}

func (c *Config) ownedAddresses() map[string]string {
	addresses := map[string]string{
		"Dex Server": net.JoinHostPort(c.BindAddress, strconv.Itoa(c.DexPort)),
		"Dex Web":    net.JoinHostPort(c.BindAddress, strconv.Itoa(c.WebPort)),
	}
	if c.TemporalAddress == "" {
		addresses["Temporal"] = net.JoinHostPort(c.BindAddress, strconv.Itoa(c.TemporalPort))
		addresses["Temporal Web"] = net.JoinHostPort(c.BindAddress, strconv.Itoa(c.TemporalUIPort))
	}
	return addresses
}

func (c *Config) temporalAddress() string {
	if c.TemporalAddress != "" {
		return c.TemporalAddress
	}
	return net.JoinHostPort(c.BindAddress, strconv.Itoa(c.TemporalPort))
}

func validateDistinctAddresses(addresses map[string]string) error {
	owners := make(map[string]string, len(addresses))
	for name, address := range addresses {
		if owner, exists := owners[address]; exists {
			return fmt.Errorf("%s and %s cannot both use %s", owner, name, address)
		}
		owners[address] = name
	}
	return nil
}
