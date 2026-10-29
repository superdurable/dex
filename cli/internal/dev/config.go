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
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	defaultBindAddress      = "127.0.0.1"
	defaultDexPort          = 8801
	defaultWebPort          = 8802
	defaultZDStartupTimeout = 60 * time.Second
	zdBinaryName            = "dex-server-zd"
)

var flagOrder = []string{
	"attribute-store-config",
	"bind-address",
	"connector-config-dir",
	"connector-release-override",
	"dex-port",
	"engine",
	"flow-rendering-dir",
	"open",
	"server-log-folder",
	"web-port",
	"zd-binary",
	"zd-config",
	"zd-data-dir",
}

type Config struct {
	// Engine defaults to zd; --engine zd is accepted for compatibility.
	Engine string
	// ZDBinary defaults to DEX_SERVER_ZD_BINARY. When neither --zd-binary nor DEX_SERVER_ZD_BINARY is set, dexcli
	// uses dex-server-zd from the running dexcli executable's directory, then its symlink-resolved directory, then PATH.
	ZDBinary string
	// ZDDataDirectory defaults to $HOME/.dex/zd/<dex-port> after the Dex port is selected.
	ZDDataDirectory string
	// ZDConfigPath defaults empty; when set, dexcli passes it to dex-server-zd dev as --config overrides.
	ZDConfigPath string
	// BindAddress defaults to 127.0.0.1 for all owned listeners.
	BindAddress string
	// DexPort defaults to 8801 for the Dex gRPC server.
	DexPort int
	// WebPort defaults to 8802 for Dex Web.
	WebPort int
	// LogDirectory defaults empty; server-zd logs to stderr unless a directory is supplied.
	LogDirectory string
	// StateDirectory defaults to $HOME/.dex and stores server-zd data.
	StateDirectory string
	// AttributeStoreConfigPath defaults empty and loads Attribute Store settings from standard Dex YAML when set.
	// dexcli passes the file to dex-server-zd dev as --attribute-store-config.
	AttributeStoreConfigPath string
	// FlowRenderingDirectory defaults empty and supplies Flow Definition Graph JSON files to Dex Web.
	FlowRenderingDirectory string
	// ConnectorConfigDirectory defaults to $HOME/.dex/connectors and stores local connector credentials.
	ConnectorConfigDirectory string
	// ConnectorReleaseOverrides maps Connector IDs to local release artifact directories.
	ConnectorReleaseOverrides map[string]string
	// OpenBrowser defaults true and opens Dex Web after readiness.
	OpenBrowser bool
	// ZDStartupTimeout defaults to 60 seconds.
	ZDStartupTimeout time.Duration
	// ShutdownTimeout defaults to 10 seconds.
	ShutdownTimeout time.Duration

	explicitLocalFlags map[string]bool
	version            string
}

