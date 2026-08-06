# Dex Web Phase 1 设计

状态：Implementation in progress
日期：2026-08-05

## 1. Phase 1 范围

Phase 1 确定：

- Web 所需的 Dex server API proto；
- Temporal/Cadence history 到 Dex 语义事件的转换边界；
- SYNC/ASYNC step method 的统一展示语义；
- Run 实时状态的 query 数据；
- 搜索页和 Run 详情页的信息架构。

实现顺序：

1. 定稿 public history event 和 internal async snapshot proto。
2. 实现 Temporal/Cadence history converter、local snapshot persistence 和 server tests。
3. 将 Timeline、Step Graph、Reset 和 Selected Event 迁移到统一 `input/output/context`。
4. 完成 Web blob hydration tests、E2E、文档和发布验证。

## 2. 核心原则

### 2.1 只暴露 Dex 语义

Web API 不暴露以下 Temporal/Cadence internal details：

- backend event type；
- raw event JSON；
- workflow task；
- task queue configuration；
- activity 或 pending activity；
- parent/root engine execution；
- memo map；
- state transition count；
- event correlation ID。

Server 将 engine 数据转换成 Dex 概念，例如：

- memo 中的 request ID → `request_id`；
- `FlowType` search attribute → `flow_type`；
- Activity/LocalActivity history → Step WaitFor/Execute completed or failed event。

### 2.2 History event 不是一对一转换

一个 Dex history event 可以由多个 engine events 聚合而成；与 Dex 无关的 engine event 可以产生零个 Dex event。

例如一个 SYNC Execute：

```text
ActivityTaskScheduled
  + ActivityTaskStarted
  + ActivityTaskFailed(retry)
  + ActivityTaskStarted
  + ActivityTaskCompleted
  -> StepExecuteCompletedEvent
```

ASYNC local failure 后的 regular Activity fallback 也归并为同一个逻辑 step method execution。

### 2.3 History 与 Current State 分工

- `GetFlowSummary` 返回稳定的 flow execution metadata。
- `GetHistoryEvents` 返回已经持久化的 Dex 语义事件。
- `GetFlowState` 返回 flow 的 interpreter 快照。
- Closed flow 不需要 `GetFlowState`；最终状态由 history 重建。

## 3. FlowService API

```proto
import "google/protobuf/duration.proto";
import "google/protobuf/timestamp.proto";

service FlowService {
  // Existing RPCs omitted.
  rpc GetFlowSummary(GetFlowSummaryRequest) returns (GetFlowSummaryResponse);
  rpc GetHistoryEvents(GetHistoryEventsRequest) returns (GetHistoryEventsResponse);
  rpc WaitForHistoryEvent(WaitForHistoryEventRequest)
      returns (WaitForHistoryEventResponse);
  rpc GetFlowState(GetFlowStateRequest)
      returns (GetFlowStateResponse);
}
```

时间使用 `google.protobuf.Timestamp` 和 `google.protobuf.Duration`，不新增 Unix 秒/毫秒/纳秒字段。

## 4. GetFlowSummary

### 4.1 Proto

```proto
message FlowExecutionID {
  string flow_id = 1;
  string run_id = 2;
}

message GetFlowSummaryRequest {
  string flow_id = 1;
  // Empty resolves the current/latest execution.
  string run_id = 2;
}

message GetFlowSummaryResponse {
  FlowExecutionID flow_execution_id = 1;
  string first_run_id = 2;
  string request_id = 3;
  string flow_type = 4;
  FlowStatus flow_status = 5;
  google.protobuf.Timestamp start_time = 6;
  google.protobuf.Timestamp close_time = 7;
}
```

`request_id` 从 `__DexSystem_WorkflowRequestId` memo 转换得到。`flow_type` 从 Dex `FlowType` search attribute 转换得到。原始 memo 不返回。

`run_id` 为空时，server 让 Temporal/Cadence 解析 current/latest run，并在 response 中返回实际 run ID。Web 随后替换为包含 run ID 的 canonical URL。

## 5. SearchFlows

当前 proto 已经包含：

