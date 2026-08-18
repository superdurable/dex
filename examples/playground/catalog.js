/*
Copyright (c) 2022-2026 Super Durable, Inc.

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in
all copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
THE SOFTWARE.
*/

function flowId(name = "workflowId") {
  return { name, in: "query", role: "flowId" };
}

function query(name, extra) {
  return Object.assign({ name, in: "query" }, extra || {});
}

function body(name, extra) {
  return Object.assign({ name, in: "body" }, extra || {});
}

function endpoint(method, path, title, fields, extra) {
  return Object.assign({ method, path, title, fields: fields || [] }, extra || {});
}

window.PLAYGROUND_CATALOG = [
  {
    group: "products",
    id: "engagement",
    title: "Engagement",
    flowIdPrefix: "engagement",
    note: "Start mints its own flowID in most languages; copy it from the response.",
    endpoints: [
      endpoint("GET", "/products/engagement/start", "Start"),
      endpoint("GET", "/products/engagement/describe", "Describe", [flowId()]),
      endpoint("GET", "/products/engagement/optout", "Opt out of reminder", [flowId()]),
      endpoint("GET", "/products/engagement/decline", "Decline", [
        flowId(),
        query("notes", { default: "not a fit" }),
      ]),
      endpoint("GET", "/products/engagement/accept", "Accept", [
        flowId(),
        query("notes", { default: "looks good" }),
      ]),
      endpoint("GET", "/products/engagement/list", "List / search", [
        query("query", { default: 'ExecutionStatus="Running"' }),
      ]),
    ],
  },
  {
    group: "products",
    id: "job-post",
    title: "Job post",
    flowIdPrefix: "job_id",
    note: "Create mints job_id_<unix>; Rust also exposes GET /start.",
    endpoints: [
      endpoint("GET", "/products/job-post/create", "Create", [
        query("title", { default: "Staff engineer" }),
        query("description", { default: "Build durable workflows" }),
      ]),
      endpoint("GET", "/products/job-post/read", "Read", [flowId()]),
      endpoint("GET", "/products/job-post/update", "Update", [
        flowId(),
        query("title", { default: "Staff engineer" }),
        query("description", { default: "Updated description" }),
        query("notes", { default: "test-notes" }),
      ]),
      endpoint("GET", "/products/job-post/delete", "Delete / stop", [flowId()]),
      endpoint("GET", "/products/job-post/search", "Search", [
        query("query", { default: "" }),
      ]),
      endpoint("GET", "/products/job-post/start", "Start (Rust)", [flowId()]),
    ],
  },
  {
    group: "products",
    id: "microservices",
    title: "Microservices",
    flowIdPrefix: "microservices",
    endpoints: [
      endpoint("GET", "/products/microservices/start", "Start", [flowId()]),
      endpoint("GET", "/products/microservices/swap", "Swap data (RPC)", [
        flowId(),
        query("data", { default: "swapped payload" }),
      ]),
      endpoint("GET", "/products/microservices/signal", "Signal ready", [flowId()]),
    ],
  },
  {
    group: "products",
    id: "money-transfer",
    title: "Money transfer",
    flowIdPrefix: "money-transfer",
    note: "Start mints its own flowID in most languages; copy it from the response.",
    endpoints: [
      endpoint("GET", "/products/money-transfer/start", "Start", [
        query("fromAccount", { default: "from-checking" }),
        query("toAccount", { default: "to-savings" }),
        query("amount", { type: "number", default: "100" }),
        query("notes", { default: "playground transfer" }),
      ]),
    ],
  },
  {
    group: "products",
    id: "order-processing",
    title: "Order processing",
    flowIdPrefix: "order-processing",
    note: "Start mints its own flowID; copy it from the response. Wait for charge, then approve shipment.",
    endpoints: [
      endpoint("GET", "/products/order-processing/start", "Start", [
        query("failShip", { default: "false" }),
      ]),
      endpoint("GET", "/products/order-processing/wait-charged", "Wait until charged", [flowId()]),
      endpoint("GET", "/products/order-processing/approve", "Approve shipment", [flowId()]),
      endpoint("GET", "/products/order-processing/describe", "Describe", [flowId()]),
    ],
  },
  {
    group: "products",
    id: "polling",
    title: "Polling",
    flowIdPrefix: "polling",
    endpoints: [
      endpoint("GET", "/products/polling/start", "Start", [
        flowId(),
        query("pollingCompletionThreshold", { type: "number", default: "3" }),
      ]),
      endpoint("GET", "/products/polling/complete", "Complete task", [
        flowId(),
        query("channel", {
          type: "select",
          options: ["task-a-completed", "task-b-completed"],
          default: "task-a-completed",
        }),
      ]),
    ],
  },
  {
    group: "products",
    id: "shortlist-candidates",
    title: "Shortlist candidates",
    flowIdPrefix: "test-employer",
    idParam: "employerId",
    note: "Flow IDs are derived from employerId / candidateId. Rust also exposes GET /start.",
    endpoints: [
      endpoint("POST", "/products/shortlist-candidates/opt_in", "Opt in", [
        body("employerId", { role: "flowId", default: "test-employer" }),
      ]),
      endpoint("POST", "/products/shortlist-candidates/opt_out", "Opt out", [
        body("employerId", { role: "flowId", default: "test-employer" }),
      ]),
      endpoint("GET", "/products/shortlist-candidates/is_opted_in", "Is opted in", [
        query("employerId", { role: "flowId", default: "test-employer" }),
      ]),
      endpoint("POST", "/products/shortlist-candidates/shortlist", "Shortlist", [
        body("employerId", { role: "flowId", default: "test-employer" }),
        body("candidateId", { default: "test-candidate" }),
      ]),
      endpoint("POST", "/products/shortlist-candidates/revoke_shortlist", "Revoke shortlist", [
        body("employerId", { role: "flowId", default: "test-employer" }),
        body("candidateId", { default: "test-candidate" }),
      ]),
      endpoint("GET", "/products/shortlist-candidates/email_sent_timestamp", "Email sent timestamp", [
        query("employerId", { role: "flowId", default: "test-employer" }),
        query("candidateId", { default: "test-candidate" }),
      ]),
      endpoint("GET", "/products/shortlist-candidates/start", "Start (Rust)", [flowId()]),
    ],
  },
  {
    group: "products",
    id: "signup",
    title: "Signup",
    flowIdPrefix: "signup-user",
    idParam: "username",
    note: "username is the flowID. Rust uses GET /start instead of /submit.",
    endpoints: [
      endpoint("GET", "/products/signup/submit", "Submit", [
        query("username", { role: "flowId" }),
        query("email", { default: "user@example.com" }),
      ]),
      endpoint("GET", "/products/signup/verify", "Verify", [
        query("username", { role: "flowId" }),
      ]),
      endpoint("GET", "/products/signup/start", "Start (Rust)", [flowId()]),
    ],
  },
  {
    group: "products",
    id: "subscription",
    title: "Subscription",
    flowIdPrefix: "subscription",
    note: "Start mints its own flowID in most languages; copy it from the response.",
    endpoints: [
      endpoint("GET", "/products/subscription/start", "Start"),
      endpoint("GET", "/products/subscription/cancel", "Cancel", [flowId()]),
      endpoint("GET", "/products/subscription/updateChargeAmount", "Update charge amount", [
        flowId(),
        query("newChargeAmount", { type: "number", default: "200" }),
      ]),
      endpoint("GET", "/products/subscription/describe", "Describe", [flowId()]),
    ],
  },
  {
    group: "patterns",
    id: "drain-channels-internal",
    title: "Drain channels (internal)",
    flowIdPrefix: "drain-internal",
    endpoints: [
      endpoint("GET", "/patterns/drain-channels/internal/start", "Start", [flowId()]),
    ],
  },
  {
    group: "patterns",
    id: "drain-channels-signal",
    title: "Drain channels (signal)",
    flowIdPrefix: "drain-signal",
    note: "Go uses /start-or-signal; other languages use /startorsignal.",
    endpoints: [
      endpoint("GET", "/patterns/drain-channels/signal/startorsignal", "Start or signal", [flowId()]),
      endpoint("GET", "/patterns/drain-channels/signal/start-or-signal", "Start or signal (Go)", [flowId()]),
    ],
  },
  {
    group: "patterns",
    id: "entity-store",
    title: "Entity store",
    flowIdPrefix: "user",
    idParam: "userId",
    note: "userId is the flowID. Rust also exposes GET /start.",
    endpoints: [
      endpoint("POST", "/patterns/entity-store/profile", "Create profile", [
        {
          name: "body",
          in: "raw-json",
          injectFlowId: "userId",
          default: JSON.stringify(
            {
              userId: "",
              displayName: "Ada Lovelace",
              email: "ada@example.com",
              marketingOptIn: true,
              credits: 100,
              weight: 61.5,
              lastLoggedInTime: "2026-01-15T09:30:00Z",
              metadata: { source: "playground", tags: ["example"] },
            },
            null,
            2,
          ),
        },
      ]),
      endpoint("POST", "/patterns/entity-store/profile/update", "Update profile", [
        {
          name: "body",
          in: "raw-json",
          injectFlowId: "userId",
          default: JSON.stringify(
            {
              userId: "",
              displayName: "Ada Lovelace",
              email: "ada@example.com",
              marketingOptIn: false,
              credits: 80,
              weight: 61.5,
              lastLoggedInTime: "2026-01-16T09:30:00Z",
              metadata: { source: "playground", tags: ["updated"] },
            },
            null,
            2,
          ),
        },
      ]),
      endpoint("GET", "/patterns/entity-store/profile", "Get profile", [
        query("userId", { role: "flowId" }),
      ]),
      endpoint("POST", "/patterns/entity-store/profile/clear", "Clear profile", [
        query("userId", { role: "flowId" }),
      ]),
      endpoint("GET", "/patterns/entity-store/start", "Start (Rust)", [flowId()]),
    ],
  },
  {
    group: "patterns",
    id: "interruptible",
    title: "Interruptible",
    flowIdPrefix: "interruptible",
    endpoints: [
      endpoint("GET", "/patterns/interruptible/start", "Start", [flowId()]),
      endpoint("GET", "/patterns/interruptible/cancel", "Cancel", [flowId()]),
    ],
  },
  {
    group: "patterns",
    id: "intervention",
    title: "Manual intervention",
    flowIdPrefix: "intervention",
    endpoints: [
      endpoint("GET", "/patterns/intervention/start", "Start", [flowId()]),
    ],
  },
  {
    group: "patterns",
    id: "parallel",
    title: "Parallel",
    flowIdPrefix: "parallel",
    endpoints: [
      endpoint("GET", "/patterns/parallel/start/simple", "Start simple", [flowId()]),
      endpoint("GET", "/patterns/parallel/start/withAwait", "Start with await", [flowId()]),
    ],
  },
  {
    group: "patterns",
    id: "parent-child",
    title: "Parent / child",
    flowIdPrefix: "parent-child",
    endpoints: [
      endpoint("GET", "/patterns/parent-child/start", "Start", [
        flowId(),
        query("numOfChildWfs", { type: "number", default: "3" }),
      ]),
    ],
  },
  {
    group: "patterns",
    id: "polling",
    title: "Polling",
    flowIdPrefix: "pattern-polling",
    endpoints: [
      endpoint("GET", "/patterns/polling/start/simple", "Start simple", [flowId()]),
      endpoint("GET", "/patterns/polling/start/backoff", "Start backoff", [flowId()]),
    ],
  },
  {
    group: "patterns",
    id: "recovery",
    title: "Recovery",
    flowIdPrefix: "recovery",
    endpoints: [
      endpoint("GET", "/patterns/recovery/start", "Start", [
        flowId(),
        query("itemName", { default: "widget" }),
        query("quantity", { type: "number", default: "2" }),
      ]),
    ],
  },
  {
    group: "patterns",
    id: "reminders",
    title: "Reminders",
    flowIdPrefix: "reminder_test_id",
    note: "Start mints reminder_test_id_<nanos>; copy it from the response.",
    endpoints: [
      endpoint("GET", "/patterns/reminders/start", "Start"),
      endpoint("GET", "/patterns/reminders/accept", "Accept", [flowId()]),
      endpoint("GET", "/patterns/reminders/optout", "Opt out", [flowId()]),
    ],
  },
  {
    group: "patterns",
    id: "resettable-timer",
    title: "Resettable timer",
    flowIdPrefix: "resettable-timer",
    endpoints: [
      endpoint("GET", "/patterns/resettable-timer/start", "Start", [flowId()]),
      endpoint("GET", "/patterns/resettable-timer/reset", "Reset", [flowId()]),
    ],
  },
  {
    group: "patterns",
    id: "resource-control",
    title: "Resource control",
    flowIdPrefix: "controller_flow",
    note: "Python-only. request id becomes processing-<id>; shutdown takes instance_id.",
    endpoints: [
      endpoint("GET", "/patterns/resource-control/request", "Enqueue request", [
        query("id", { default: "req-1" }),
        query("data", { default: "abcd" }),
      ]),
      endpoint("GET", "/patterns/resource-control/shutdown", "Shutdown instance", [
        query("instance_id", { default: "1" }),
      ]),
      endpoint("GET", "/patterns/resource-control/processing/describe", "Describe processing", [
        query("id", { default: "req-1" }),
      ]),
    ],
  },
  {
    group: "patterns",
    id: "scalable-parallel",
    title: "Scalable parallel",
    flowIdPrefix: "scalable-parallel",
    endpoints: [
      endpoint("GET", "/patterns/scalable-parallel/start", "Start", [
        flowId(),
        query("numOfChildWfs", { type: "number", default: "3" }),
      ]),
    ],
  },
  {
    group: "patterns",
    id: "timeout",
    title: "Timeout",
    flowIdPrefix: "timeout",
    endpoints: [
      endpoint("GET", "/patterns/timeout/start", "Start", [
        flowId(),
        query("successfulWorkflow", { type: "boolean", default: "true" }),
      ]),
    ],
  },
  {
    group: "patterns",
    id: "wait-for-state-completion",
    title: "Wait for state completion",
    flowIdPrefix: "wait-state",
    endpoints: [
      endpoint("GET", "/patterns/wait-for-state-completion/start", "Start", [flowId()]),
    ],
  },
  {
    group: "primitives",
    id: "attribute",
    title: "Attribute",
    flowIdPrefix: "attribute",
    endpoints: [
      endpoint("GET", "/primitives/attribute/start", "Start", [
        flowId(),
        query("message", { default: "hello from playground" }),
      ]),
    ],
  },
  {
    group: "primitives",
    id: "channel",
    title: "Channel",
    flowIdPrefix: "channel",
    endpoints: [
      endpoint("GET", "/primitives/channel/start", "Start", [
        flowId(),
        query("inputNum", { type: "number", default: "1" }),
      ]),
      endpoint("GET", "/primitives/channel/approve", "Approve", [flowId()]),
    ],
  },
  {
    group: "primitives",
    id: "client-apis",
    title: "Client APIs",
    flowIdPrefix: "client-apis",
    endpoints: [
      endpoint("GET", "/primitives/client-apis/start", "Start", [
        flowId(),
        query("keyword", { default: "playground" }),
      ]),
      endpoint("GET", "/primitives/client-apis/search", "Search", [
        query("query", { default: 'ExecutionStatus="Running"' }),
      ]),
    ],
  },
  {
    group: "primitives",
    id: "rpc",
    title: "RPC",
    flowIdPrefix: "rpc",
    endpoints: [
      endpoint("GET", "/primitives/rpc/start", "Start", [flowId()]),
      endpoint("GET", "/primitives/rpc/trigger", "Trigger", [
        flowId(),
        query("message", { default: "ping" }),
      ]),
    ],
  },
  {
    group: "primitives",
    id: "step",
    title: "Step",
    flowIdPrefix: "step",
    endpoints: [
      endpoint("GET", "/primitives/step/start", "Start", [
        flowId(),
        query("inputNum", { type: "number", default: "1" }),
      ]),
    ],
  },
  {
    group: "primitives",
    id: "step-retry",
    title: "Step retry",
    flowIdPrefix: "step-retry",
    endpoints: [
      endpoint("GET", "/primitives/step/retry/start", "Start", [
        flowId(),
        query("readyAfterAttempt", { type: "number", default: "2" }),
      ]),
    ],
  },
  {
    group: "primitives",
    id: "subflow",
    title: "SubFlow",
    flowIdPrefix: "subflow",
    endpoints: [
      endpoint("GET", "/primitives/subflow/start", "Start", [
        flowId(),
        query("inputNum", { type: "number", default: "1" }),
      ]),
    ],
  },
  {
    group: "primitives",
    id: "timer",
    title: "Timer",
    flowIdPrefix: "timer",
    endpoints: [
      endpoint("GET", "/primitives/timer/start", "Start", [
        flowId(),
        query("seconds", { type: "number", default: "2" }),
      ]),
    ],
  },
];