func parseConfig(args []string, output io.Writer) (*Config, error) {
	if err := rejectRemovedTemporalFlags(args); err != nil {
		return nil, err
	}
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
	flags.StringVar(&cfg.Engine, "engine", cfg.Engine, "compatibility option; server-zd is the only engine")
	flags.StringVar(&cfg.ZDBinary, "zd-binary", cfg.ZDBinary, "dex-server-zd executable (default DEX_SERVER_ZD_BINARY, then next to dexcli, then PATH)")
	flags.StringVar(&cfg.ZDDataDirectory, "zd-data-dir", "", "zd state directory (default $HOME/.dex/zd/<dex-port>)")
	flags.StringVar(&cfg.ZDConfigPath, "zd-config", "", "dex-server-zd YAML file with configuration overrides")
	flags.IntVar(&cfg.DexPort, "dex-port", cfg.DexPort, "Dex gRPC port")
	flags.StringVar(
		&cfg.FlowRenderingDirectory,
		"flow-rendering-dir",
		"",
		"directory containing Flow Definition Graph JSON files",
	)
	flags.StringVar(
		&cfg.ConnectorConfigDirectory,
		"connector-config-dir",
		cfg.ConnectorConfigDirectory,
		"directory containing local Connector configuration",
	)
	connectorReleaseOverrides := connectorReleaseOverrideFlag{values: cfg.ConnectorReleaseOverrides}
	flags.Var(
		&connectorReleaseOverrides,
		"connector-release-override",
		"local Connector release override as connector-id=artifact-directory (repeatable)",
	)
	flags.BoolVar(&cfg.OpenBrowser, "open", true, "open Dex Web after startup")
	flags.IntVar(&cfg.WebPort, "web-port", cfg.WebPort, "Dex Web port")
	flags.StringVar(
		&cfg.LogDirectory,
		"server-log-folder",
		"",
		"keep server logs in this folder (default stderr)",
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
	cfg.ConnectorReleaseOverrides = connectorReleaseOverrides.values
	cfg.explicitLocalFlags = make(map[string]bool)
	flags.Visit(func(item *flag.Flag) {
		switch item.Name {
		case "dex-port", "web-port", "server-log-folder", "attribute-store-config", "zd-binary", "zd-data-dir", "zd-config":
			cfg.explicitLocalFlags[item.Name] = true
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
		Engine:                    "zd",
		ZDBinary:                  os.Getenv("DEX_SERVER_ZD_BINARY"),
		BindAddress:               defaultBindAddress,
		DexPort:                   defaultDexPort,
		WebPort:                   defaultWebPort,
		StateDirectory:            stateDirectory,
		ConnectorConfigDirectory:  filepath.Join(stateDirectory, "connectors"),
		ConnectorReleaseOverrides: make(map[string]string),
		OpenBrowser:               true,
		ZDStartupTimeout:          defaultZDStartupTimeout,
		ShutdownTimeout:           10 * time.Second,
		explicitLocalFlags:        make(map[string]bool),
	}, nil
}

func defaultStateDirectory() (string, error) {
	homeDirectory, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory for Dex state: %w", err)
	}
	return filepath.Join(homeDirectory, ".dex"), nil
}

func (c *Config) validate() error {
	if c.Engine != "zd" {
		return fmt.Errorf("Temporal engine was removed; server-zd is the only engine (use --engine zd or omit --engine): %q", c.Engine)
	}
	invocationPath, err := dexcliInvocationPath(os.Args[0])
	if err != nil {
		// The resolved executable directory and PATH remain candidates.
		invocationPath = ""
	}
	executablePath, err := os.Executable()
	if err != nil {
		// PATH lookup still works without the dexcli executable directory.
		executablePath = ""
	}
	binaryPath, err := resolveZDBinary(c.ZDBinary, invocationPath, executablePath)
	if err != nil {
		return err
	}
	c.ZDBinary = binaryPath
	if c.isExplicit("zd-data-dir") {
		c.ZDDataDirectory = strings.TrimSpace(c.ZDDataDirectory)
		if c.ZDDataDirectory == "" {
			return fmt.Errorf("zd data directory is required")
		}
		dataDirectory, err := filepath.Abs(c.ZDDataDirectory)
		if err != nil {
			return fmt.Errorf("resolve zd data directory: %w", err)
		}
		c.ZDDataDirectory = dataDirectory
	}
	if c.isExplicit("zd-config") {
		configPath, err := resolveZDConfigPath(c.ZDConfigPath)
		if err != nil {
			return err
		}
		c.ZDConfigPath = configPath
	}
	if c.isExplicit("attribute-store-config") {
		configPath, err := resolveRegularFilePath(c.AttributeStoreConfigPath, "Attribute Store config file")
		if err != nil {
			return err
		}
		c.AttributeStoreConfigPath = configPath
	}
	if net.ParseIP(c.BindAddress) == nil {
		return fmt.Errorf("bind address must be an IP address: %q", c.BindAddress)
	}
	c.FlowRenderingDirectory = strings.TrimSpace(c.FlowRenderingDirectory)
	c.ConnectorConfigDirectory = strings.TrimSpace(c.ConnectorConfigDirectory)
	if c.ConnectorConfigDirectory == "" {
		return fmt.Errorf("connector config directory is required")
	}
	absoluteConnectorConfigDirectory, err := filepath.Abs(c.ConnectorConfigDirectory)
	if err != nil {
		return fmt.Errorf("resolve connector config directory: %w", err)
	}
	c.ConnectorConfigDirectory = absoluteConnectorConfigDirectory
	for connectorID, directory := range c.ConnectorReleaseOverrides {
		absoluteDirectory, err := filepath.Abs(strings.TrimSpace(directory))
		if err != nil {
			return fmt.Errorf("resolve Connector release override %q: %w", connectorID, err)
		}
		c.ConnectorReleaseOverrides[connectorID] = absoluteDirectory
	}
	for name, port := range map[string]int{
		"dex-port": c.DexPort,
		"web-port": c.WebPort,
	} {
		if port < 1 || port > 65535 {
			return fmt.Errorf("%s must be between 1 and 65535", name)
		}
	}
	if c.explicitLocalFlags["server-log-folder"] {
		c.LogDirectory = strings.TrimSpace(c.LogDirectory)
		if c.LogDirectory == "" {
			return fmt.Errorf("server log folder is required")
		}
	}
	return validateDistinctAddresses(c.ownedAddresses())
}

type connectorReleaseOverrideFlag struct {
	values map[string]string
}

func (value *connectorReleaseOverrideFlag) String() string {
	return ""
}

func (value *connectorReleaseOverrideFlag) Set(raw string) error {
	connectorID, directory, found := strings.Cut(raw, "=")
	connectorID = strings.TrimSpace(connectorID)
	directory = strings.TrimSpace(directory)
	if !found || !validConnectorOverrideID(connectorID) || directory == "" {
		return fmt.Errorf("Connector release override must be connector-id=artifact-directory")
	}
	if value.values == nil {
		value.values = make(map[string]string)
	}
	if _, exists := value.values[connectorID]; exists {
		return fmt.Errorf("Connector release override %q is duplicated", connectorID)
	}
	value.values[connectorID] = directory
	return nil
}

func validConnectorOverrideID(value string) bool {
	if len(value) < 2 || len(value) > 63 || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	for _, character := range value[1:] {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '-' {
			return false
		}
	}
	return true
}

func (c *Config) ownedAddresses() map[string]string {
	addresses := map[string]string{
		"Dex Server": net.JoinHostPort(c.BindAddress, strconv.Itoa(c.DexPort)),
		"Dex Web":    net.JoinHostPort(c.BindAddress, strconv.Itoa(c.WebPort)),
	}
	return addresses
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

func resolveZDBinary(configuredBinary string, invocationPath string, executablePath string) (string, error) {
	if configuredBinary != "" {
		binaryPath, err := exec.LookPath(configuredBinary)
		if err != nil {
			return "", fmt.Errorf("dex-server-zd binary %q was not found; use --zd-binary or DEX_SERVER_ZD_BINARY: %w", configuredBinary, err)
		}
		return binaryPath, nil
	}
	searchedDirectories := dexcliExecutableDirectories(invocationPath, executablePath)
	for _, directory := range searchedDirectories {
		// A missing or non-executable sibling falls through to the next location.
		if binaryPath, err := exec.LookPath(filepath.Join(directory, zdBinaryName)); err == nil {
			return binaryPath, nil
		}
	}
	binaryPath, err := exec.LookPath(zdBinaryName)
	if err != nil {
		searchedLocations := "PATH"
		if len(searchedDirectories) > 0 {
			searchedLocations = strings.Join(searchedDirectories, ", ") + ", or PATH"
		}
		return "", fmt.Errorf(
			"dex-server-zd was not found in %s; reinstall dexcli, or use --zd-binary or DEX_SERVER_ZD_BINARY: %w",
			searchedLocations, err)
	}
	return binaryPath, nil
}

// dexcliInvocationPath returns the absolute path dexcli was invoked through, keeping any symlink, or an error when
// the path cannot be made absolute or a bare command name is not on PATH.
// os.Executable cannot provide it because Linux reports the symlink target.
func dexcliInvocationPath(argument string) (string, error) {
	if argument == "" {
		return "", fmt.Errorf("dexcli invocation path is empty")
	}
	if !strings.ContainsRune(argument, os.PathSeparator) && !strings.ContainsRune(argument, '/') {
		lookedUpPath, err := exec.LookPath(argument)
		if err != nil {
			return "", fmt.Errorf("find dexcli invocation %q on PATH: %w", argument, err)
		}
		argument = lookedUpPath
	}
	absolutePath, err := filepath.Abs(argument)
	if err != nil {
		return "", fmt.Errorf("resolve dexcli invocation path %q: %w", argument, err)
	}
	return absolutePath, nil
}

// dexcliExecutableDirectories returns the directory of invocationPath, then the directory of the symlink-resolved
// executablePath when different. Empty paths contribute no directory.
func dexcliExecutableDirectories(invocationPath string, executablePath string) []string {
	var directories []string
	if invocationPath != "" {
		directories = append(directories, filepath.Dir(invocationPath))
	}
	if executablePath == "" {
		return directories
	}
	resolvedPath, err := filepath.EvalSymlinks(executablePath)
	if err != nil {
		// The unresolved directory is still a valid candidate.
		resolvedPath = executablePath
	}
	resolvedDirectory := filepath.Dir(resolvedPath)
	if len(directories) == 0 || resolvedDirectory != directories[0] {
		directories = append(directories, resolvedDirectory)
	}
	return directories
}

// resolveZDConfigPath returns the absolute path of an existing regular zd configuration file, or an error naming
// the empty, unresolvable, unreadable, or non-regular path.
func resolveZDConfigPath(path string) (string, error) {
	return resolveRegularFilePath(path, "zd config file")
}

// resolveRegularFilePath returns the absolute path of an existing regular file, or an error that starts with
// description and names the empty, unresolvable, unreadable, or non-regular path.
func resolveRegularFilePath(path string, description string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("%s is required", description)
	}
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", description, err)
	}
	info, err := os.Stat(absolutePath)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", description, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("%s %q is not a regular file", description, absolutePath)
	}
	return absolutePath, nil
}

func (c *Config) zdDataDirectory() string {
	if c.ZDDataDirectory != "" {
		return c.ZDDataDirectory
	}
	return filepath.Join(c.StateDirectory, "zd", strconv.Itoa(c.DexPort))
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

func rejectRemovedTemporalFlags(arguments []string) error {
	for _, argument := range arguments {
		if argument == "--" {
			break
		}
		if !strings.HasPrefix(argument, "-") {
			continue
		}
		name, _, _ := strings.Cut(strings.TrimLeft(argument, "-"), "=")
		if strings.HasPrefix(name, "external-temporal-") || strings.HasPrefix(name, "temporal-") || name == "sqlite-db-filename" || name == "verbose-engine-log" || name == "blob-store-dir" {
			return fmt.Errorf("--%s: Temporal engine was removed; server-zd is the only engine; use --zd-data-dir and --zd-config for server-zd storage", name)
		}
	}
	return nil
}