```proto
message SearchFlowsResponseEntry {
  string flow_id = 1;
  string run_id = 2;
  repeated KV search_attributes = 3;
}
```

它不复用 `GetFlowSummaryResponse`。列表与 summary 的数据来源、成本和可用字段不同。

只增加搜索页直接需要的公共字段：

```proto
message SearchFlowsResponseEntry {
  string flow_id = 1;
  string run_id = 2;
  repeated KV search_attributes = 3;
  string flow_type = 4;
  FlowStatus flow_status = 5;
  google.protobuf.Timestamp start_time = 6;
  google.protobuf.Timestamp close_time = 7;
}
```

不增加 task queue、memo、pending activity、state transition count 或 backend configuration。

`flow_type` 虽然也存在于 search attributes，仍作为稳定的 Dex system field 返回。Custom search attributes 继续保留在 `search_attributes`。

## 6. GetHistoryEvents

### 6.1 Pagination

```proto
message GetHistoryEventsRequest {
  string flow_id = 1;
  string run_id = 2;
  // Inclusive raw-history cursor; zero starts from the beginning.
  int64 start_internal_event_id = 3;
  // Requested Temporal/Cadence raw-history page size.
  int32 estimate_page_size = 4;
  bytes next_page_token = 5;
}

message GetHistoryEventsResponse {
  repeated FlowHistoryEvent events = 1;
  bytes next_page_token = 2;
  // First raw-history event not consumed by this response.
  int64 next_internal_event_id = 3;
}
```

`estimate_page_size` 不是 response size 保证：

- 它直接传给 Temporal/Cadence 作为 raw-history page size；
- 多个 raw events 可能聚合成一个 Dex event；
- 跨 raw page 的 step method operation 仍聚合成一个 Dex event；
- 与 Dex 无关的 raw events 不产生 Dex event；
- 因此 response 数量可能小于或略大于 estimate。

`next_page_token` 直接透传 Temporal/Cadence 返回的 native token。调用方同时把
`next_internal_event_id` 作为下一次请求的 `start_internal_event_id`。

### 6.2 Dex semantic events

```proto
message FlowHistoryEvent {
  // Terminal or anchor raw-history event ID.
  int64 event_id = 1;
  google.protobuf.Timestamp event_time = 2;

  oneof payload {
    FlowStartedOrContinuedHistoryEvent flow_started_or_continued = 20;
    FlowClosedHistoryEvent flow_closed = 21;
    StepWaitForCompletedEvent step_wait_for_completed = 22;
    StepWaitForFailedEvent step_wait_for_failed = 23;
    StepExecuteCompletedEvent step_execute_completed = 24;
    StepExecuteFailedEvent step_execute_failed = 25;
    RpcExecutionCompletedEvent rpc_execution_completed = 26;
    ChannelExternalPublishEvent channel_external_publish = 27;
  }
}
```

不返回 unknown engine event。遇到新 engine event type 时忽略；已识别的 Dex payload 无法解码时返回 internal error。

### 6.3 Flow events

```proto
message FlowStartedOrContinuedHistoryEvent {
  FlowExecutionID flow_execution_id = 1;
  string flow_type = 2;
  FlowConfig flow_config = 3;

  oneof start_or_continue {
    FlowInitialStart initial_start = 10;
    FlowContinuedStart continued_start = 11;
  }
}

message FlowInitialStart {
  string start_step_type = 1;
  Value step_input = 2;
  StepOptions step_options = 3;
  repeated KV initial_attributes = 4;
}

message FlowContinuedStart {
  string previous_run_id = 1;
  repeated StepMovement steps_to_start = 2;
  repeated StepExecutionResumeInfo steps_to_resume = 3;
  map<string, ChannelValues> pending_channel_messages = 4;
  repeated KV attributes = 5;
  repeated StepCompletionOutput completed_steps = 6;
}

message FlowClosedHistoryEvent {
  FlowStatus flow_status = 1;
  repeated StepCompletionOutput results = 2;
  FlowErrorType error_type = 3;
  string error_message = 4;
  string continued_to_run_id = 5;
}
```

Initial start 返回用户提交的起始 step、input、options 和 attributes。Continued start 返回从上一 run 带入的：

