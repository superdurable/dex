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
	defaultBindAddress        = "127.0.0.1"
	defaultDexPort            = 8801
	defaultWebPort            = 8802
	defaultTemporalPort       = 7233
	defaultTemporalUIPort     = 8233
	defaultTemporalNamespace  = "default"
	localSQLiteFileName       = "dex.sqlite.db"
	localBlobStoreName        = "dex.blobs"
	dexServerLogFileName      = "dex-server.log"
	temporalEngineLogFileName = "temporal-engine-server.log"
)

var flagOrder = []string{
	"attribute-store-config",
	"bind-address",
	"blob-store-dir",
	"dex-port",
	"open",
	"web-port",
	"sqlite-db-filename",
	"server-log-folder",
	"external-temporal-address",
	"external-temporal-namespace",
}

type Config struct {
	// BindAddress defaults to 127.0.0.1 for all owned listeners.
	BindAddress string
	// DexPort defaults to 8801 for the Dex gRPC server.
	DexPort int
	// WebPort defaults to 8802 for Dex Web.
	WebPort int
	// ExternalTemporalAddress selects external Temporal when non-empty. Default is local mode.
	ExternalTemporalAddress string
	// TemporalNamespace defaults to default.
	TemporalNamespace string
	// TemporalPort defaults to 7233 in local mode.
	TemporalPort int
	// TemporalUIPort defaults to 8233 in local mode.
	TemporalUIPort int
	// SQLiteDBFilename defaults to $HOME/.dex/dev/<temporal-port>/dex.sqlite.db in local mode.
	SQLiteDBFilename string
	// LogDirectory defaults to a temp directory deleted on clean exit.
	LogDirectory string
	// StateDirectory defaults to $HOME/.dex and stores auto-assigned Temporal SQLite files.
	StateDirectory string
	// BlobStoreDirectory defaults to $HOME/.dex/blobs unless SQLiteDBFilename selects its adjacent dex.blobs store.
	BlobStoreDirectory string
	// AttributeStoreConfigPath defaults empty and loads Attribute Store settings from standard Dex YAML when set.
	AttributeStoreConfigPath string
	// OpenBrowser defaults true and opens Dex Web after readiness.
	OpenBrowser bool
	// StartupTimeout defaults to 45 seconds.
	StartupTimeout time.Duration
	// ShutdownTimeout defaults to 10 seconds.
	ShutdownTimeout time.Duration

	explicitLocalFlags map[string]bool
	// blobStoreDirectoryDefault defaults true and allows SQLiteDBFilename to select its adjacent store.
	blobStoreDirectoryDefault bool
	version                   string
}

