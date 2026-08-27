// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package config

import (
	"fmt"
	"log"
	"os"
	"time"

	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/service/common/ptr"
	"github.com/uber-go/tally/v4/prometheus"
	temporalWorker "go.temporal.io/sdk/worker"
	cadenceWorker "go.uber.org/cadence/worker"
	"google.golang.org/grpc/codes"
	"gopkg.in/yaml.v3"
)

const (
	StorageStatusActive   = "active"
	StorageStatusInactive = "inactive"
)

const (
	StorageTypeS3    = "s3"
	StorageTypeLocal = "local"
)

const (
	CleanupStrategyTypeAfterAllRunsDeleted CleanupStrategyType = "afterAllRunsDeleted"
)

const (
	// DefaultApiPort is the default FlowService/InternalService gRPC bind port.
	DefaultApiPort = 8801
	// DefaultMaxWaitSeconds caps WaitForFlow / WaitForStepCompletion / WaitForAttribute when MaxWaitSeconds is 0.
	DefaultMaxWaitSeconds int64 = 60
	// DefaultGrpcMaxMessageBytes is 16 MiB so that large attributes can be transported.
	DefaultGrpcMaxMessageBytes = 16 * 1024 * 1024
	// DefaultWorkerConnectionIdleTimeout is how long an idle WorkerService conn may sit unused before eviction.
	DefaultWorkerConnectionIdleTimeout = 10 * time.Minute
	// DefaultMaxWorkerConnections caps the WorkerService dial pool; positive required at runtime.
	DefaultMaxWorkerConnections = 1000
	// DefaultHeadlessAddressRefreshInterval controls headless WorkerService DNS refresh.
	DefaultHeadlessAddressRefreshInterval = time.Minute
	// DefaultMaxStickyRoutingEntries caps remembered flow-to-worker routes.
	DefaultMaxStickyRoutingEntries = 100000
	// DefaultWorkerServiceRequestMaxAttempts includes the first headless WorkerService request.
	DefaultWorkerServiceRequestMaxAttempts = 3
	// DefaultBlobCacheMaxBytes caps the Server Attribute blob cache at one GiB.
	DefaultBlobCacheMaxBytes int64 = 1 << 30
	// DefaultBlobStoreThresholdInBytes offloads Attribute payloads larger than one KiB.
	DefaultBlobStoreThresholdInBytes = 1 << 10
	// DefaultAttributeStoreSchemaSyncInterval refreshes table schemas every minute before jitter.
	DefaultAttributeStoreSchemaSyncInterval = time.Minute
	// DefaultAttributeStoreSyncBatchSize caps items in one Attribute Store upsert.
	DefaultAttributeStoreSyncBatchSize = 100
	// DefaultAttributeStoreSyncAttemptTimeout caps each regular Activity attempt at thirty seconds.
	DefaultAttributeStoreSyncAttemptTimeout = 30 * time.Second
	// DefaultAttributeStoreSyncTotalDuration caps regular Activity retries at one hour.
	DefaultAttributeStoreSyncTotalDuration = time.Hour
	// DefaultAttributeIndexSyncTimeout bounds backend index registration and propagation checks.
	DefaultAttributeIndexSyncTimeout = 2 * time.Minute
	// DefaultStreamMaxMessageBytes limits each serialized Stream Value to 100 KiB.
	DefaultStreamMaxMessageBytes int64 = 100 * 1024
	// DefaultStreamEstimatedMessageOverheadBytes approximates Redis bookkeeping per message.
	DefaultStreamEstimatedMessageOverheadBytes int64 = 512
	// DefaultStreamTrimTriggerPercent starts asynchronous trimming at ninety percent of capacity.
	DefaultStreamTrimTriggerPercent int32 = 90
	// DefaultStreamTrimTargetPercent stops asynchronous trimming at eighty percent of capacity.
	DefaultStreamTrimTargetPercent int32 = 80
	// DefaultStreamBackgroundTrimBatchSize caps messages removed by one atomic Redis trim script.
	DefaultStreamBackgroundTrimBatchSize = 256
	// DefaultStreamTrimLeaseTTL keeps a distributed trim lease alive for five seconds.
	DefaultStreamTrimLeaseTTL = 5 * time.Second
	// DefaultStreamTrimLeaseRetry retries a held distributed trim lease every 100 milliseconds.
	DefaultStreamTrimLeaseRetry = 100 * time.Millisecond
	// DefaultStreamTrimBatchYieldTime pauses one millisecond between atomic trim batches.
	DefaultStreamTrimBatchYieldTime = time.Millisecond
	// DefaultStreamTrimWorkers bounds process-wide asynchronous trim concurrency.
	DefaultStreamTrimWorkers = 4
)

const (
	AttributeStoreTypeMySQL    AttributeStoreType = "mysql"
	AttributeStoreTypePostgres AttributeStoreType = "postgres"
)

var defaultHeadlessFailoverStatusCodes = [...]codes.Code{
	codes.Unavailable,
	codes.DeadlineExceeded,
	codes.Unknown,
}

