/*
 * Legacy Materials in this file remain under their original licenses.
 * See LEGACY_NOTICES.md.
 */

/*
 * Modifications Copyright (c) 2026 Super Durable, Inc.
 *
 * Modifications after the Legacy Cutoff are licensed under the
 * Super Durable Source License 1.0.
 * Legacy Materials remain under their original licenses.
 * See LICENSE and LEGACY_NOTICES.md.
 */

package io.superdurable.dex.spring.controller;

import io.superdurable.dex.core.WorkerOptions;
import io.superdurable.dex.core.WorkerService;
import io.superdurable.dex.gen.models.WorkerErrorResponse;
import io.superdurable.dex.gen.models.WorkflowStateExecuteRequest;
import io.superdurable.dex.gen.models.WorkflowStateWaitUntilRequest;
import io.superdurable.dex.gen.models.WorkflowWorkerRpcRequest;
import org.springframework.http.ResponseEntity;
import org.springframework.stereotype.Controller;
import org.springframework.web.bind.annotation.PostMapping;
import org.springframework.web.bind.annotation.RequestBody;
import org.springframework.web.bind.annotation.RequestMapping;

import java.io.PrintWriter;
import java.io.StringWriter;

@Controller
public class ApiController {

    private WorkerService workerService;

    public ApiController(WorkflowRegistry registry) {
        workerService = new WorkerService(registry.getRegistry(), WorkerOptions.defaultOptions);
    }

    @RequestMapping("/")
    public ResponseEntity<String> index() {
        return ResponseEntity.ok("Hello, world!");
    }

    @PostMapping(WorkerService.WORKFLOW_STATE_WAIT_UNTIL_API_PATH)
    public ResponseEntity<?> apiV1WorkflowStateStartPost(
            final @RequestBody WorkflowStateWaitUntilRequest request
    ) {
        try {
            return ResponseEntity.ok(workerService.handleWorkflowStateWaitUntil(request));
        } catch (RuntimeException e) {
            return processWorkerException(request.getContext().getWorkflowId(), e, "WaitUntil");
        }
    }

    @PostMapping(WorkerService.WORKFLOW_STATE_EXECUTE_API_PATH)
    public ResponseEntity<?> apiV1WorkflowStateDecidePost(
            final @RequestBody WorkflowStateExecuteRequest request
    ) {
        try {
            return ResponseEntity.ok(workerService.handleWorkflowStateExecute(request));
        } catch (RuntimeException e) {
            return processWorkerException(request.getContext().getWorkflowId(), e, "Execute");
        }
    }

    @PostMapping(WorkerService.WORKFLOW_WORKER_RPC_API_PATH)
    public ResponseEntity<?> apiV1WorkflowWorkerRpcPost(
            final @RequestBody WorkflowWorkerRpcRequest request
    ) {
        try {
            return ResponseEntity.ok(workerService.handleWorkflowWorkerRpc(request));
        } catch (RuntimeException e) {
            return processWorkerException(request.getContext().getWorkflowId(), e, "RPC");
        }
    }

    private ResponseEntity<?> processWorkerException(final String workflowId, final RuntimeException e, final String methodType) {
        // This info log is for troubleshooting because the default exception handler may not log it properly
        StringWriter sw = new StringWriter();
        PrintWriter pw = new PrintWriter(sw);
        e.printStackTrace(pw);
        String sStackTrace = sw.toString(); // stack trace as a string
        if(sStackTrace.length()>1000){
            // make sure NOT exceeding 4K limit in Temporal https://github.com/superdurable/dex/issues/272
            sStackTrace = sStackTrace.substring(0, 1000) + "...(TRUNCATED)";
        }
        String msg = e.getMessage();
        if(msg == null){
            System.out.println("empty message for exception "+sw.toString());
            msg = "empty message for exception "+e;
        }
        if (msg.length() > 500) {
            msg = msg.substring(0, 500) + "...(TRUNCATED)";
        }

        System.out.println("Exception in "+methodType+" for workflow "+workflowId+" : "+msg + "\n STACKTRACE: \n"+sStackTrace);

        final WorkerErrorResponse errResp = new WorkerErrorResponse()
                .detail(msg+"\n STACKTRACE: \n"+sStackTrace)
                .errorType(e.getClass().getName());
        return ResponseEntity.status(501).body(errResp);
    }

}