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

package io.superdurable.dex.workflow.shortlistcandidates;

import io.superdurable.dex.Client;
import io.superdurable.dex.DexException;
import io.superdurable.dex.ErrorSubStatus;

public final class WorkflowIds {
    private WorkflowIds() {
    }

    public static String employerOptIn(final String employerId) {
        return "shortlist_candidates_opt_in_" + employerId;
    }

    public static String shortlist(final String employerId, final String candidateId) {
        return "shortlist_candidates_shortlist_" + employerId + "_" + candidateId;
    }

    public static boolean isOptedIn(
            final Client client,
            final EmployerOptInFlow employerOptInFlow,
            final String employerId) {
        try {
            final EmployerOptInFlow stub =
                    client.newRpcStub(EmployerOptInFlow.class, employerOptIn(employerId));
            return Boolean.TRUE.equals(client.invokeRPC(stub::isOptedIn));
        } catch (final DexException exception) {
            if (exception.getSubStatus() == ErrorSubStatus.FLOW_NOT_EXISTS) {
                return false;
            }
            throw exception;
        }
    }
}
