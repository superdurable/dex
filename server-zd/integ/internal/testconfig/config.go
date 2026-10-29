// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package testconfig

import "time"

type RetryPolicy struct {
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

type MatchingConfig struct {
	// DispatchWait bounds a regular attempt's Worker search. Default 10s. Must be positive when set.
	DispatchWait time.Duration `yaml:"dispatchWait"`
	// StickyWait bounds searching a sticky queue while it has connected streams before fallback. Default 5s. When set, must not exceed DispatchWait.
	StickyWait time.Duration `yaml:"stickyWait"`
	// MaxConcurrentTasksCap bounds registered streams per appWorkerID on each owner node. Default 256. Must be positive when set.
	MaxConcurrentTasksCap int `yaml:"maxConcurrentTasksCap"`
	// MaxStreams bounds registered streams on this server. Default 100000. Must be positive when set.
	MaxStreams int `yaml:"maxStreams"`
	// MaxQueueLength bounds queue and fallback queue names. Default 256. Must be positive when set.
	MaxQueueLength int `yaml:"maxQueueLength"`
	// MaxFlowTypes bounds declared Flow types per Worker registration. Default 256. Must be positive when set.
	MaxFlowTypes int `yaml:"maxFlowTypes"`
	// MaxFlowTypeLength bounds each declared Flow type. Default 256. Must be positive when set.
	MaxFlowTypeLength int `yaml:"maxFlowTypeLength"`
	// PingInterval sends an application Ping on idle poll streams. Default 20s. Must be positive when set.
	PingInterval time.Duration `yaml:"pingInterval"`
}

type StreamStoreConfig struct {
	// Backend selects disabled, memory, or redis storage. Default disabled. Immutable after startup.
	Backend StreamStoreBackend `yaml:"backend"`
	// MaxMessageBytes limits each serialized Stream Value. Default 102400. Must be positive after defaults.
	MaxMessageBytes int64 `yaml:"maxMessageBytes"`
	// MaxReadMessages caps one ListStreamMessages page. Default 1000. Must be positive after defaults. Immutable after startup.
	MaxReadMessages int32 `yaml:"maxReadMessages"`
	// EstimatedMessageOverheadBytes is charged per message beyond payload and identity bytes. Default 512. Must be non-negative.
	EstimatedMessageOverheadBytes int64 `yaml:"estimatedMessageOverheadBytes"`
	// TrimTriggerPercent starts asynchronous trimming at this capacity percentage. Default 90. Valid range is 1 through 99.
	TrimTriggerPercent int32 `yaml:"trimTriggerPercent"`
	// TrimTargetPercent stops asynchronous trimming at this capacity percentage. Default 80. Must be below TrimTriggerPercent.
	TrimTargetPercent int32 `yaml:"trimTargetPercent"`
	// BackgroundTrimBatchSize caps messages removed by one trim batch. Default 256. Must be positive after defaults.
	BackgroundTrimBatchSize int `yaml:"backgroundTrimBatchSize"`
	// TrimBatchYieldTime controls the pause between atomic trim batches. Default 1ms. Must be positive after defaults.
	TrimBatchYieldTime time.Duration `yaml:"trimBatchYieldTime"`
	// TrimWorkers bounds concurrent background trim jobs per server process. Default 4. Must be positive after defaults.
	TrimWorkers int `yaml:"trimWorkers"`
}

type AttributeStoreConfig struct {
	// Stores maps FlowConfig names to immutable external destinations. Default empty disables Attribute synchronization.
	Stores map[string]AttributeStoreConfigEntry `yaml:"stores"`
	// SchemaSyncInterval refreshes SQL table schemas. Default 1m. Each interval receives uniform ±10% jitter.
	SchemaSyncInterval time.Duration `yaml:"schemaSyncInterval"`
	// SyncBatchSize caps contiguous items per Attribute Store upsert. Default 100. Must be positive after defaults.
	SyncBatchSize int `yaml:"syncBatchSize"`
	// SyncAttemptTimeout caps each regular Activity attempt. Default 30s. Must be positive after defaults.
	SyncAttemptTimeout time.Duration `yaml:"syncAttemptTimeout"`
	// SyncRetryPolicy controls regular Activity retries. Nil or zero fields default to 100ms/30s/2x/unlimited attempts/1h total.
	SyncRetryPolicy *RetryPolicy `yaml:"syncRetryPolicy"`
}

type AttributeStoreConfigEntry struct {
	// Type selects mysql, postgres, databricks, snowflake, or mongodb. Default empty is invalid for a configured entry. Immutable after startup.
	Type AttributeStoreType `yaml:"type"`
	// DSN is the driver connection string or MongoDB URI. Default empty is invalid. It may contain credentials and is never logged.
	DSN string `yaml:"dsn"`
	// TableName selects the SQL target. MySQL and PostgreSQL accept up to two parts; warehouses accept up to three. Default empty is invalid for SQL stores. Immutable after startup.
	TableName string `yaml:"tableName"`
	// FlowIDColumn stores Flow IDs in Databricks and Snowflake. Default empty is invalid for warehouse stores. Immutable after startup.
	FlowIDColumn string `yaml:"flowIdColumn"`
	// DatabaseName selects the MongoDB database. Default empty is invalid for MongoDB stores. Immutable after startup.
	DatabaseName string `yaml:"databaseName"`
	// CollectionName selects the existing MongoDB collection. Default empty is invalid for MongoDB stores. Immutable after startup.
	CollectionName string `yaml:"collectionName"`
	// RowKey splits each Flow ID into the primary-key columns of a PostgreSQL or MySQL table, such as
	// release-{project_id}-{release_id}. Each {column} placeholder captures up to the next literal text, and the
	// last placeholder captures the rest; {*} captures without a column. The table's primary key must be exactly the
	// named columns, all character columns. A Flow ID that does not match skips its batch with an error log.
	// Default empty stores the whole Flow ID in the table's single primary-key column. Immutable after startup.
	RowKey string `yaml:"rowKey"`
	// ColumnNaming maps Attribute names, and the fields of AttributeMap instance values, to PostgreSQL or MySQL
	// column names: empty (default) uses each name exactly, and snake_case uses its snake_case form. Immutable after
	// startup.
	ColumnNaming AttributeStoreColumnNaming `yaml:"columnNaming"`
	// AttributeMaps writes each instance of the named AttributeMaps as one row of its own PostgreSQL or MySQL table,
	// keyed by AttributeMap name. Writing an instance upserts its row and deleting it deletes the row, in the same
	// transaction as the batch's other writes. Default empty writes AttributeMap instances like other Attributes.
	// Immutable after startup.
	AttributeMaps map[string]AttributeStoreMapTableConfig `yaml:"attributeMaps"`
}

type AttributeStoreMapTableConfig struct {
	// TableName selects the instance table as table or schema.table. Its primary key must be exactly the store's
	// primary-key columns plus InstanceKeyColumn, all character columns. Required. Immutable after startup.
	TableName string `yaml:"tableName"`
	// InstanceKeyColumn stores the unescaped instance key, such as account-7 for the instance written as
	// Members/account-7. Required. Immutable after startup.
	InstanceKeyColumn string `yaml:"instanceKeyColumn"`
}

type AuthConfig struct {
	// Authenticators are tried in order. Default empty disables public API authentication. Immutable after startup.
	Authenticators []AuthenticatorConfig `yaml:"authenticators"`
}

type AuthenticatorConfig struct {
	// Kind selects jwt or staticTokens. Default empty is invalid for a configured authenticator. Immutable after startup.
	Kind AuthenticatorKind `yaml:"kind"`
	// Issuer is the exact JWT issuer URL. Default empty is invalid for jwt. HTTPS is required except on loopback. Immutable after startup.
	Issuer string `yaml:"issuer"`
	// Audiences lists accepted JWT audiences. Default empty is invalid for jwt; a token needs at least one match. Immutable after startup.
	Audiences []string `yaml:"audiences"`
	// JWKSURL is the key set URL. Default empty discovers it through the issuer's OIDC configuration.
	// HTTPS is required except on loopback; keys refresh at most once a minute, with a bounded unknown-key refresh.
	// The URL is immutable after startup.
	JWKSURL string `yaml:"jwksURL"`
	// CAFile adds a PEM certificate bundle to system roots for issuer HTTPS endpoints. Default empty uses system roots. Immutable after startup.
	CAFile string `yaml:"caFile"`
	// File contains YAML tokens entries with SHA-256 hashes and no plaintext tokens. Default empty is invalid for staticTokens.
	// Changed files are reloaded; malformed updates retain the last valid content, while empty or missing files revoke all tokens.
	// The file path is immutable after startup.
	File string `yaml:"file"`
}

type AttributeStoreType string
type AttributeStoreColumnNaming string
type StreamStoreBackend string
type AuthenticatorKind string

const (
	DefaultGrpcMaxMessageBytes                                     = 32 * 1024 * 1024
	AttributeStoreTypePostgres          AttributeStoreType         = "postgres"
	AttributeStoreTypeMongoDB           AttributeStoreType         = "mongodb"
	AttributeStoreColumnNamingSnakeCase AttributeStoreColumnNaming = "snake_case"
	StreamStoreBackendDisabled          StreamStoreBackend         = "disabled"
	StreamStoreBackendMemory            StreamStoreBackend         = "memory"
	AuthenticatorKindJWT                AuthenticatorKind          = "jwt"
	AuthenticatorKindStaticTokens       AuthenticatorKind          = "staticTokens"
)

func (configuration StreamStoreConfig) EffectiveBackend() StreamStoreBackend {
	if configuration.Backend == "" {
		return StreamStoreBackendDisabled
	}
	return configuration.Backend
}
