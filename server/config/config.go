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
	"bytes"
	"encoding/json"
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
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
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
	// DefaultAttributeStoreSyncTotalDurationSeconds caps regular Activity retries at one hour.
	DefaultAttributeStoreSyncTotalDurationSeconds int32 = 3600
	// DefaultAttributeIndexSyncTimeout bounds backend index registration and propagation checks.
	DefaultAttributeIndexSyncTimeout = 2 * time.Minute
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

type (
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
		// SyncRetryPolicy controls regular Activity retries. Nil or zero fields default to 1s/30s/2x/unlimited attempts/1h total.
		SyncRetryPolicy *dexpb.RetryPolicy `yaml:"syncRetryPolicy"`
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
		// IncludeCadenceRPCInputOutputIntoHistory stores RPC input/output in Cadence signal history for debugging. Default false. Temporal Updates always store both.
		IncludeCadenceRPCInputOutputIntoHistory bool `yaml:"includeCadenceRPCInputOutputIntoHistory"`
		// QueryWorkflowFailedRetryPolicy retries failed Describe/Query calls against the backend.
		QueryWorkflowFailedRetryPolicy QueryWorkflowFailedRetryPolicy `yaml:"queryWorkflowFailedRetryPolicy"`
		// InvokeRPCContinuedAsNewErrorRetryPolicy retries InvokeRPC after ContinueAsNew. Default interval is 1 second and maximum attempts is 5.
		InvokeRPCContinuedAsNewErrorRetryPolicy InvokeRPCContinuedAsNewErrorRetryPolicy `yaml:"invokeRPCContinuedAsNewErrorRetryPolicy"`
	}

	QueryWorkflowFailedRetryPolicy struct {
		// InitialIntervalSeconds is the first backoff between query retries. Default 1.
		InitialIntervalSeconds int `yaml:"initialIntervalSeconds"`
		// MaximumAttempts is the max attempts including the first. Default 5.
		MaximumAttempts int `yaml:"maximumAttempts"`
	}

	InvokeRPCContinuedAsNewErrorRetryPolicy struct {
		// InitialIntervalSeconds is the delay between attempts. Non-positive values default to 1.
		InitialIntervalSeconds int `yaml:"initialIntervalSeconds"`
		// MaximumAttempts includes the initial attempt. Non-positive values default to 5.
		MaximumAttempts int `yaml:"maximumAttempts"`
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
		// RetryPolicy is the activity retry policy. Nil uses the registration default.
		RetryPolicy *dexpb.RetryPolicy `yaml:"retryPolicy"`
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

	return cfg, nil
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

// EffectiveInvokeRPCContinuedAsNewErrorRetryPolicy returns the configured policy with defaults.
func (c ApiConfig) EffectiveInvokeRPCContinuedAsNewErrorRetryPolicy() InvokeRPCContinuedAsNewErrorRetryPolicy {
	policy := c.InvokeRPCContinuedAsNewErrorRetryPolicy
	if policy.InitialIntervalSeconds <= 0 {
		policy.InitialIntervalSeconds = 1
	}
	if policy.MaximumAttempts <= 0 {
		policy.MaximumAttempts = 5
	}
	return policy
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

// UnmarshalYAML decodes camelCase RetryPolicy fields into the shared protobuf type.
func (c *AttributeStoreConfig) UnmarshalYAML(node *yaml.Node) error {
	type plainAttributeStoreConfig AttributeStoreConfig
	filteredNode := *node
	filteredNode.Content = make([]*yaml.Node, 0, len(node.Content))
	var retryPolicyNode *yaml.Node
	for index := 0; index < len(node.Content); index += 2 {
		keyNode := node.Content[index]
		valueNode := node.Content[index+1]
		if keyNode.Value == "syncRetryPolicy" {
			retryPolicyNode = valueNode
			continue
		}
		filteredNode.Content = append(filteredNode.Content, keyNode, valueNode)
	}

	filteredYAML, err := yaml.Marshal(&filteredNode)
	if err != nil {
		return fmt.Errorf("encode attribute store config: %w", err)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(filteredYAML))
	decoder.KnownFields(true)
	if err := decoder.Decode((*plainAttributeStoreConfig)(c)); err != nil {
		return err
	}
	if retryPolicyNode == nil || retryPolicyNode.Tag == "!!null" {
		return nil
	}

	policy := &dexpb.RetryPolicy{}
	if err := decodeProtoYAML(retryPolicyNode, policy); err != nil {
		return fmt.Errorf("decode attribute store syncRetryPolicy: %w", err)
	}
	c.SyncRetryPolicy = policy
	return nil
}

func decodeProtoYAML(node *yaml.Node, message proto.Message) error {
	var value any
	if err := node.Decode(&value); err != nil {
		return err
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return protojson.Unmarshal(encoded, message)
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
func (c AttributeStoreConfig) EffectiveSyncRetryPolicy() *dexpb.RetryPolicy {
	policy := &dexpb.RetryPolicy{
		InitialIntervalSeconds: c.SyncRetryPolicy.GetInitialIntervalSeconds(),
		MaximumIntervalSeconds: c.SyncRetryPolicy.GetMaximumIntervalSeconds(),
		BackoffCoefficient:     c.SyncRetryPolicy.GetBackoffCoefficient(),
		MaximumAttempts:        c.SyncRetryPolicy.GetMaximumAttempts(),
		TotalDurationSeconds:   c.SyncRetryPolicy.GetTotalDurationSeconds(),
	}
	if policy.InitialIntervalSeconds == 0 {
		policy.InitialIntervalSeconds = 1
	}
	if policy.MaximumIntervalSeconds == 0 {
		policy.MaximumIntervalSeconds = 30
	}
	if policy.BackoffCoefficient == 0 {
		policy.BackoffCoefficient = 2
	}
	if policy.TotalDurationSeconds == 0 {
		policy.TotalDurationSeconds = DefaultAttributeStoreSyncTotalDurationSeconds
	}
	return policy
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
	if policy.GetInitialIntervalSeconds() <= 0 || policy.GetMaximumIntervalSeconds() <= 0 ||
		policy.GetBackoffCoefficient() <= 0 || policy.GetMaximumAttempts() < 0 ||
		policy.GetTotalDurationSeconds() <= 0 {
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

// QueryWorkflowFailedRetryPolicyWithDefaults fills zero fields with defaults (1s / 5 attempts).
func QueryWorkflowFailedRetryPolicyWithDefaults(retryPolicy *QueryWorkflowFailedRetryPolicy) QueryWorkflowFailedRetryPolicy {
	var rp QueryWorkflowFailedRetryPolicy

	if retryPolicy != nil && retryPolicy.InitialIntervalSeconds != 0 {
		rp.InitialIntervalSeconds = retryPolicy.InitialIntervalSeconds
	} else {
		rp.InitialIntervalSeconds = 1
	}

	if retryPolicy != nil && retryPolicy.MaximumAttempts != 0 {
		rp.MaximumAttempts = retryPolicy.MaximumAttempts
	} else {
		rp.MaximumAttempts = 5
	}

	return rp
}
