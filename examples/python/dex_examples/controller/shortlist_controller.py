# Copyright (c) 2022-2026 Super Durable, Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

from __future__ import annotations

from dex import DexException, ErrorSubStatus
from quart import Blueprint, Response, jsonify

from dex_examples.app import ExampleApp
from dex_examples.config import start_options
from dex_examples.controller.query import optional_query, required_body_field
from dex_examples.workflow.shortlistcandidates import workflow_ids
from dex_examples.workflow.shortlistcandidates.employer_opt_in_input import (
    EmployerOptInInput,
)
from dex_examples.workflow.shortlistcandidates.shortlist_input import ShortlistInput


def create_shortlist_blueprint(app_state: ExampleApp) -> Blueprint:
    blueprint = Blueprint(
        "shortlist_candidates",
        __name__,
        url_prefix="/shortlist_candidates",
    )

    async def check_opted_in(employer_id: str) -> bool:
        try:
            return await app_state.client.invoke_rpc(
                app_state.employer_opt_in.is_opted_in,
                workflow_ids.employer_opt_in(employer_id),
            )
        except DexException as error:
            if error.sub_status is ErrorSubStatus.FLOW_NOT_EXISTS:
                return False
            raise

    @blueprint.post("/opt_in")
    async def opt_in() -> str:
        employer_id = await required_body_field("employerId")
        flow_id = workflow_ids.employer_opt_in(employer_id)
        try:
            await app_state.client.start_flow(
                app_state.employer_opt_in,
                flow_id,
                EmployerOptInInput(employer_id),
                start_options(),
            )
        except DexException as error:
            if error.sub_status is not ErrorSubStatus.FLOW_ALREADY_STARTED:
                raise
            return f"Employer {employer_id} has already opted in"
        return f"Started workflowId: {flow_id}"

    @blueprint.post("/opt_out")
    async def opt_out() -> str:
        employer_id = await required_body_field("employerId")
        try:
            await app_state.client.invoke_rpc(
                app_state.employer_opt_in.opt_out,
                workflow_ids.employer_opt_in(employer_id),
            )
        except DexException as error:
            if error.sub_status is not ErrorSubStatus.FLOW_NOT_EXISTS:
                raise
            return f"Employer {employer_id} is not in the opt-in status"
        return f"Employer {employer_id} has opted out"

    @blueprint.get("/is_opted_in")
    async def is_opted_in() -> Response:
        employer_id = optional_query("employerId", "test-employer")
        return jsonify(await check_opted_in(employer_id))

    @blueprint.post("/shortlist")
    async def shortlist() -> str:
        employer_id = await required_body_field("employerId")
        candidate_id = await required_body_field("candidateId")

        if not await check_opted_in(employer_id):
            return f"Do nothing for {employer_id}-{candidate_id} because of no opt-in"

        flow_id = workflow_ids.shortlist(employer_id, candidate_id)
        try:
            await app_state.client.start_flow(
                app_state.shortlist,
                flow_id,
                ShortlistInput(employer_id, candidate_id),
                start_options(),
            )
        except DexException as error:
            if error.sub_status is not ErrorSubStatus.FLOW_ALREADY_STARTED:
                raise
            return f"Already running workflowId: {flow_id}"
        return f"Started workflowId: {flow_id}"

    @blueprint.post("/revoke_shortlist")
    async def revoke_shortlist() -> str:
        employer_id = await required_body_field("employerId")
        candidate_id = await required_body_field("candidateId")
        try:
            await app_state.client.publish(
                workflow_ids.shortlist(employer_id, candidate_id),
                app_state.shortlist.revoke_shortlist,
                None,
            )
        except DexException as error:
            if error.sub_status is not ErrorSubStatus.FLOW_NOT_EXISTS:
                raise
            return f"No running workflow to revoke for {employer_id}-{candidate_id}"
        return f"Revoked shortlist for {employer_id}-{candidate_id}"

    @blueprint.get("/email_sent_timestamp")
    async def email_sent_timestamp() -> Response | tuple[Response, int]:
        employer_id = optional_query("employerId", "test-employer")
        candidate_id = optional_query("candidateId", "test-candidate")
        try:
            timestamp = await app_state.client.invoke_rpc(
                app_state.shortlist.get_email_sent_timestamp,
                workflow_ids.shortlist(employer_id, candidate_id),
            )
        except DexException as error:
            if error.sub_status is not ErrorSubStatus.FLOW_NOT_EXISTS:
                raise
            return jsonify({"error": "shortlist flow does not exist"}), 404
        return jsonify(timestamp)

    return blueprint