var (
	DefaultQueryWorkflowFailedRetryPolicy = RetryPolicy{
		InitialInterval:    100 * time.Millisecond,
		BackoffCoefficient: 1,
		MaximumInterval:    2 * time.Second,
		MaximumAttempts:    5,
	}
	DefaultInvokeRPCContinuedAsNewErrorRetryPolicy = RetryPolicy{
		InitialInterval:    100 * time.Millisecond,
		BackoffCoefficient: 2,
		MaximumInterval:    500 * time.Millisecond,
		TotalDuration:      5 * time.Second,
	}
	DefaultAttributeStoreSyncRetryPolicy = RetryPolicy{
		InitialInterval:    100 * time.Millisecond,
		BackoffCoefficient: 2,
		MaximumInterval:    30 * time.Second,
		TotalDuration:      DefaultAttributeStoreSyncTotalDuration,
	}
	DefaultInternalActivityRetryPolicy = RetryPolicy{
		InitialInterval:    100 * time.Millisecond,
		BackoffCoefficient: 2,
		MaximumInterval:    60 * time.Second,
	}
)

type (
	RetryPolicy struct {
		// InitialInterval is the first delay between attempts. Zero uses the owning config field's default.
		InitialInterval time.Duration `yaml:"initialInterval"`
		// BackoffCoefficient multiplies the delay after each retry. Zero uses the owning config field's default.
		BackoffCoefficient float64 `yaml:"backoffCoefficient"`
		// MaximumInterval caps the delay between attempts. Zero uses the owning config field's default.
		MaximumInterval time.Duration `yaml:"maximumInterval"`
		// MaximumAttempts includes the initial attempt. Zero uses the owning config field's default or total-duration bound.
		MaximumAttempts int32 `yaml:"maximumAttempts"`
		// TotalDuration caps elapsed retry time. Zero uses the owning config field's default or disables the duration bound.
		TotalDuration time.Duration `yaml:"totalDuration"`
	}

	Config struct {
		// Log is process logging (stdout/stderr/file, level, encoding).
		Log Logger `yaml:"log"`
		// Api is the public FlowService and internal InternalService gRPC server config.
		Api ApiConfig `yaml:"api"`
		// Worker configures shared WorkerService clients. Immutable after startup.
		Worker WorkerConfig `yaml:"worker"`
		// Interpreter selects Temporal or Cadence and worker activity settings. Exactly one of Temporal/Cadence must be set.
		Interpreter Interpreter `yaml:"interpreter"`
		// BlobStore offloads large Attribute payloads above ThresholdInBytes. Default enabled.
		BlobStore BlobStoreConfig `yaml:"blobStore"`
		// AttributeStore configures optional MySQL/Postgres Attribute synchronization. Default disabled when Stores is empty.
		AttributeStore AttributeStoreConfig `yaml:"attributeStore"`
		// StreamStore configures best-effort resumable Streams. Default disabled when RedisURL is empty.
		StreamStore StreamStoreConfig `yaml:"streamStore"`
	}

	StreamStoreConfig struct {
		// RedisURL is a Redis 7+ Standalone URL. Default empty disables Streams. Immutable after startup.
		RedisURL string `yaml:"redisURL"`
		// MaxMessageBytes limits each serialized Stream Value. Default 102400. Must be positive after defaults.
		MaxMessageBytes int64 `yaml:"maxMessageBytes"`
		// EstimatedMessageOverheadBytes is charged per message beyond payload and identity bytes. Default 512. Must be non-negative.
		EstimatedMessageOverheadBytes int64 `yaml:"estimatedMessageOverheadBytes"`
		// TrimTriggerPercent starts asynchronous trimming at this capacity percentage. Default 90. Valid range is 1 through 99.
		TrimTriggerPercent int32 `yaml:"trimTriggerPercent"`
		// TrimTargetPercent stops asynchronous trimming at this capacity percentage. Default 80. Must be below TrimTriggerPercent.
		TrimTargetPercent int32 `yaml:"trimTargetPercent"`
		// BackgroundTrimBatchSize caps messages removed by one atomic Redis trim script. Default 256. Must be positive after defaults.
		BackgroundTrimBatchSize int `yaml:"backgroundTrimBatchSize"`
		// TrimLeaseTTL controls the Redis lease lifetime for one active trimmer. Default 5s. Must be positive after defaults.
		TrimLeaseTTL time.Duration `yaml:"trimLeaseTTL"`
		// TrimLeaseRetry controls how often a server retries a held trim lease. Default 100ms. Must be positive after defaults.
		TrimLeaseRetry time.Duration `yaml:"trimLeaseRetry"`
		// TrimBatchYieldTime controls the pause between atomic trim batches. Default 1ms. Must be positive after defaults.
		TrimBatchYieldTime time.Duration `yaml:"trimBatchYieldTime"`
		// TrimWorkers bounds concurrent background trim jobs per server process. Default 4. Must be positive after defaults.
		TrimWorkers int `yaml:"trimWorkers"`
	}

	BlobStoreConfig struct {
		// Enabled turns blob offload on or off. Default true when omitted.
		Enabled *bool `yaml:"enabled"`
		// LazyLoading turns lazy loading on or off.
		// When on, server will only send blobIDs to worker for worker APIs(invoke waitFor/execute/RPC) and GetAttribute API.
		// Worker wil call LoadBlobs API to get the actual values.
		// So that worker & server can minimize the data transfer, and worker can cache the values if needed.
		// Default true when omitted (nil).
		LazyLoading *bool `yaml:"lazyLoading"`
		// ThresholdInBytes triggers blob offload above this payload size. Default 1024. Zero uses the default.
		ThresholdInBytes int `yaml:"thresholdInBytes"`
		// SupportedStorages lists blob backends. Exactly one may have Status active for writes; others are read-only.
		SupportedStorages []BlobStoreConfigEntry `yaml:"supportedStorages"`
		// HistoryRetentionInDays must match the Temporal/Cadence history retention. Default 0; configure it explicitly.
		HistoryRetentionInDays int `yaml:"historyRetentionInDays"`
		// BlobCache configures the S3 Attribute disk cache. Default disabled because Directory is empty. Immutable after startup.
		BlobCache BlobCacheConfig `yaml:"blobCache"`
	}

	BlobCacheConfig struct {
		// Directory exclusively stores cached S3 Attribute blobs. Default empty disables caching. Immutable after startup.
		Directory string `yaml:"directory"`
		// MaxBytes caps cache-owned logical file bytes. Default 1073741824. Must be positive when Directory is configured.
		MaxBytes int64 `yaml:"maxBytes"`
	}

	AttributeStoreType string

	AttributeStoreConfig struct {
		// Stores maps FlowConfig names to immutable SQL destinations. Default empty disables Attribute synchronization.
		Stores map[string]AttributeStoreConfigEntry `yaml:"stores"`
		// SchemaSyncInterval refreshes table schemas. Default 1m. Each interval receives uniform ±10% jitter.
		SchemaSyncInterval time.Duration `yaml:"schemaSyncInterval"`
		// SyncBatchSize caps contiguous items per SQL upsert. Default 100. Must be positive after defaults.
		SyncBatchSize int `yaml:"syncBatchSize"`
		// SyncAttemptTimeout caps each regular Activity attempt. Default 30s. Must be positive after defaults.
		SyncAttemptTimeout time.Duration `yaml:"syncAttemptTimeout"`
		// SyncRetryPolicy controls regular Activity retries. Nil or zero fields default to 100ms/30s/2x/unlimited attempts/1h total.
		SyncRetryPolicy *RetryPolicy `yaml:"syncRetryPolicy"`
	}

	AttributeStoreConfigEntry struct {
		// Type selects mysql or postgres. Default empty is invalid for a configured entry. Immutable after startup.
		Type AttributeStoreType `yaml:"type"`
		// DSN is the driver connection string. Default empty is invalid. It may contain credentials and is never logged.
		DSN string `yaml:"dsn"`
		// TableName selects table or schema.table/database.table. Default empty is invalid. Immutable after startup.
		TableName string `yaml:"tableName"`
	}

	StorageStatus       string
	StorageType         string
	CleanupStrategyType string

	CleanupStrategy struct {
		// CleanupStrategyType selects cleanup eligibility. Default and only supported value: afterAllRunsDeleted.
		CleanupStrategyType CleanupStrategyType `yaml:"cleanupStrategyType"`
		// CleanupFrequencyInDays schedules cleanup at midnight every Nth day-of-month. Default 0 disables scheduled cleanup.
		CleanupFrequencyInDays int `yaml:"cleanupFrequencyInDays"`
	}

	BlobStoreConfigEntry struct {
		// Status is "active" (writable) or "inactive" (read-only). Only one active store is allowed.
		Status StorageStatus
		// StorageId identifies this backend inside blob ids persisted on Value.
		StorageId string `yaml:"storageId"`
		// StorageType selects "s3" or "local". Default is empty and invalid when storage is enabled.
		StorageType StorageType `yaml:"storageType"`
		// LocalDirectory stores blobs on the server filesystem for "local". Default empty; a directory is required.
		LocalDirectory string `yaml:"localDirectory"`
		// S3Endpoint is the S3 API base URL (e.g. http://localhost:9000 for MinIO).
		S3Endpoint string `yaml:"s3Endpoint"`
		// S3Bucket is the bucket name for object storage.
		S3Bucket string `yaml:"s3Bucket"`
		// S3Region is the AWS/S3 region string.
		S3Region string `yaml:"s3Region"`
		// S3AccessKey is the access key id for S3 auth.
		S3AccessKey string `yaml:"s3AccessKey"`
		// S3SecretKey is the secret access key for S3 auth.
		S3SecretKey string `yaml:"s3SecretKey"`
		// CleanupStrategy controls automatic blob cleanup. Default type is afterAllRunsDeleted; zero frequency disables scheduling.
		CleanupStrategy CleanupStrategy `yaml:"cleanupStrategy"`
	}

	ApiConfig struct {
		// Port is the TCP port for FlowService and InternalService (plaintext gRPC). Default 8801. Bind is 0.0.0.0:Port; SDKs/integ and the interpreter CAN activity dial this port.
		Port int `yaml:"port"`
		// MaxWaitSeconds caps WaitForFlow, WaitForStepCompletion, and WaitForAttribute. Zero uses DefaultMaxWaitSeconds (60). Positive values are the cap. Negatives are invalid.
		MaxWaitSeconds int64 `yaml:"maxWaitSeconds"`
		// GrpcMaxMessageBytes is MaxRecv/MaxSend for FlowService, InternalService, and WorkerService clients. Default 16 MiB. Must be positive and larger than continue-as-new page size plus overhead.
		GrpcMaxMessageBytes int `yaml:"grpcMaxMessageBytes"`
		// IncludeRPCInputOutputIntoHistory stores RPC input/output in signal history for debugging. Default false. Synchronous Updates always store both.
		IncludeRPCInputOutputIntoHistory bool `yaml:"includeRPCInputOutputIntoHistory"`
		// UseTemporalSynchronousUpdateForAllRPCs routes non-locking Temporal RPCs through synchronous Updates. Default false because Temporal limits concurrent Updates. Enabling trades that capacity for validator rejection and atomic effect commits. Locking Temporal RPCs always use Updates.
		UseTemporalSynchronousUpdateForAllRPCs bool `yaml:"useTemporalSynchronousUpdateForAllRPCs"`
		// QueryWorkflowFailedRetryPolicy retries failed backend queries. Nil or zero fields default to 100ms fixed intervals and 5 attempts.
		QueryWorkflowFailedRetryPolicy *RetryPolicy `yaml:"queryWorkflowFailedRetryPolicy"`
		// InvokeRPCContinuedAsNewErrorRetryPolicy retries transient InvokeRPC failures across current-run changes. Nil or zero fields default to 100ms initial, 2x backoff, 1s maximum, and 5s total duration.
		InvokeRPCContinuedAsNewErrorRetryPolicy *RetryPolicy `yaml:"invokeRPCContinuedAsNewErrorRetryPolicy"`
	}

	WorkerConfig struct {
		// DefaultHeaders are forwarded as outgoing gRPC metadata on WorkerService calls. Empty means none.
		// Default empty.
		DefaultHeaders map[string]string `yaml:"defaultHeaders"`
		// WorkerConnectionIdleTimeout evicts idle, unreferenced WorkerService connections. Non-positive defaults to 10m. Immutable after startup.
		WorkerConnectionIdleTimeout time.Duration `yaml:"workerConnectionIdleTimeout"`
		// MaxWorkerConnections caps the WorkerService connection pool. Non-positive defaults to 1000. Immutable after startup.
		MaxWorkerConnections int `yaml:"maxWorkerConnections"`
		// HeadlessAddressRefreshInterval refreshes headless WorkerService DNS addresses. Non-positive defaults to 60s. Immutable after startup.
		HeadlessAddressRefreshInterval time.Duration `yaml:"headlessAddressRefreshInterval"`
		// MaxStickyRoutingEntries caps remembered flow routes. Non-positive defaults to 100000. Immutable after startup.
		MaxStickyRoutingEntries int `yaml:"maxStickyRoutingEntries"`
		// WorkerServiceRequestMaxAttempts includes the first headless transport attempt. Non-positive defaults to 3. Activity retries are independent.
		WorkerServiceRequestMaxAttempts int `yaml:"workerServiceRequestMaxAttempts"`
		// HeadlessFailoverStatusCodes trigger endpoint failover. Empty defaults to Unavailable (14), DeadlineExceeded (4), and Unknown (2).
		// See https://grpc.io/docs/guides/status-codes/. Immutable after startup.
		HeadlessFailoverStatusCodes []int `yaml:"headlessFailoverStatusCodes"`
	}

	Interpreter struct {
		// Temporal connects the interpreter to a Temporal cluster. Mutually exclusive with Cadence.
		Temporal *TemporalConfig `yaml:"temporal"`
		// Cadence connects the interpreter to a Cadence cluster. Mutually exclusive with Temporal.
		Cadence *CadenceConfig `yaml:"cadence"`
		// DefaultWorkflowConfig is the default FlowConfig applied when StartFlow omits an override. Nil uses package DefaultWorkflowConfig.
		DefaultWorkflowConfig *dexpb.FlowConfig `yaml:"defaultWorkflowConfig"`
		// InterpreterActivityConfig tunes worker→API and worker→WorkerService dialing.
		InterpreterActivityConfig InterpreterActivityConfig `yaml:"interpreterActivityConfig"`
		// VerboseDebug enables extra interpreter debug logs. Default false.
		VerboseDebug bool `yaml:"verboseDebug"`
		// AttributeIndexSyncTimeout bounds registration and backend propagation checks. Default 2m. Immutable after startup.
		AttributeIndexSyncTimeout time.Duration `yaml:"attributeIndexSyncTimeout"`
	}

	TemporalConfig struct {
		// HostPort is the Temporal frontend address. Default localhost:7233. Client dials this gRPC endpoint.
		HostPort string `yaml:"hostPort"`
		// CloudAPIKey authenticates to Temporal Cloud. Empty means no cloud credentials.
		CloudAPIKey string `yaml:"cloudAPIKey"`
		// Namespace is the Temporal namespace. Default "default".
		Namespace string `yaml:"namespace"`
		// Prometheus configures the Temporal SDK metrics exposer. Nil disables.
		Prometheus *prometheus.Configuration `yaml:"prometheus"`
		// WorkerOptions are passed to the Temporal worker. Nil uses SDK defaults.
		WorkerOptions *temporalWorker.Options
	}

	CadenceConfig struct {
		// HostPort is the Cadence frontend address. Default 127.0.0.1:7833.
		HostPort string `yaml:"hostPort"`
		// Domain is the Cadence domain. Default "default".
		Domain string `yaml:"domain"`
		// AdminSecurityToken authorizes Cadence search attribute registration. Default empty.
		AdminSecurityToken string `yaml:"adminSecurityToken"`
		// WorkerOptions are passed to the Cadence worker. Nil uses SDK defaults.
		WorkerOptions *cadenceWorker.Options
	}

	InterpreterActivityConfig struct {
		// InternalServiceTarget is the plaintext gRPC dial target for InternalService (CAN dump). Empty defaults to localhost:<Api.Port>. YAML key internalServiceTarget.
		InternalServiceTarget string `yaml:"internalServiceTarget"`
		// DumpWorkflowInternalActivityConfig tunes the CAN dump activity timeouts/retries. Nil uses activity defaults.
		DumpWorkflowInternalActivityConfig *DumpWorkflowInternalActivityConfig `yaml:"dumpWorkflowInternalActivityConfig"`
		// LogLocalActivityThresholdBytes logs local-activity I/O at warn when serialized size >= this. Zero disables. Default 0.
		LogLocalActivityThresholdBytes int `yaml:"logLocalActivityThresholdBytes"`
	}

	DumpWorkflowInternalActivityConfig struct {
		// StartToCloseTimeout is the activity start-to-close timeout. Zero uses the activity registration default.
		StartToCloseTimeout time.Duration `yaml:"startToCloseTimeout"`
		// RetryPolicy is the activity retry policy. Nil or zero fields default to 100ms/100s/2x/unlimited attempts and no total-duration bound.
		RetryPolicy *RetryPolicy `yaml:"retryPolicy"`
	}

	Logger struct {
		// Stdout sends logs to stdout when true; otherwise stderr (unless OutputFile is set). Default false.
		Stdout bool `yaml:"stdout"`
		// Level is the zap log level string (debug/info/warn/error). Default depends on NewZapLogger.
		Level string `yaml:"level"`
		// OutputFile writes logs to this path when non-empty and Stdout is false.
		OutputFile string `yaml:"outputFile"`
		// LevelKey is the JSON field name for level. Default "level".
		LevelKey string `yaml:"levelKey"`
		// Encoding is "json" or "console". Default "json".
		Encoding string `yaml:"encoding"`
	}
)

