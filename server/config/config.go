// Copyright (c) 2022-2026 Super Durable, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

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
	"gopkg.in/yaml.v3"
)

const (
	StorageStatusActive   = "active"
	StorageStatusInactive = "inactive"
)

const (
	StorageTypeS3 = "s3"
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
)

type (
	Config struct {
		// Log is process logging (stdout/stderr/file, level, encoding).
		Log Logger `yaml:"log"`
		// Api is the public FlowService and internal InternalService gRPC server config.
		Api ApiConfig `yaml:"api"`
		// Interpreter selects Temporal or Cadence and worker activity settings. Exactly one of Temporal/Cadence must be set.
		Interpreter Interpreter `yaml:"interpreter"`
		// ExternalStorage offloads large Attribute payloads (string/object) above ThresholdInBytes.
		ExternalStorage ExternalStorageConfig `yaml:"externalStorage"`
	}

	ExternalStorageConfig struct {
		// Enabled turns external blob offload on or off. Default false.
		Enabled bool `yaml:"enabled"`
		// LazyLoading turns lazy loading on or off.
		// When on, server will only send blobIDs to worker for worker APIs(invoke waitFor/execute/RPC) and GetAttribute API.
		// Worker wil call LoadBlobs API to get the actual values.
		// So that worker & server can minimize the data transfer, and worker can cache the values if needed.
		// Default true when omitted (nil).
		LazyLoading *bool `yaml:"lazyLoading"`
		// ThresholdInBytes is the payload size that triggers writing a blob id onto Value instead of inline data. Default 0 (never offload when Enabled is false).
		ThresholdInBytes int `yaml:"thresholdInBytes"`
		// SupportedStorages lists blob backends. Exactly one may have Status active for writes; others are read-only.
		SupportedStorages []BlobStorageConfig `yaml:"supportedStorages"`
		// MinAgeForCleanupCheckInDays stops cleanup scans for objects newer than now minus this many days. Align with Temporal/Cadence retention.
		// Default 0 means no age gate. Need to manually set as it needs to align with Temporal/Cadence retention.
		MinAgeForCleanupCheckInDays int `yaml:"minAgeForCleanupCheckInDays"`
	}

	StorageStatus string
	StorageType   string

	BlobStorageConfig struct {
		// Status is "active" (writable) or "inactive" (read-only). Only one active store is allowed.
		Status StorageStatus
		// StorageId identifies this backend inside blob ids persisted on Value.
		StorageId string `yaml:"storageId"`
		// StorageType selects the driver; currently only "s3".
		StorageType StorageType `yaml:"storageType"`
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
		// CleanupCronSchedule is a standard cron for the blob cleanup workflow. Empty disables cleanup.
		CleanupCronSchedule string `yaml:"cleanupCronSchedule"`
	}

	ApiConfig struct {
		// Port is the TCP port for FlowService and InternalService (plaintext gRPC). Default 8801. Bind is 0.0.0.0:Port; SDKs/integ and the interpreter CAN activity dial this port.
		Port int `yaml:"port"`
		// MaxWaitSeconds caps WaitForFlow, WaitForStepCompletion, and WaitForAttribute. Zero uses DefaultMaxWaitSeconds (60). Positive values are the cap. Negatives are invalid.
		MaxWaitSeconds int64 `yaml:"maxWaitSeconds"`
		// GrpcMaxMessageBytes is MaxRecv/MaxSend for FlowService, InternalService, and WorkerService clients. Default 16 MiB. Must be positive and larger than continue-as-new page size plus overhead.
		GrpcMaxMessageBytes int `yaml:"grpcMaxMessageBytes"`
		// IncludeRPCInputOutputIntoHistory stores RPC input/output in Temporal/Cadence history for debugging. Default false.
		IncludeRPCInputOutputIntoHistory bool `yaml:"includeRPCInputOutputIntoHistory"`
		// QueryWorkflowFailedRetryPolicy retries failed Describe/Query calls against the backend.
		QueryWorkflowFailedRetryPolicy QueryWorkflowFailedRetryPolicy `yaml:"queryWorkflowFailedRetryPolicy"`
	}

	QueryWorkflowFailedRetryPolicy struct {
		// InitialIntervalSeconds is the first backoff between query retries. Default 1.
		InitialIntervalSeconds int `yaml:"initialIntervalSeconds"`
		// MaximumAttempts is the max attempts including the first. Default 5.
		MaximumAttempts int `yaml:"maximumAttempts"`
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
		// WorkerOptions are passed to the Cadence worker. Nil uses SDK defaults.
		WorkerOptions *cadenceWorker.Options
	}

	InterpreterActivityConfig struct {
		// InternalServiceTarget is the plaintext gRPC dial target for InternalService (CAN dump). Empty defaults to localhost:<Api.Port>. YAML key internalServiceTarget.
		InternalServiceTarget string `yaml:"internalServiceTarget"`
		// DumpWorkflowInternalActivityConfig tunes the CAN dump activity timeouts/retries. Nil uses activity defaults.
		DumpWorkflowInternalActivityConfig *DumpWorkflowInternalActivityConfig `yaml:"dumpWorkflowInternalActivityConfig"`
		// DefaultHeaders are forwarded as outgoing gRPC metadata on WorkerService calls. Empty means none.
		DefaultHeaders map[string]string `yaml:"defaultHeaders"`
		// WorkerConnectionIdleTimeout evicts idle, unreferenced WorkerService connections. Zero uses DefaultWorkerConnectionIdleTimeout (10m).
		WorkerConnectionIdleTimeout time.Duration `yaml:"workerConnectionIdleTimeout"`
		// MaxWorkerConnections caps the WorkerService connection pool. Zero uses DefaultMaxWorkerConnections (1000). Must be positive after defaults.
		MaxWorkerConnections int `yaml:"maxWorkerConnections"`
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

// EffectiveLazyLoading returns LazyLoading, defaulting to true when omitted.
func (c ExternalStorageConfig) EffectiveLazyLoading() bool {
	if c.LazyLoading == nil {
		return true
	}
	return *c.LazyLoading
}

// EffectiveGrpcMaxMessageBytes returns GrpcMaxMessageBytes or DefaultGrpcMaxMessageBytes.
func (c ApiConfig) EffectiveGrpcMaxMessageBytes() int {
	if c.GrpcMaxMessageBytes <= 0 {
		return DefaultGrpcMaxMessageBytes
	}
	return c.GrpcMaxMessageBytes
}

// EffectiveWorkerConnectionIdleTimeout returns the idle eviction timeout for WorkerService conns.
func (c InterpreterActivityConfig) EffectiveWorkerConnectionIdleTimeout() time.Duration {
	if c.WorkerConnectionIdleTimeout <= 0 {
		return DefaultWorkerConnectionIdleTimeout
	}
	return c.WorkerConnectionIdleTimeout
}

// EffectiveMaxWorkerConnections returns the WorkerService pool size cap.
func (c InterpreterActivityConfig) EffectiveMaxWorkerConnections() int {
	if c.MaxWorkerConnections <= 0 {
		return DefaultMaxWorkerConnections
	}
	return c.MaxWorkerConnections
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
