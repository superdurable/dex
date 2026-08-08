<!---
---
title: WorkflowConfig
---
--->

<!---## DOCHUB-PATH: advanced-concepts/workflow-config.mdx :DOCHUB-PATH ##--->

# WorkflowConfig

The `WorkflowConfig` struct allows for customization of workflow execution behavior within the Dex. It provides options to control various aspects of workflow management, including setting executing state ID to workflow search attributes, continue-as-new behavior, and execution optimizations.

## Properties

### **`DisableSystemSearchAttribute`** (*bool*)
Decides if system search attributes are used. Defaults to `false`.

### **`ExecutingStateIdMode`** (*ExecutingStateIdMode*)
Determines which of the executing state IDs will be saved to workflow search attributes:
* `ENABLED_FOR_ALL`: every executing state ID is added to workflow search attributes
* `ENABLED_FOR_STATES_WITH_WAIT_UNTIL`: only IDs of the executing states that do not execute right away (skip `wait until` API) are added to workflow search attributes
* `DISABLED`: no state IDs are added to workflow search attributes

### **`ContinueAsNewThreshold`** (*int*)
Sets a threshold for the number of workflow operations (signals received + executed state APIs + sync update received) after which the workflow will be continued as new. This helps manage the size of workflow history and prevent unbounded growth. `0` means unlimited.

### **`ContinueAsNewPageSizeInBytes`** (*int*)
Specifies the maximum size (in bytes) of the data dump where continuing workflow is new. `0` sets it to default 1 MB.

### **`OptimizeActivity`** (*bool*)
Enables optimizations for activity executions within the workflow. When `true`, Temporal's [Local Activity](https://docs.temporal.io/local-activity) will be used instead of regular activity. It minimizes the storage cost as the payload will not be shown in activity input. Defaults to `false`.

### **`OptimizeTimer`** (*bool*)
Enables optimizations for timer executions within the workflow. Minimizes costs by limiting number of timers started.

Without optimization, a new timer is always created when receiving a REST timer request.

**Example**: If the first timer is 7 days, and after one day, the client requests to create another 7-day timer, then after another day, it requests another 7-day timer again, there will be 3 timers created and active.

The optimization allows for the lazy creation of timers ensuring only one timer is active at the time.

**Example**: In the same scenario as above, after creating the first timer, the requests to create the other two will be kept in memory and only created for the leftover time (1 day) after the first timer fired.

Visual explanation:
![Screenshot 2025-04-17 at 3 09 44 PM](https://github.com/user-attachments/assets/841eb505-715a-4520-8507-f440f36be248)

Defaults to `false`.