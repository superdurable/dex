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
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/superdurable/dex/config"
)

const (
	defaultBindAddress       = "127.0.0.1"
	defaultDexPort           = 8801
	defaultWebPort           = 8802
	defaultTemporalPort      = 7233
	defaultTemporalUIPort    = 8233
	defaultTemporalNamespace = "default"
	localSQLiteFileName      = "dex.sqlite.db"
	localBlobStoreName       = "dex.blobs"
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
	// TemporalDBFilename defaults to $HOME/.dex/dev/<temporal-port>/dex.sqlite.db in local mode.
	TemporalDBFilename string
	// TemporalLogFile defaults empty and discards local Temporal process logs.
	TemporalLogFile string
	// StateDirectory defaults to $HOME/.dex and stores auto-assigned Temporal SQLite files.
	StateDirectory string
	// BlobStoreDirectory defaults to $HOME/.dex/blobs unless TemporalDBFilename selects its adjacent dex.blobs store.
	BlobStoreDirectory string
	// AttributeStoreConfigPath defaults empty and loads Attribute Store settings from standard Dex YAML when set.
	AttributeStoreConfigPath string
	// OpenBrowser defaults false and opens Dex Web after readiness.
	OpenBrowser bool
	// StartupTimeout defaults to 45 seconds.
	StartupTimeout time.Duration
	// ShutdownTimeout defaults to 10 seconds.
	ShutdownTimeout time.Duration

	explicitLocalFlags map[string]bool
	// blobStoreDirectoryDefault defaults true and allows TemporalDBFilename to select its adjacent store.
	blobStoreDirectoryDefault bool
}

func parseConfig(args []string, output io.Writer) (*Config, error) {
	cfg, err := defaultConfig()
	if err != nil {
		return nil, err
	}
	flags := flag.NewFlagSet("dexcli dev", flag.ContinueOnError)
	flags.SetOutput(output)
	flags.StringVar(&cfg.BindAddress, "bind-address", cfg.BindAddress, "address for local services")
	flags.IntVar(&cfg.DexPort, "dex-port", cfg.DexPort, "Dex gRPC port")
	flags.IntVar(&cfg.WebPort, "web-port", cfg.WebPort, "Dex Web port")
	flags.StringVar(&cfg.TemporalAddress, "temporal-address", "", "external Temporal host:port")
	flags.StringVar(&cfg.TemporalNamespace, "temporal-namespace", cfg.TemporalNamespace, "Temporal namespace")
	flags.IntVar(&cfg.TemporalPort, "temporal-port", cfg.TemporalPort, "local Temporal gRPC port")
	flags.IntVar(&cfg.TemporalUIPort, "temporal-ui-port", cfg.TemporalUIPort, "local Temporal Web port")
	flags.StringVar(
		&cfg.TemporalDBFilename,
		"temporal-db-filename",
		"",
		"local Temporal SQLite file (default $HOME/.dex/dev/<temporal-port>/dex.sqlite.db)",
	)
	flags.StringVar(
		&cfg.TemporalLogFile,
		"temporal-log-file",
		"",
		"write local Temporal server and Web logs to this file",
	)
	flags.StringVar(
		&cfg.BlobStoreDirectory,
		"blob-store-dir",
		cfg.BlobStoreDirectory,
		"Dex blob storage directory",
	)
	flags.StringVar(
		&cfg.AttributeStoreConfigPath,
		"attribute-store-config",
		"",
		"Dex YAML file supplying attributeStore settings",
	)
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
		case "dex-port", "web-port", "temporal-port", "temporal-ui-port", "temporal-db-filename", "temporal-log-file":
			cfg.explicitLocalFlags[item.Name] = true
		case "blob-store-dir":
			cfg.blobStoreDirectoryDefault = false
		}
	})
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func defaultConfig() (*Config, error) {
	stateDirectory, err := defaultStateDirectory()
	if err != nil {
		return nil, err
	}
	return &Config{
		BindAddress:               defaultBindAddress,
		DexPort:                   defaultDexPort,
		WebPort:                   defaultWebPort,
		TemporalNamespace:         defaultTemporalNamespace,
		TemporalPort:              defaultTemporalPort,
		TemporalUIPort:            defaultTemporalUIPort,
		StateDirectory:            stateDirectory,
		BlobStoreDirectory:        filepath.Join(stateDirectory, "blobs"),
		StartupTimeout:            45 * time.Second,
		ShutdownTimeout:           10 * time.Second,
		explicitLocalFlags:        make(map[string]bool),
		blobStoreDirectoryDefault: true,
	}, nil
}