func parseConfig(args []string, output io.Writer) (*Config, error) {
	cfg, err := defaultConfig()
	if err != nil {
		return nil, err
	}
	flags := flag.NewFlagSet("dexcli dev", flag.ContinueOnError)
	flags.SetOutput(output)
	flags.StringVar(
		&cfg.AttributeStoreConfigPath,
		"attribute-store-config",
		"",
		"Dex YAML file supplying attributeStore settings",
	)
	flags.StringVar(&cfg.BindAddress, "bind-address", cfg.BindAddress, "address for local services")
	flags.StringVar(
		&cfg.BlobStoreDirectory,
		"blob-store-dir",
		cfg.BlobStoreDirectory,
		"Dex blob storage directory",
	)
	flags.IntVar(&cfg.DexPort, "dex-port", cfg.DexPort, "Dex gRPC port")
	flags.BoolVar(&cfg.OpenBrowser, "open", true, "open Dex Web after startup")
	flags.IntVar(&cfg.WebPort, "web-port", cfg.WebPort, "Dex Web port")
	flags.StringVar(
		&cfg.SQLiteDBFilename,
		"sqlite-db-filename",
		"",
		"local SQLite file (default $HOME/.dex/dev/<port>/dex.sqlite.db)",
	)
	flags.StringVar(
		&cfg.LogDirectory,
		"server-log-folder",
		"",
		"keep server logs in this folder (default temp folder, deleted on exit)",
	)
	flags.StringVar(&cfg.ExternalTemporalAddress, "external-temporal-address", "", "external Temporal host:port")
	flags.StringVar(
		&cfg.TemporalNamespace,
		"external-temporal-namespace",
		cfg.TemporalNamespace,
		"external Temporal namespace",
	)
	flags.Usage = func() {
		fmt.Fprintln(output, "Usage: dexcli dev [flags]")
		printFlags(output, flags, flagOrder)
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
		case "dex-port", "web-port", "sqlite-db-filename", "server-log-folder", "external-temporal-namespace":
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
		OpenBrowser:               true,
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
		"Temporal port":    c.TemporalPort,
		"Temporal UI port": c.TemporalUIPort,
	} {
		if port < 1 || port > 65535 {
			return fmt.Errorf("%s must be between 1 and 65535", name)
		}
	}
	if c.TemporalNamespace == "" {
		return fmt.Errorf("temporal namespace is required")
	}
	if c.ExternalTemporalAddress != "" {
		if strings.Contains(c.ExternalTemporalAddress, "://") {
			return fmt.Errorf("external Temporal address must be host:port, not a URL")
		}
		if _, _, err := net.SplitHostPort(c.ExternalTemporalAddress); err != nil {
			return fmt.Errorf("external Temporal address must be host:port: %w", err)
		}
		for _, name := range []string{"sqlite-db-filename"} {
			if c.explicitLocalFlags[name] {
				return fmt.Errorf("--%s cannot be used with --external-temporal-address", name)
			}
		}
	} else if c.explicitLocalFlags["external-temporal-namespace"] {
		return fmt.Errorf("--external-temporal-namespace cannot be used without --external-temporal-address")
	}
	if c.explicitLocalFlags["server-log-folder"] {
		c.LogDirectory = strings.TrimSpace(c.LogDirectory)
		if c.LogDirectory == "" {
			return fmt.Errorf("server log folder is required")
		}
	}
	return validateDistinctAddresses(c.ownedAddresses())
}

func (c *Config) ownedAddresses() map[string]string {
	addresses := map[string]string{
		"Dex Server": net.JoinHostPort(c.BindAddress, strconv.Itoa(c.DexPort)),
		"Dex Web":    net.JoinHostPort(c.BindAddress, strconv.Itoa(c.WebPort)),
	}
	if c.ExternalTemporalAddress == "" {
		addresses["Temporal"] = net.JoinHostPort(c.BindAddress, strconv.Itoa(c.TemporalPort))
		addresses["Temporal Web"] = net.JoinHostPort(c.BindAddress, strconv.Itoa(c.TemporalUIPort))
	}
	return addresses
}

func (c *Config) temporalAddress() string {
	if c.ExternalTemporalAddress != "" {
		return c.ExternalTemporalAddress
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

func (c *Config) autoSQLiteDBFilename() string {
	return filepath.Join(c.StateDirectory, "dev", strconv.Itoa(c.TemporalPort), localSQLiteFileName)
}

func (c *Config) adjacentBlobStoreDirectory() string {
	return filepath.Join(filepath.Dir(c.SQLiteDBFilename), localBlobStoreName)
}

func (c *Config) dexServerLogPath() string {
	return filepath.Join(c.LogDirectory, dexServerLogFileName)
}

func (c *Config) temporalEngineLogPath() string {
	return filepath.Join(c.LogDirectory, temporalEngineLogFileName)
}

func (c *Config) writeTemporalStartupRecord(logs io.Writer) error {
	database := c.sqliteDBDirectory()
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

func (c *Config) sqliteDBDirectory() string {
	if c.SQLiteDBFilename == "" {
		return ""
	}
	return filepath.Dir(c.SQLiteDBFilename)
}

func printFlags(output io.Writer, flags *flag.FlagSet, names []string) {
	for _, name := range names {
		item := flags.Lookup(name)
		if item == nil {
			panic("missing flag " + name)
		}
		typeName, usage := flag.UnquoteUsage(item)
		fmt.Fprintf(output, "  -%s", item.Name)
		if typeName != "" {
			fmt.Fprintf(output, " %s", typeName)
		}
		fmt.Fprintf(output, "\n        %s", usage)
		if item.DefValue != "" && item.DefValue != "false" && item.DefValue != "0" {
			if typeName == "string" {
				fmt.Fprintf(output, " (default %q)", item.DefValue)
			} else {
				fmt.Fprintf(output, " (default %s)", item.DefValue)
			}
		}
		fmt.Fprintln(output)
	}
}