- 待启动的 step movements；
- 待恢复的 step executions；
- pending channel messages；
- attributes 和 completed step outputs。

Continue-As-New 仍保持 run 边界。Web 不把不同 run 合并到同一个 event ID 空间。

### 6.4 Step method events

```proto
message StepMethodFailure {
  string message = 1;
  string error_type = 2;
  string stack_trace = 3;
  string retry_state = 4;
  ErrorResponse details = 5;
  int32 attempt = 6;
}

message StepMethodOptions {
  int32 timeout_seconds = 1;
  RetryPolicy retry_policy = 2;
}

message StepMethodEventInput {
  bool unavailable = 1;
  Value step_input = 2;
  ConditionResults condition_results = 3;
  repeated KV attributes = 4;
  repeated KV step_execution_locals = 5;
}

message StepMethodEventContext {
  string step_execution_id = 1;
  string from_step_execution_id = 2;
  string step_type = 3;
  StepDurability durability = 4;
  int32 final_attempt = 5;
  google.protobuf.Timestamp started_time = 6;
  google.protobuf.Duration duration = 7;
  StepMethodOptions method_options = 8;
  optional bool is_transient_step = 9;
  StepMethodFailure last_failure_info = 10;
}

message StepWaitForCompletedOutput {
  WaitingCondition wait_for_condition = 1;
  repeated AttributeWrite upsert_attributes = 2;
  repeated ChannelMessage publish_to_channel = 3;
  repeated KV record_events = 4;
  repeated KV upsert_step_execution_locals = 5;
  StepMovement transient_step_movement = 6;
}

message StepExecuteCompletedOutput {
  StepDecision step_decision = 1;
  repeated AttributeWrite upsert_attributes = 2;
  repeated ChannelMessage publish_to_channel = 3;
  repeated KV record_events = 4;
  repeated KV upsert_step_execution_locals = 5;
}

message StepMethodFailedOutput {
  StepMethodFailure failure = 1;
}

message StepWaitForCompletedEvent {
  StepMethodEventInput input = 1;
  StepWaitForCompletedOutput output = 2;
  StepMethodEventContext context = 3;
}

message StepWaitForFailedEvent {
  StepMethodEventInput input = 1;
  StepMethodFailedOutput output = 2;
  StepMethodEventContext context = 3;
}

message StepExecuteCompletedEvent {
  StepMethodEventInput input = 1;
  StepExecuteCompletedOutput output = 2;
  StepMethodEventContext context = 3;
}

message StepExecuteFailedEvent {
  StepMethodEventInput input = 1;
  StepMethodFailedOutput output = 2;
  StepMethodEventContext context = 3;
}
```

四种 step method event 都使用同样的三层结构：

- `input`：worker 调用时的 `step_input`、完整 attributes snapshot、Execute 的
  `condition_results`，以及 step execution locals；
- `output`：WaitFor 的 waiting condition 或 Execute 的 step decision，加上该次调用的
  attributes、channel、record event 和 step-local side effects；失败 event 只返回最终
  terminal failure；
- `context`：step execution identity、lineage、durability、final attempt、started time、
  duration、method options，以及 SYNC retry 的最近一次 failure。

不返回 previous attempt failures。SYNC Activity retry 只返回紧邻最终 attempt 的
`last_failure_info`；terminal failure 放在 `output.failure`。两者的 `attempt` 分别标识其
对应 attempt。ASYNC local failure 只用于触发 regular Activity fallback，不展示为用户事件。

Web 只消费统一的 `input/output/context`，不根据 durability 选择额外 API。Server 在
`GetHistoryEvents` 内部按实际执行路径补齐数据：

| 执行路径 | Input 来源 | Context/options 来源 |
|---|---|---|
| SYNC regular Activity | ActivityTaskScheduled input | scheduled event metadata 和 input context |
| ASYNC local success | run-scoped async input snapshot | LocalActivity marker 与 async input snapshot |
| ASYNC local failure + regular fallback | fallback ActivityTaskScheduled input | scheduled event metadata；durability 仍为 ASYNC |

