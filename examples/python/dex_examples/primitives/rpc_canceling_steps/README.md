# RPC canceling steps

A user stops a report while **GenerateReport** runs. The **stop_report** RPC
cancels **GenerateReport** and moves to **FinishReportStop**, which decides the
outcome from committed state.

HTTP:
- `GET /primitives/rpc-canceling-steps/start?workflowId=...&reportName=q3`
- `GET /primitives/rpc-canceling-steps/stop?workflowId=...`