func defaultStateDirectory() (string, error) {
	homeDirectory, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory for Dex state: %w", err)
	}
	return filepath.Join(homeDirectory, ".dex"), nil
}

func defaultBlobStoreDirectory() (string, error) {
	stateDirectory, err := defaultStateDirectory()
	if err != nil {
		return "", err
	}
	return filepath.Join(stateDirectory, "blobs"), nil
}

func (c *Config) validate() error {
	if net.ParseIP(c.BindAddress) == nil {
		return fmt.Errorf("bind address must be an IP address: %q", c.BindAddress)
	}
	if strings.TrimSpace(c.BlobStoreDirectory) == "" {
		return fmt.Errorf("blob store directory is required")
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
		for _, name := range []string{"temporal-port", "temporal-ui-port", "temporal-db-filename", "temporal-log-file"} {
			if c.explicitLocalFlags[name] {
				return fmt.Errorf("--%s cannot be used with --temporal-address", name)
			}
		}
	}
	if c.TemporalLogFile != "" {
		c.TemporalLogFile = strings.TrimSpace(c.TemporalLogFile)
		if c.TemporalLogFile == "" {
			return fmt.Errorf("temporal log file is required")
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

func (c *Config) loadAttributeStoreConfig() (config.AttributeStoreConfig, error) {
	if c.AttributeStoreConfigPath == "" {
		return config.AttributeStoreConfig{}, nil
	}
	serverConfig, err := config.NewConfig(c.AttributeStoreConfigPath)
	if err != nil {
		return config.AttributeStoreConfig{}, fmt.Errorf("load Attribute Store config: %w", err)
	}
	if err := serverConfig.AttributeStore.Validate(); err != nil {
		return config.AttributeStoreConfig{}, fmt.Errorf("validate Attribute Store config: %w", err)
	}
	if len(serverConfig.AttributeStore.Stores) == 0 {
		return config.AttributeStoreConfig{}, fmt.Errorf("Attribute Store config does not define any stores")
	}
	return serverConfig.AttributeStore, nil
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

func (c *Config) isExplicit(flagName string) bool {
	return c.explicitLocalFlags[flagName]
}

func (c *Config) autoTemporalDBFilename() string {
	return filepath.Join(c.StateDirectory, "dev", strconv.Itoa(c.TemporalPort), localSQLiteFileName)
}

func (c *Config) adjacentBlobStoreDirectory() string {
	return filepath.Join(filepath.Dir(c.TemporalDBFilename), localBlobStoreName)
}

func (c *Config) writeTemporalStartupRecord(logs io.Writer) error {
	database := c.temporalDBDirectory()
	if database == "" {
		database = "in-memory"
	}
	_, err := fmt.Fprintf(
		logs,
		"Temporal:     %s\nTemporal Web: %s\nTemporal DB:  %s\n",
		c.temporalAddress(),
		"http://"+net.JoinHostPort(c.BindAddress, strconv.Itoa(c.TemporalUIPort)),
		database,
	)
	return err
}

func (c *Config) temporalDBDirectory() string {
	if c.TemporalDBFilename == "" {
		return ""
	}
	return filepath.Dir(c.TemporalDBFilename)
}