local snapshot 不存在、external storage 未启用或数据已清理时，server 返回
`input.unavailable=true`。这只代表 step method input snapshot 不可恢复，不代表其中某个
独立 Value blob 加载失败。

`from_step_execution_id` 只接受 server 写入的值：

- `__start__`；
- `__rpc/<rpcName>`；
- 另一个 step execution ID。

### 6.5 SYNC/ASYNC 聚合

| 模式 | Raw history | Dex event |
|---|---|---|
| SYNC success | Activity scheduled/started/retries/completed | 一个 completed event |
| SYNC terminal failure | Activity scheduled/started/retries/failed | 一个 failed event |
| ASYNC success | LocalActivity marker result | 一个 completed event |
| ASYNC local failure + fallback success | failure marker + regular Activity lifecycle | 一个 completed event；不展示 local failure |
| ASYNC local failure + fallback failure | failure marker + regular Activity failure | 一个 failed event；只展示 terminal failure |

`durability` 表示请求的 Dex durability。ASYNC fallback 最终由 regular Activity 完成时仍返回 `STEP_DURABILITY_ASYNC`。

Transient step：

- 有独立 `step_execution_id`；
- lineage 指向产生它的 WaitFor step；
- 只有 Execute event；
- `is_transient_step=true`；
- Execute 的 DeadEnd 只结束 transient branch，不表示 Flow Closed。

### 6.6 RPC 和 channel

```proto
message RpcExecutionCompletedEvent {
  string rpc_name = 1;
  Value input = 2;
  Value output = 3;
  StepDecision step_decision = 4;
  repeated AttributeWrite upsert_attributes = 5;
  repeated KV record_events = 6;
  repeated ChannelMessage publish_to_channel = 7;
}

message ChannelExternalPublishEvent {
  repeated ChannelMessage messages = 1;
}
```

RPC 启动的 step 从 `StepMovement.from_step_execution_id_internal_only` 中的 `__rpc/<rpcName>` 建图。

### 6.7 Step event input 与 blob hydration

两个 internal message 的职责严格分开：

```proto
message InternalLocalActivityInput {
  int64 current_run_started_timestamp = 1;
  StepMethodOptions method_options = 2;
}

message InternalAsyncStepInputSnapshot {
  StepMethodOptions method_options = 1;

  oneof request {
    InvokeWaitForMethodRequest wait_for_request = 2;
    InvokeExecuteMethodRequest execute_request = 3;
  }
}
```

- `InternalLocalActivityInput` 是 workflow provider 只给 local activity 的第二个参数，
  用于携带当前 run start time 和无法从 local marker 恢复的 method options；
- `InternalAsyncStepInputSnapshot` 是成功 local activity 写入 external storage 的 protobuf，
  保存准确发送给 worker 的 request 和 method options；
- `InvokeWaitForMethodActivityInput` 和 `InvokeExecuteMethodActivityInput` 保持不变，避免
  增大 regular Activity history；
- SYNC regular activity 同一个第二参数位置传 `nil`，可以产生极小 null payload；
- ASYNC local activity 传 `InternalLocalActivityInput`，fallback regular activity 传 `nil`。

workflow provider 使用同一个 activity function，不增加 wrapper activity：

```go
ExecuteActivity(
    valuePtr interface{},
    durability dexpb.StepDurability,
    ctx UnifiedContext,
    activity interface{},
    regularInput interface{},
    localActivityOnlyInput interface{},
) error
```

local activity 成功后保存准确的 WaitFor/Execute worker request。缺失 run start time 时，
activity 使用当前 flow ID 和准确 run ID 执行 Describe。
文件位于：

```text
<namespace>/<run-start-date>$<encoded-flow-id>$<encoded-run-id>/
  <encoded-step-execution-id>/<method>.pb
```

写入失败会让 local activity 失败并进入 regular Activity fallback。`GetHistoryEvents`
把 snapshot 转换成公共 `StepMethodEventInput` 并合并到 semantic event。regular
Activity 直接由 scheduled event 转成同一公共结构。Web 不根据 event 顺序重建
attributes，也不推测 timer/channel results。

