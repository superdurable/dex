package config

import (
	"os"
	"time"
)

// Config holds all configuration for a dex server instance.
// Fields with `env` tags are automatically populated from environment variables
// via config.ApplyEnvOverrides.
type Config struct {
	// GRPCListenAddress is the bind address for the RunsService gRPC API.
	// Clients and SDK workers connect here.
	// Default: ":7233"
	GRPCListenAddress string `yaml:"grpcListenAddress" env:"DEX_GRPC_LISTEN_ADDRESS"`

	// MatchingGRPCListenAddress is the bind address for the MatchingService gRPC.
	// Workers connect here for the ConnectWorker bidi stream.
	// Run-service calls DispatchRun/DeliverChannelMessages here.
	// Default: ":7234"
	MatchingGRPCListenAddress string `yaml:"matchingGrpcListenAddress" env:"DEX_MATCHING_GRPC_LISTEN_ADDRESS"`

	// OpsGRPCListenAddress is the bind address for the OpsService gRPC. Hosts
	// the read-only operational APIs (ListRuns, GetHistoryEvents) on its
	// own port.
	// Default: ":7235"
	OpsGRPCListenAddress string `yaml:"opsGrpcListenAddress" env:"DEX_OPS_GRPC_LISTEN_ADDRESS"`

	// Persistence configures durable backing services such as MongoDB.
	Persistence PersistenceConfig `yaml:"persistence"`

	// Metrics controls metric emission and exporter configuration.
	Metrics MetricsConfig `yaml:"metrics"`

	// Shard controls shard ownership, leasing, and cluster membership.
	Shard ShardConfig `yaml:"shard"`

	// Tasklist controls tasklist ownership and partitioning for the matching service.
	Tasklist TasklistConfig `yaml:"tasklist"`

	// RunService controls the RunService behavior.
	RunService RunServiceConfig `yaml:"runService"`

	// MatchingService controls the MatchingService's worker interaction logic.
	MatchingService MatchingServiceConfig `yaml:"matchingService"`

	// TaskProcessor controls task processing parameters.
	TaskProcessor TaskProcessorConfig `yaml:"taskProcessor"`

	// Log controls logging behavior (level, format, output).
	Log LoggerConfig `yaml:"log"`

	// MemberID uniquely identifies this server instance.
	// In Kubernetes, setting POD_NAME lets env overrides produce a stable member ID
	// without explicitly setting DEX_MEMBER_ID.
	// Default: "{hostname}-{ISO8601 datetime}", e.g. "web-01-2026-04-13T12:30:00Z".
	MemberID string `yaml:"memberID" env:"DEX_MEMBER_ID,POD_NAME"`
}

// DefaultConfig returns a Config with sensible defaults.
func DefaultConfig() Config {
	hostname, _ := os.Hostname()
	memberID := hostname + "-" + time.Now().UTC().Format("2006-01-02T15:04:05Z")
	return Config{
		GRPCListenAddress:         ":7233",
		MatchingGRPCListenAddress: ":7234",
		OpsGRPCListenAddress:      ":7235",
		Persistence:               DefaultPersistenceConfig(),
		Metrics:                   DefaultMetricsConfig(),
		Shard:                     DefaultShardConfig(),
		Tasklist:                  DefaultTasklistConfig(),
		RunService:                DefaultRunServiceConfig(),
		MatchingService:           DefaultMatchingEngineConfig(),
		TaskProcessor:             DefaultTaskProcessorConfig(),
		Log:                       DefaultLoggerConfig(),
		MemberID:                  memberID,
	}
}
