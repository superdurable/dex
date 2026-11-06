# Go FDG 2.0 authoring

Use this page for Summary, Display, Start input, Actions, Step groups, and
analyzer diagnostics before you search the CLI source. The same definitions
drive local `dexcli dev` and a deployed Dex Web. The application never runs
its own Web server for them. The
[Flow source analyzer reference](https://docs.superdurable.io/reference/web/flow-visualization)
has the supported source profile, the JSON contract, and the full diagnostic
list. The
[customer-refund example](https://github.com/superdurable/dex/blob/main/examples/go/products/customer-refund/deterministic/workflow.go)
is a complete Flow with every directive kind.

## File and registration boundaries

Keep the Flow, its indexed, view, and Action Attributes, its view RPCs, and its
Action input structs in one source file.

- Register every durable primitive in `GetPersistenceSchema`.
- Register every Step in `GetSteps`.
- Register both view RPCs in `GetRPCs`.
- Use Go type-derived Flow and Step identities. Do not add `GetFlowType` or
  `GetStepType` overrides, except on a generic Flow or Step type, which must
  implement them to get a stable name.
- A Connector factory Step uses its generated configuration's static
  `StepType`, `ConnectionName`, and `Annotations`. Inspect that exact released
  factory type.

## Step groups and explanations

Each ordinary Step type has exactly one group and one explanation. The
explanation is one sentence that states what the Step does.

```go
// dex:group group-id:process group-label:"Process"
// dex:explanation text:"Perform the accepted operation."
```

A group of two or more Steps does one thing, so Dex Web can fold it into one
stage. Make it one of these:

- **one run**: ordinary transitions inside the group join all its Steps;
- **one choice**: one Step outside the group leads into every part, such as
  an agent's decision Step and its tools.

The analyzer warns about any other group shape and names its parts. Dex Web
then shows those Steps in the group's band instead of folding them. The
warning never blocks.

Groups are judged on the Flow's main story. A Step that only failure
transitions reach, directly or through other such Steps, is failure handling.
Dex Web hides it until the failing Step is selected or a run went through it.
A failure transition joins no group, and a failure-handling Step's group is
not checked. Give it the group of the Step it handles.

When a Step only records breakage, such as exhausted retries or an uncertain
outcome, make it the failing Step's Execute-failure route
(`dex.ProceedToOnExecuteFailure`), not an ordinary decision. Dex Web then
shows it as failure handling. Business rejections and Connector outcome
branches stay ordinary decisions in the main story. A Step that catches its
own error and then picks the next Step makes an ordinary decision. To hide
that handling, return the error so the failure route runs.

## Indexed Attributes

Every indexed Attribute has a matching directive. The directive values must
equal the Go definition.

```go
// dex:indexed-attribute attribute-key:status index-key:keyword2 index-type:keyword value-type:string description:"Current status"
var Status = dex.DefineAttribute[string]("status",
	dex.Indexed(dex.AttributeIndex{Type: dex.IndexKeyword, IndexKey: "keyword2"}))
```

The physical index key can be a generic slot such as `keyword2`. Index slots
are few. Index an Attribute only when a list needs it.

## Summary and Display

Both view methods have the signature
`func (flow *F) GetDexSummary(ctx dex.Context, _ dex.None) (*dex.RPCResult[map[string]any], error)`,
and the same for `GetDexDisplay`. FDG 2.0 requires both, even when a Flow has
little to show. Register them with `dex.DefineRPC(flow.GetDexSummary, nil)`
and `dex.DefineRPC(flow.GetDexDisplay, nil)`.

```go
// dex:field attribute-key:status value-type:string editable:false description:"Current status" ui-slot:status
func (*ProcessFlow) GetDexDisplay(ctx dex.Context, _ dex.None) (*dex.RPCResult[map[string]any], error) {
	status, err := Status.Get(ctx)
	if err != nil {
		return nil, err
	}
	return &dex.RPCResult[map[string]any]{Output: map[string]any{"status": status}}, nil
}
```

Rules:

- Read only the declared Attributes. Views have no side effects.
- Return one map literal with exactly the declared field keys, including
  `nil` for a missing value. Never build the map through loops or helpers
  that hide its keys from the analyzer.
- `dex:field` takes `attribute-key`, `value-type`, `editable`, `description`,
  and optional `ui-slot`. There is no `field-key`, `rpc`, `dex:display`, or
  implicit label syntax.
- A field names an Attribute declared in this file, never a computed map
  entry.
- An empty view returns an empty map and has no field directives.
- Do not repeat indexed Attributes in Summary; the list already shows them.
  Display may include them.
- Never expose a hard-delete Attribute in Summary or Display, and never use
  one as an Action condition. Dex rejects hard-delete Action conditions.
- UI slots are `title`, `subtitle`, `status`, `recommendation`, and `reason`.
  Each slot except `reason` appears at most once per view. Most fields need no
  slot. A slot is not an index.
- Summary is always read-only. Editable Display fields must be supported
  scalars. JSON, objects, arrays, and AttributeMap values stay read-only.
- Keep structured business results in their owning Attribute and expose its
  real FDG type. Never duplicate business state for presentation.
- Directive lines keep source order. That order is the column, detail-field,
  Action, and form order.

## Actions and input

An operator Action is a typed RPC registration, not a comment directive:

```go
dex.DefineRPC(flow.SubmitDecision, &dex.RPCOptions{
	Action: dex.DefineAction("Submit decision",
		dex.WhenAttributeMatches(Status, dex.AttributeMatchEqual("PENDING")),
		dex.ActionRequiresPermission("process.review")),
	LockAttributes: []dex.AttributeLock{dex.LockAttribute(Status)},
})
```

- The label, permission, and condition values are compile-time constants.
- Declare exactly one permission.
- The condition uses a scalar Attribute declared in this file.
- There are no `dex:action` or `dex:when` comments.
- The handler rechecks current state under the business locks that atomic
  admission needs. The UI condition alone never authorizes or deduplicates a
  write. Follow the Flow's revision and operation identity rules.

An Action with no fields accepts `dex.None` and returns
`*dex.RPCResult[dex.None]`. Otherwise it accepts a named struct in the same
file, with one `dex:input` per exported JSON field, attached to the RPC
method:

```go
type DecisionInput struct {
	Decision string  `json:"decision"`
	Note     *string `json:"note,omitempty"`
}

// dex:input field-name:decision value-type:string source:user required:true description:"Decision"
// dex:input field-name:note value-type:string source:user required:false description:"Optional note"
func (flow *ProcessFlow) SubmitDecision(ctx dex.Context, input DecisionInput) (*dex.RPCResult[dex.None], error) {
	// Recheck the current state, then write Attributes or publish a Channel message.
	return &dex.RPCResult[dex.None]{}, nil
}
```

- `dex:input` takes `field-name`, `value-type`, `source`, `required`,
  `description`, and optional `attribute-key` or `capture`.
- `source:user` forbids `attribute-key`.
- `source:attribute` requires a matching scalar Attribute key. Dex Web fills
  that input from the Run and hides it from the user.
- Requiredness matches pointer semantics. Optional fields are pointers.
- `capture:qr-code` applies only to a user-sourced string field.

## Start input

The Start Flow form comes from the input type of the Step registered with
`dex.DefineStartStep`. Use a concrete typed business input with JSON tags. Do
not add `dex:input` directives to the start Step. Do not put Worker addresses,
actor permissions, or engine run IDs in business input. Pass the start Step's
input type to `StartFlow`, or declare `dex.None` and pass `nil`.

## Focused verification

After you change a definition, render every Flow source file:

```bash
dexcli visualize path/to/flow.go --schema-version 2.0 --json --out DIRECTORY/<flow-name>
```

Analysis neither deploys nor calls providers. Require `valid: true` and fix
every blocking diagnostic. With blocking diagnostics the command still writes
partial JSON and exits non-zero. Keep the rendering in one project command,
so every Flow is checked, not only the one you edited.

If analysis fails, read the partial JSON diagnostics before you fetch
unrelated source. Compile first when a diagnostic reports Go syntax or type
errors.

| Diagnostic | Correction |
| --- | --- |
| `v2_view_rpc` | Define and register both view methods with the exact signatures. |
| `v2_view_rpc_output` | Return one literal map with exactly the declared field keys. |
| `v2_directive` | Match the Attribute's real type and the argument names. An unnamed slice is `array` (`[]string` is `string-array`), not `json`. |
| `v2_action` | Use direct typed Action registration, a declared scalar condition, and one permission. |
| `v2_action_input` | Declare every JSON field once. Use a pointer for `required:false`. |
| `v2_start_input` | Use a concrete typed start input the form can render. |
| `hidden_dex_decision` | Return Dex decisions directly from Step methods. Helpers may compute input or record state. |
| `unknown_step_target` | Register the target and use its real Go type or the exact static Connector Step reference. |
| `connector_factory_step_type` | Give the operation factory a nonempty compile-time `StepType`. |
| `unused_resource` | Use the primitive visibly in the Flow's Step or RPC methods or in same-package helpers they call. Remove obsolete scaffold state. |
| `connector_release_required` | Require an exact official published module version with no local replacement. Handoff pins the exact official release. |
| `attribute_map_read_not_loaded`, `attribute_map_enumeration_not_loaded`, `attribute_map_instance_not_loaded`, `channel_messages_not_loaded` | Select the AttributeMap instance or whole map, or the Channel messages, in the options of the Step, RPC, or timeout handler that reads them. An unselected read fails at run time. |
| `step_progress_outside_step` | Write Stream output only in a Step. An RPC publishes a Channel message or returns next Steps to schedule that Step. |
| `start_flow_input_type_mismatch` | Pass the start Step's input type to `StartFlow`, or declare `dex.None` and pass `nil`. |
| `v2_step_group_shape` | A warning only. Split the group so that each group is one run or one choice. |

A valid FDG proves a definition, not a provider call. Keep source readiness
separate from configured real business acceptance.