公共 event 内仍可能包含 blob-backed `Value`。这些 Value 继续由通用 `LoadBlobs`
路径按需加载，不与 async input snapshot 的 availability 混为一谈。

Web Go bridge 提供 `POST /api/blobs/load`，统一 string/object blob reference 的 JSON
shape。前端递归收集所选 event 或 live state 中的 references，按 `kind + blob ID`
去重并批量加载；缓存跨 Overview、Step Graph 和 Timeline 共用。失败时保留页面：

- `input.unavailable=true` 显示 “Step event input unavailable”；
- 单个 blob-backed Value 无法加载显示 “Value blob unavailable”；
- Raw JSON 不泄露 blob ID、store ID 或 object path。

## 7. WaitForHistoryEvent

```proto
message WaitForHistoryEventRequest {
  string flow_id = 1;
  string run_id = 2;
  // First raw-history event not consumed by the caller.
  int64 next_internal_event_id = 3;
}

message WaitForHistoryEventResponse {
  bool event_available = 1;
  int64 available_internal_event_id = 2;
  FlowStatus flow_status = 3;
}
```

行为：

- `next_internal_event_id` 已存在时立即返回。
- Running run 使用 backend long poll。
- Terminal run 没有新 event 时返回 `event_available=false`。
- Continue-As-New 终止当前 run 的 wait，不自动进入新 run。
- Timeout 返回 `DeadlineExceeded + LONG_POLL_TIME_OUT`。

Wait 只表示 raw history 有变化。调用方收到通知后再调用 `GetHistoryEvents` 完成语义聚合。

## 8. GetFlowState

### 8.1 Proto

```proto
enum ActiveStepPhase {
  ACTIVE_STEP_PHASE_UNSPECIFIED = 0;
  ACTIVE_STEP_PHASE_ACTIVE = 1;
  ACTIVE_STEP_PHASE_WAITING = 2;
}

message ActiveStepExecutionState {
  string step_execution_id = 1;
  string from_step_execution_id = 2;
  string step_type = 3;
  ActiveStepPhase phase = 4;
  StepMovement movement = 5;
  WaitingCondition waiting_condition = 6;
  StepExecutionCompletedConditions completed_conditions = 7;
  repeated KV step_execution_locals = 8;
  repeated TimerInfo timers = 9;
}

message GetFlowStateRequest {
  string flow_id = 1;
  string run_id = 2;
}

message GetFlowStateResponse {
  FlowConfig flow_config = 1;
  repeated KV attributes = 2;
  repeated ActiveStepExecutionState active_step_executions = 3;
  repeated StepMovement queued_steps = 4;
  map<string, ChannelValues> pending_channel_messages = 5;
  repeated StepCompletionOutput completed_steps = 6;
}
```

### 8.2 DebugDump query 改造

现有 `DebugDumpResponse` 只有 config、snapshot 和 firing timers。增加：

```proto
message DebugDumpResponse {
  FlowConfig config = 1;
  ContinueAsNewDump snapshot = 2;
  repeated int64 firing_timers_unix_timestamps = 3;
  repeated ActiveStepExecutionState active_step_executions = 4;
}
```

`DebugDumpQueryHandler` 组合 `StepExecutionCounter` 和 `ContinueAsNewer`：

1. 从 `stepActiveExecutionNums` 枚举所有 active execution IDs。
2. 使用 `stepTypeActiveCounts` 校验各 step type 的 active 数量。
3. execution ID 在 live `StepExecutionToResumeMap` 中时标记为 `WAITING`。
4. 其他 active execution 标记为 `ACTIVE`。
5. waiting entry 提供 movement、lineage、waiting condition、completed conditions 和 locals。
6. timer processor 按 step execution ID 补充 timer 状态。
7. 结果按 step type 和 execution number 确定性排序。

不能使用 `GetSnapshot().step_executions_to_resume` 判断 WAITING，因为 snapshot 还合并了尚未启动的 resume requests。

当前 `StepExecutionToResumeMap` 在 Execute 返回后才删除。为了让 map membership 精确表示 WAITING，需要把删除时点移到 Execute 开始前：

