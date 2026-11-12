---
section: Added
---
- The running server reports the effective values of **retention.closedFlowDuration**, **values.maxValueBytes**, **limits.maxFlowStateBytes**, and **lifecycle.attemptDrainTimeout**, including defaults for unset keys. **dev** and **local** also report **dev.durabilityMode**. The values appear in the **dexzd_config_closed_flow_retention_seconds**, **dexzd_config_max_value_bytes**, **dexzd_config_max_flow_state_bytes**, **dexzd_config_attempt_drain_timeout_seconds**, and **dexzd_config_durability_mode_info** metrics, and in one **effective configuration** INFO line at startup. [Reference](/reference/server/configuration#effective-configuration).
