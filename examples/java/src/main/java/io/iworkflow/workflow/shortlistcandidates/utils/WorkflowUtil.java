/*
 * Copyright (c) 2022-2026 Super Durable, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package io.iworkflow.workflow.shortlistcandidates.utils;

import io.iworkflow.core.Client;
import io.iworkflow.core.exceptions.WorkflowAlreadyStartedException;
import io.iworkflow.workflow.shortlistcandidates.EmployerOptInWorkflow;

public class WorkflowUtil {
    public static String buildEmployerOptInWorkflowId(final String employerId) {
        return "shortlist_candidates_opt_in_" + employerId;
    }

    public static String buildShortlistWorkflowId(final String employerId, final String candidateId) {
        return "shortlist_candidates_shortlist_" + employerId + "_" + candidateId;
    }

    public static Boolean isOptedIn(final Client client, final String employerId) {
        final String workflowId = buildEmployerOptInWorkflowId(employerId);

        final EmployerOptInWorkflow rpcStub = client.newRpcStub(
                EmployerOptInWorkflow.class,
                workflowId
        );

        try {
            return client.invokeRPC(rpcStub::isOptedIn);
        } catch (final WorkflowAlreadyStartedException e) {
            return false;
        }
    }
}