```text
WaitFor/Transient running       -> ACTIVE，map 中不存在
Waiting on conditions          -> WAITING，map 中存在
Execute running                -> ACTIVE，Execute 前从 map 删除
Completed/Failed               -> 不在 active counter 中
```

该时点调整必须验证 Continue-As-New drain：Execute in-flight 时 CAN 继续等待 step thread 完成，不把它错误保存为 waiting resume。

## 9. 页面草图

### 9.1 Flow Search

```text
┌──────────────────────────────────────────────────────────────────┐
│ Dex | Flows | profile | backend | namespace/domain | timezone ⚙ │
├──────────────────────────────────────────────────────────────────┤
│ Query mode: [Basic] [Advanced]                                   │
│ Basic: Status | Flow ID | Run ID | Flow Type | Time | + Filter   │
│        Generated query preview .................... [Search][Clear]│
│ Advanced: Visibility query editor ................. [Search][Clear]│
│ Recent searches | Named queries | Save query                      │
├──────────────────────────────────────────────────────────────────┤
│ Status | Flow ID | Run ID | Flow Type | Start | Close | Duration │
│ Custom search attribute columns                                  │
├──────────────────────────────────────────────────────────────────┤
│ Columns ⚙          Page size [50]  [First][Previous][Next]       │
└──────────────────────────────────────────────────────────────────┘
```

Sections：