// RetryPolicyWithDefaults applies field-specific defaults to a retry policy.
func RetryPolicyWithDefaults(policy *RetryPolicy, defaults RetryPolicy) RetryPolicy {
	effective := RetryPolicy{}
	if policy != nil {
		effective = *policy
	}
	if effective.InitialInterval == 0 {
		effective.InitialInterval = defaults.InitialInterval
	}
	if effective.BackoffCoefficient == 0 {
		effective.BackoffCoefficient = defaults.BackoffCoefficient
	}
	if effective.MaximumInterval == 0 {
		effective.MaximumInterval = defaults.MaximumInterval
	}
	if effective.MaximumAttempts == 0 {
		effective.MaximumAttempts = defaults.MaximumAttempts
	}
	if effective.TotalDuration == 0 {
		effective.TotalDuration = defaults.TotalDuration
	}
	return effective
}

// DefaultWorkflowConfig is used when Interpreter.DefaultWorkflowConfig is nil.
var DefaultWorkflowConfig = &dexpb.FlowConfig{
	ContinueAsNewThreshold: ptr.Any(int32(100)),
	StepDurability:         ptr.Any(dexpb.StepDurability_STEP_DURABILITY_ASYNC),
}

// NewConfig returns a new decoded Config struct.
func NewConfig(configPath string) (*Config, error) {
	log.Printf("Loading configFile=%v\n", configPath)

	cfg := &Config{}

	file, err := os.Open(configPath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	d := yaml.NewDecoder(file)
	d.KnownFields(true)

	if err := d.Decode(&cfg); err != nil {
		return nil, err
	}
	if err := cfg.validateRetryPolicies(); err != nil {
		return nil, err
	}

	return cfg, nil
}

func (c Config) validateRetryPolicies() error {
	queryRetryPolicy := RetryPolicyWithDefaults(
		c.Api.QueryWorkflowFailedRetryPolicy,
		DefaultQueryWorkflowFailedRetryPolicy,
	)
	if err := validateRetryPolicy("api.queryWorkflowFailedRetryPolicy", queryRetryPolicy); err != nil {
		return err
	}

	invokeRPCRetryPolicy := RetryPolicyWithDefaults(
		c.Api.InvokeRPCContinuedAsNewErrorRetryPolicy,
		DefaultInvokeRPCContinuedAsNewErrorRetryPolicy,
	)
	if err := validateRetryPolicy("api.invokeRPCContinuedAsNewErrorRetryPolicy", invokeRPCRetryPolicy); err != nil {
		return err
	}

	attributeStoreRetryPolicy := RetryPolicyWithDefaults(
		c.AttributeStore.SyncRetryPolicy,
		DefaultAttributeStoreSyncRetryPolicy,
	)
	if err := validateRetryPolicy("attributeStore.syncRetryPolicy", attributeStoreRetryPolicy); err != nil {
		return err
	}

	activityConfig := c.Interpreter.InterpreterActivityConfig.DumpWorkflowInternalActivityConfig
	if activityConfig != nil && activityConfig.RetryPolicy != nil {
		dumpWorkflowRetryPolicy := RetryPolicyWithDefaults(
			activityConfig.RetryPolicy,
			DefaultInternalActivityRetryPolicy,
		)
		if err := validateRetryPolicy(
			"interpreter.interpreterActivityConfig.dumpWorkflowInternalActivityConfig.retryPolicy",
			dumpWorkflowRetryPolicy,
		); err != nil {
			return err
		}
	}
	return nil
}

func validateRetryPolicy(name string, policy RetryPolicy) error {
	if policy.InitialInterval <= 0 || policy.MaximumInterval <= 0 ||
		policy.BackoffCoefficient <= 0 || policy.MaximumAttempts < 0 ||
		policy.TotalDuration < 0 {
		return fmt.Errorf("%s values must be positive except maximumAttempts and totalDuration may be zero", name)
	}
	if policy.MaximumInterval < policy.InitialInterval {
		return fmt.Errorf("%s maximumInterval must not be less than initialInterval", name)
	}
	return nil
}

// GetInternalServiceTargetWithDefault returns the plaintext gRPC dial target for InternalService.
func (c Config) GetInternalServiceTargetWithDefault() string {
	if c.Interpreter.InterpreterActivityConfig.InternalServiceTarget != "" {
		return c.Interpreter.InterpreterActivityConfig.InternalServiceTarget
	}
	port := c.Api.Port
	if port == 0 {
		port = DefaultApiPort
	}
	return fmt.Sprintf("localhost:%v", port)
}

// EffectiveMaxWaitSeconds returns the wait cap: DefaultMaxWaitSeconds when MaxWaitSeconds is 0.
// Callers must reject negative MaxWaitSeconds before invoking this.
func (c ApiConfig) EffectiveMaxWaitSeconds() int64 {
	if c.MaxWaitSeconds == 0 {
		return DefaultMaxWaitSeconds
	}
	return c.MaxWaitSeconds
}

// EffectiveMaxMessageBytes returns the configured limit or 100-KiB default.
func (c StreamStoreConfig) EffectiveMaxMessageBytes() int64 {
	if c.MaxMessageBytes == 0 {
		return DefaultStreamMaxMessageBytes
	}
	return c.MaxMessageBytes
}

// EffectiveEstimatedMessageOverheadBytes returns the configured charge or 512-byte default.
func (c StreamStoreConfig) EffectiveEstimatedMessageOverheadBytes() int64 {
	if c.EstimatedMessageOverheadBytes == 0 {
		return DefaultStreamEstimatedMessageOverheadBytes
	}
	return c.EstimatedMessageOverheadBytes
}

// EffectiveTrimTriggerPercent returns the configured trigger or ninety-percent default.
func (c StreamStoreConfig) EffectiveTrimTriggerPercent() int32 {
	if c.TrimTriggerPercent == 0 {
		return DefaultStreamTrimTriggerPercent
	}
	return c.TrimTriggerPercent
}

// EffectiveTrimTargetPercent returns the configured target or eighty-percent default.
func (c StreamStoreConfig) EffectiveTrimTargetPercent() int32 {
	if c.TrimTargetPercent == 0 {
		return DefaultStreamTrimTargetPercent
	}
	return c.TrimTargetPercent
}

// EffectiveBackgroundTrimBatchSize returns the configured batch size or 256-message default.
func (c StreamStoreConfig) EffectiveBackgroundTrimBatchSize() int {
	if c.BackgroundTrimBatchSize == 0 {
		return DefaultStreamBackgroundTrimBatchSize
	}
	return c.BackgroundTrimBatchSize
}

// EffectiveTrimLeaseTTL returns the configured lease lifetime or five-second default.
func (c StreamStoreConfig) EffectiveTrimLeaseTTL() time.Duration {
	if c.TrimLeaseTTL == 0 {
		return DefaultStreamTrimLeaseTTL
	}
	return c.TrimLeaseTTL
}

// EffectiveTrimLeaseRetry returns the configured lease retry delay or 100-millisecond default.
func (c StreamStoreConfig) EffectiveTrimLeaseRetry() time.Duration {
	if c.TrimLeaseRetry == 0 {
		return DefaultStreamTrimLeaseRetry
	}
	return c.TrimLeaseRetry
}

// EffectiveTrimBatchYieldTime returns the configured batch pause or one-millisecond default.
func (c StreamStoreConfig) EffectiveTrimBatchYieldTime() time.Duration {
	if c.TrimBatchYieldTime == 0 {
		return DefaultStreamTrimBatchYieldTime
	}
	return c.TrimBatchYieldTime
}

// EffectiveTrimWorkers returns the configured worker count or four-worker default.
func (c StreamStoreConfig) EffectiveTrimWorkers() int {
	if c.TrimWorkers == 0 {
		return DefaultStreamTrimWorkers
	}
	return c.TrimWorkers
}

// Validate checks enabled Stream Store settings and trimming bounds.
func (c StreamStoreConfig) Validate() error {
	if c.EffectiveMaxMessageBytes() <= 0 {
		return fmt.Errorf("stream store maxMessageBytes must be positive")
	}
	if c.EstimatedMessageOverheadBytes < 0 {
		return fmt.Errorf("stream store estimatedMessageOverheadBytes must be non-negative")
	}
	triggerPercent := c.EffectiveTrimTriggerPercent()
	if triggerPercent < 1 || triggerPercent > 99 {
		return fmt.Errorf("stream store trimTriggerPercent must be between 1 and 99")
	}
	targetPercent := c.EffectiveTrimTargetPercent()
	if targetPercent < 1 || targetPercent >= triggerPercent {
		return fmt.Errorf("stream store trimTargetPercent must be positive and less than trimTriggerPercent")
	}
	if c.EffectiveBackgroundTrimBatchSize() <= 0 {
		return fmt.Errorf("stream store backgroundTrimBatchSize must be positive")
	}
	if c.EffectiveTrimLeaseTTL() <= 0 {
		return fmt.Errorf("stream store trimLeaseTTL must be positive")
	}
	if c.EffectiveTrimLeaseRetry() <= 0 {
		return fmt.Errorf("stream store trimLeaseRetry must be positive")
	}
	if c.EffectiveTrimBatchYieldTime() <= 0 {
		return fmt.Errorf("stream store trimBatchYieldTime must be positive")
	}
	if c.EffectiveTrimWorkers() <= 0 {
		return fmt.Errorf("stream store trimWorkers must be positive")
	}
	return nil
}

// EffectiveLazyLoading returns LazyLoading, defaulting to true when omitted.
func (c BlobStoreConfig) EffectiveLazyLoading() bool {
	if c.LazyLoading == nil {
		return true
	}
	return *c.LazyLoading
}

// EffectiveEnabled returns Enabled, defaulting to true when omitted.
func (c BlobStoreConfig) EffectiveEnabled() bool {
	if c.Enabled == nil {
		return true
	}
	return *c.Enabled
}

// EffectiveThresholdInBytes returns the offload threshold or its one-KiB default.
func (c BlobStoreConfig) EffectiveThresholdInBytes() int {
	if c.ThresholdInBytes == 0 {
		return DefaultBlobStoreThresholdInBytes
	}
	return c.ThresholdInBytes
}

// EffectiveMaxBytes returns the configured cache budget or its one-GiB default.
func (c BlobCacheConfig) EffectiveMaxBytes() int64 {
	if c.MaxBytes == 0 {
		return DefaultBlobCacheMaxBytes
	}
	return c.MaxBytes
}

// Validate checks enabled blob cache settings.
func (c BlobCacheConfig) Validate() error {
	if c.Directory == "" {
		return nil
	}
	if c.EffectiveMaxBytes() <= 0 {
		return fmt.Errorf("blob cache maxBytes must be positive")
	}
	return nil
}

// EffectiveSchemaSyncInterval returns the configured schema refresh interval or one minute.
func (c AttributeStoreConfig) EffectiveSchemaSyncInterval() time.Duration {
	if c.SchemaSyncInterval == 0 {
		return DefaultAttributeStoreSchemaSyncInterval
	}
	return c.SchemaSyncInterval
}

// EffectiveSyncBatchSize returns the configured batch limit or 100.
func (c AttributeStoreConfig) EffectiveSyncBatchSize() int {
	if c.SyncBatchSize == 0 {
		return DefaultAttributeStoreSyncBatchSize
	}
	return c.SyncBatchSize
}

// EffectiveSyncAttemptTimeout returns the configured regular Activity timeout or thirty seconds.
func (c AttributeStoreConfig) EffectiveSyncAttemptTimeout() time.Duration {
	if c.SyncAttemptTimeout == 0 {
		return DefaultAttributeStoreSyncAttemptTimeout
	}
	return c.SyncAttemptTimeout
}

// EffectiveSyncRetryPolicy returns a finite policy with Attribute Store defaults.
func (c AttributeStoreConfig) EffectiveSyncRetryPolicy() *RetryPolicy {
	policy := RetryPolicyWithDefaults(c.SyncRetryPolicy, DefaultAttributeStoreSyncRetryPolicy)
	return &policy
}

// Validate checks Attribute Store names, destinations, timeouts, and retry bounds.
func (c AttributeStoreConfig) Validate() error {
	if c.EffectiveSchemaSyncInterval() <= 0 {
		return fmt.Errorf("attribute store schemaSyncInterval must be positive")
	}
	if c.EffectiveSyncBatchSize() <= 0 {
		return fmt.Errorf("attribute store syncBatchSize must be positive")
	}
	if c.EffectiveSyncAttemptTimeout() <= 0 {
		return fmt.Errorf("attribute store syncAttemptTimeout must be positive")
	}
	policy := c.EffectiveSyncRetryPolicy()
	if policy.InitialInterval <= 0 || policy.MaximumInterval <= 0 ||
		policy.BackoffCoefficient <= 0 || policy.MaximumAttempts < 0 ||
		policy.TotalDuration <= 0 {
		return fmt.Errorf("attribute store syncRetryPolicy values must be positive except maximumAttempts may be zero")
	}
	for name, store := range c.Stores {
		if name == "" {
			return fmt.Errorf("attribute store name must not be empty")
		}
		if store.Type != AttributeStoreTypeMySQL && store.Type != AttributeStoreTypePostgres {
			return fmt.Errorf("attribute store %q has unsupported type %q", name, store.Type)
		}
		if store.DSN == "" || store.TableName == "" {
			return fmt.Errorf("attribute store %q requires dsn and tableName", name)
		}
	}
	return nil
}

func (c CleanupStrategy) CronSchedule() (string, error) {
	strategyType := c.CleanupStrategyType
	if strategyType == "" {
		strategyType = CleanupStrategyTypeAfterAllRunsDeleted
	}
	if strategyType != CleanupStrategyTypeAfterAllRunsDeleted {
		return "", fmt.Errorf("unsupported cleanup strategy type: %s", strategyType)
	}
	if c.CleanupFrequencyInDays < 0 {
		return "", fmt.Errorf("cleanup frequency in days must be non-negative")
	}
	if c.CleanupFrequencyInDays == 0 {
		return "", nil
	}
	if c.CleanupFrequencyInDays == 1 {
		return "0 0 * * *", nil
	}
	return fmt.Sprintf("0 0 */%d * *", c.CleanupFrequencyInDays), nil
}

// EffectiveGrpcMaxMessageBytes returns GrpcMaxMessageBytes or DefaultGrpcMaxMessageBytes.
func (c ApiConfig) EffectiveGrpcMaxMessageBytes() int {
	if c.GrpcMaxMessageBytes <= 0 {
		return DefaultGrpcMaxMessageBytes
	}
	return c.GrpcMaxMessageBytes
}

// EffectiveWorkerConnectionIdleTimeout returns the idle eviction timeout for WorkerService conns.
func (c WorkerConfig) EffectiveWorkerConnectionIdleTimeout() time.Duration {
	if c.WorkerConnectionIdleTimeout <= 0 {
		return DefaultWorkerConnectionIdleTimeout
	}
	return c.WorkerConnectionIdleTimeout
}

// EffectiveMaxWorkerConnections returns the WorkerService pool size cap.
func (c WorkerConfig) EffectiveMaxWorkerConnections() int {
	if c.MaxWorkerConnections <= 0 {
		return DefaultMaxWorkerConnections
	}
	return c.MaxWorkerConnections
}

// EffectiveHeadlessAddressRefreshInterval returns the headless DNS refresh interval.
func (c WorkerConfig) EffectiveHeadlessAddressRefreshInterval() time.Duration {
	if c.HeadlessAddressRefreshInterval <= 0 {
		return DefaultHeadlessAddressRefreshInterval
	}
	return c.HeadlessAddressRefreshInterval
}

// EffectiveMaxStickyRoutingEntries returns the sticky routing LRU capacity.
func (c WorkerConfig) EffectiveMaxStickyRoutingEntries() int {
	if c.MaxStickyRoutingEntries <= 0 {
		return DefaultMaxStickyRoutingEntries
	}
	return c.MaxStickyRoutingEntries
}

// EffectiveWorkerServiceRequestMaxAttempts returns total attempts per WorkerService request.
func (c WorkerConfig) EffectiveWorkerServiceRequestMaxAttempts() int {
	if c.WorkerServiceRequestMaxAttempts <= 0 {
		return DefaultWorkerServiceRequestMaxAttempts
	}
	return c.WorkerServiceRequestMaxAttempts
}

// EffectiveHeadlessFailoverStatusCodes returns configured gRPC codes or defaults.
func (c WorkerConfig) EffectiveHeadlessFailoverStatusCodes() []codes.Code {
	if len(c.HeadlessFailoverStatusCodes) == 0 {
		return append([]codes.Code(nil), defaultHeadlessFailoverStatusCodes[:]...)
	}
	statusCodes := make([]codes.Code, len(c.HeadlessFailoverStatusCodes))
	for index, statusCode := range c.HeadlessFailoverStatusCodes {
		statusCodes[index] = codes.Code(statusCode)
	}
	return statusCodes
}

// EffectiveAttributeIndexSyncTimeout returns the configured timeout or 2m.
func (c Interpreter) EffectiveAttributeIndexSyncTimeout() time.Duration {
	if c.AttributeIndexSyncTimeout <= 0 {
		return DefaultAttributeIndexSyncTimeout
	}
	return c.AttributeIndexSyncTimeout
}
