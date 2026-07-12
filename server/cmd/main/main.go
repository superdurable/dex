package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"syscall"

	"github.com/superdurable/dex/server/cmd"
	"github.com/superdurable/dex/server/common/log"
	"github.com/superdurable/dex/server/common/log/tag"
	"github.com/superdurable/dex/server/config"
)

func main() {
	logger := log.NewDefaultLogger()

	configPathFlag := flag.String("config", "", "path to YAML config file")
	flag.Parse()

	configPath := *configPathFlag
	if configPath == "" {
		configPath = os.Getenv(config.EnvConfigPath)
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		logger.Error("Failed to load config", tag.Error(err))
		os.Exit(1)
	}

	// Re-create logger from loaded config so all log settings (level, format,
	// output file, etc.) take effect.
	logger = log.MustNewLogger(&cfg.Log)

	logger.Info("Loaded config", tag.JsonValue(redactedConfigForLogging(cfg)))

	if valErr := cfg.Shard.Validate(); valErr != nil {
		logger.Error("Invalid shard config", tag.Error(valErr))
		os.Exit(1)
	}
	if valErr := cfg.Tasklist.Validate(); valErr != nil {
		logger.Error("Invalid tasklist config", tag.Error(valErr))
		os.Exit(1)
	}
	if valErr := cfg.TaskProcessor.Validate(cfg.Shard.LeaseExpiryBuffer); valErr != nil {
		logger.Error("Invalid task processor config", tag.Error(valErr))
		os.Exit(1)
	}

	app, err := cmd.NewServerApp(cfg, logger)
	if err != nil {
		logger.Error("Failed to create server", tag.Error(err))
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigCh
		logger.Info("Received shutdown signal")
		app.Stop()
		cancel()
	}()

	if err := app.Start(ctx); err != nil {
		logger.Error("Server exited with error", tag.Error(err))
		os.Exit(1)
	}
}

func redactedConfigForLogging(cfg config.Config) config.Config {
	const placeholder = "[REDACTED]"
	out := cfg
	if out.Persistence.Mongo.URI != "" {
		out.Persistence.Mongo.URI = placeholder
	}
	for _, ptr := range []*string{
		&out.Persistence.Mongo.Shards.URI,
		&out.Persistence.Mongo.Runs.URI,
		&out.Persistence.Mongo.Blobs.URI,
		&out.Persistence.Mongo.Tasklists.URI,
		&out.Persistence.Mongo.Visibility.URI,
		&out.Persistence.Mongo.History.URI,
	} {
		if *ptr != "" {
			*ptr = placeholder
		}
	}
	if out.Metrics.Datadog != nil {
		datadog := *out.Metrics.Datadog
		if datadog.APIKey != "" {
			datadog.APIKey = placeholder
		}
		out.Metrics.Datadog = &datadog
	}
	return out
}
