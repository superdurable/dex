package config

// TasklistConfig controls tasklist ownership and partitioning for the matching service.
// Tasklists are dynamic (created on first request), unlike shards which are pre-defined.
// Uses Cadence-style fencing (no lease renewal) — ownership is claimed by
// incrementing range_id, and stale owners are detected on fenced writes.
type TasklistConfig struct {
	// Cluster holds membership/gossip settings for matching nodes. The
	// deployment is always a cluster; a single node is a 1-member cluster.
	// Reuses the same ClusterConfig type as ShardConfig.
	Cluster ClusterConfig `yaml:"cluster"`

	// NumWritePartitions is the default number of write partitions per tasklist.
	// Each partition is independently owned and load-balanced across matching nodes.
	// Default: 4
	NumWritePartitions int `yaml:"numWritePartitions"`

	// NumReadPartitions is the default number of read partitions per tasklist.
	// Workers' PollForRun requests are round-robin'd across read partitions.
	// Default: 4
	NumReadPartitions int `yaml:"numReadPartitions"`

	// PerTasklistOverrides allows configuring partition counts per namespace + tasklist.
	// Outer key: namespace. Inner key: tasklist name. Overrides NumWritePartitions and NumReadPartitions.
	PerTasklistOverrides map[string]map[string]TasklistPartitionOverride `yaml:"perTasklistOverrides"`
}

// TasklistPartitionOverride allows per-tasklist partition count configuration.
type TasklistPartitionOverride struct {
	NumWritePartitions int `yaml:"numWritePartitions"`
	NumReadPartitions  int `yaml:"numReadPartitions"`
}

func DefaultTasklistConfig() TasklistConfig {
	return TasklistConfig{
		Cluster:            DefaultClusterConfig(),
		NumWritePartitions: 4,
		NumReadPartitions:  4,
	}
}

// Validate checks tasklist configuration constraints.
func (c TasklistConfig) Validate() error {
	return c.Cluster.Validate()
}