- 参考 [temporalio/ui](https://github.com/temporalio/ui) 提供 Basic 和 Advanced 两种 query mode；
- Basic mode 通过点击 status、flow type、时间和 search attribute filters 自动生成 visibility query；
- Advanced mode 允许直接编辑完整 visibility query；
- 两种 mode 使用同一 query 状态；切换 mode、刷新和分享 URL 不改变查询语义；
- recent/named searches；
- custom columns、顺序和显示偏好；
- status、flow/run ID、flow type、start/close/duration；
- custom search attributes；
- token pagination、timezone、shareable URL。

### 9.2 Run Details

```text
┌──────────────────────────────────────────────────────────────────┐
│ Flows / flow-id / run-id                         [Refresh][Copy] │
│ Status | Start/Close/Duration | History length/size | Run chain │
├──────────────────────────────────────────────────────────────────┤
│ [Overview] [Step Graph] [Timeline]                               │
├────────────────────────────────────────┬─────────────────────────┤
│ Current tab                            │ Selected event          │
│                                        │ Input                   │
│                                        │ Output                  │
│                                        │ Context / Raw JSON      │
└────────────────────────────────────────┴─────────────────────────┘
```

Overview：

- execution summary、first/current/next run；
- start input、step options、FlowConfig；
- request ID、attributes；
- current active/waiting steps、channels 和 timers。

Step Graph：

- Start、RPC source、step executions、Flow End/Failed；
- WaitFor/Execute sections；
- active、waiting、retrying、completed、failed；
- fan-out、join、failed-and-proceed；
- CloseDecision 的 conditional/graceful/force/fail/dead-end 语义。

Timeline：

- 只展示 Dex semantic events；
- 默认倒序，最新 event 位于顶部；
- 同一个 step execution 的 WaitForCondition started 和 Execute 用独立 lane 连线；
- completed/failed method event 展开统一的 Input、Output、Context；
- long poll 增量更新；
- 大 history 使用 semantic pagination 和虚拟列表。

Selected event：

- `Input`：step input、Execute condition results、attributes、step locals；
- `Output`：WaitFor condition 或 Execute decision，以及 side effects；失败时显示 terminal failure；
- `Context`：execution ID、from、durability、final attempt、started、duration、step options，
  以及可选的 SYNC last failure；
- UI 不显示 previous attempts 或 transient-step 字段；
- SYNC 和 ASYNC 使用完全相同的 renderer；
- Raw JSON tab 使用 hydrated public event，不泄露 internal snapshot 或 blob location。

## 10. Tests

Phase 2 使用 `server/integ/`：

- Temporal/Cadence summary：canonical run、request ID、flow type 和 execution timestamps。
- Search：保留 custom search attributes，并返回 flow type/status/start/close。
- Temporal/Cadence × SYNC/ASYNC：相同逻辑 flow 产生相同 Dex semantic events。
- Activity retries 聚合为一个 completed/failed event，只返回 final attempt 或 terminal failure。
- ASYNC local failure 与 regular fallback 跨 raw page 聚合。
- `estimate_page_size=1` 强制覆盖跨 native page 的不完整 operation 聚合。
- starting、RPC、fan-out、Continue-As-New lineage。
- Continue-As-New 首事件展示 carried steps、resume state、channel messages、attributes 和 completed outputs。
- transient step 只有 Execute event，DeadEnd 不产生 FlowClosed。
- Wait：立即命中、long poll、timeout、terminal 和 Continue-As-New。
- Current state：WaitFor/Execute 为 ACTIVE，condition wait 为 WAITING。
- Current state：CAN-resumed waiting step、queued resume request 和 transient Execute。
- Execute 前移除 resume entry 后，CAN drain 不丢 step 或重复执行。
- Temporal/Cadence × SYNC/ASYNC：WaitFor/Execute 显示调用时 step input、attributes 和 condition results。
- SYNC scheduled input 和 ASYNC snapshot 都映射为完全相同的 `input/output/context` shape。
- regular Activity input proto 保持不变；第二个 activity argument 为 null 时 Temporal/Cadence 都能解码。
- ASYNC local success 保存 `InternalAsyncStepInputSnapshot`；marker 中不增加完整 request。
- method options：SYNC 从 scheduled metadata 转换，ASYNC 从 `InternalLocalActivityInput` 保存并恢复。
- channel values、多个 timers、ANY/ALL results 从保存的 worker request 精确恢复。
- local failure fallback 使用 regular Activity history request，且不暴露 local failure。
- sync retry 只返回最近一次 failure；async fallback 不返回 retry failure。
- 关闭存储或清理后只对缺失的 async snapshot 返回 `input.unavailable=true`。
- local filesystem storage 覆盖 string/object blob、run-level cleanup 和安全路径。

Web Go integration：

- `/api/blobs/load` 映射 string/object arms，并按 `kind + blob ID` 去重。
- history mapping 对 SYNC/ASYNC 返回相同的 step Input、Output、Context 和 unavailable 语义。

Web Vitest：

- 递归发现和替换 step input、attributes、channels、continued state 和 condition results。
- batch cache 避免切换 tab 后重复加载。
- timeline、step graph、reset dialog 和 selected event 不再读取旧的 execution/request/response。
- Input 按 step input、condition results、attributes、locals 排列；Output 和 Context 使用新结构。
- step snapshot unavailable 与单个 Value blob unavailable 使用不同提示。
- 加载失败显示 unavailable，结构化 details 和 Raw JSON 都不泄露 blob ID。

Web E2E：

- Search filters、columns、saved queries、URL 和 pagination。
- SYNC/ASYNC 的 Step Graph 具有相同业务 topology。
- Timeline 聚合 retries/fallback，不显示 engine internal events。
- Running flow 实时 ACTIVE/WAITING 更新，terminal flow 停止 state query。

## 11. Documentation

- 本文同步记录 public history event、internal async snapshot 和 Web details 的最终结构。
- `protos/README.md` 记录 `GetHistoryEvents` 自动补齐 input 及两种 unavailable 语义。
- `web/README.md` 记录统一 event renderer、Value blob hydration 和缓存行为。
- `CONTRIBUTING.md` 仅在开发命令或生成流程变化时更新。
- 不修改 `sdk-java`。

## 12. UI/UX

Web 功能覆盖本地 `dex-base/web` 与 `durableworkflow/iwf-web` 的功能并集，但全部使用 Dex terminology。

Temporal/Cadence 缺失的数据展示为 `—`。ASYNC local activity 在 marker 持久化前无法审计，UI 显示 “active, history pending”，不伪造 started event、worker 或 failure。

Selected Event 固定按 Input、Output、Context 顺序展示。SYNC/ASYNC 不改变页面结构；
`input.unavailable` 与 Value blob unavailable 分别说明 snapshot 缺失和单个值缺失。
