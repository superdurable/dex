# Legacy Materials in this file remain under their original licenses.
# See LEGACY_NOTICES.md.

# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications after the Legacy Cutoff are licensed under the
# Super Durable Source License 1.0.
# Legacy Materials remain under their original licenses.
# See LICENSE and LEGACY_NOTICES.md.

import inspect
import time
import unittest

from dex.client import Client
from dex.tests.worker_server import registry
from dex.tests.workflows.persistence_search_attributes_workflow import (
    PersistenceSearchAttributesWorkflow,
    sa_keyword_key,
    sa_double_key,
    sa_int_key,
    sa_bool_key,
    sa_datetime_key,
    sa_keyword_array_key,
    initial_sa_keyword,
    initial_sa_double,
    initial_sa_int,
    initial_sa_bool,
    initial_sa_datetime,
    initial_sa_keyword_array,
    SearchAttributeState1,
    sa_keyword,
    sa_double,
    sa_bool,
    sa_keyword_array,
    sa_int,
    final_sa_keyword,
    final_sa_bool,
    final_sa_int,
    final_sa_keyword_array,
)
from dex.workflow_options import WorkflowOptions

class TestPersistenceSearchAttributes(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.client = Client(registry)

    def test_persistence_search_attributes_workflow(self):
        wf_id = f"{inspect.currentframe().f_code.co_name}-{time.time_ns()}"

        wf_opts = WorkflowOptions(
            initial_search_attributes={
                sa_keyword_key: initial_sa_keyword,
                sa_double_key: initial_sa_double,
                sa_int_key: initial_sa_int,
                sa_bool_key: initial_sa_bool,
                sa_datetime_key: initial_sa_datetime,
                sa_keyword_array_key: initial_sa_keyword_array,
            }
        )
        wf_opts.add_wait_for_completion_state_ids(SearchAttributeState1)
        self.client.start_workflow(
            PersistenceSearchAttributesWorkflow, wf_id, 100, None, wf_opts
        )

        initial_returned_search_attributes = self.client.get_all_search_attributes(
            PersistenceSearchAttributesWorkflow,
            wf_id,
        )

        initial_expected_search_attributes = dict()
        initial_expected_search_attributes[sa_keyword_key] = initial_sa_keyword
        initial_expected_search_attributes[sa_double_key] = initial_sa_double
        initial_expected_search_attributes[sa_bool_key] = initial_sa_bool
        initial_expected_search_attributes[sa_keyword_array_key] = (
            initial_sa_keyword_array
        )
        initial_expected_search_attributes[sa_int_key] = initial_sa_int
        initial_expected_search_attributes[sa_datetime_key] = (
            "2024-11-10T00:00:01.731455544Z"  # This is a bug. The dex-server always returns utc time. See https://github.com/superdurable/dex/issues/261
            # "2024-11-09T18:00:01.731455544-06:00"
        )

        assert initial_expected_search_attributes == initial_returned_search_attributes

        self.client.wait_for_state_execution_completion_with_state_execution_id(
            SearchAttributeState1, wf_id
        )

        returned_search_attributes = self.client.get_all_search_attributes(
            PersistenceSearchAttributesWorkflow,
            wf_id,
        )

        expected_search_attributes = dict()
        expected_search_attributes[sa_keyword_key] = sa_keyword
        expected_search_attributes[sa_double_key] = sa_double
        expected_search_attributes[sa_bool_key] = sa_bool
        expected_search_attributes[sa_keyword_array_key] = sa_keyword_array
        expected_search_attributes[sa_int_key] = sa_int
        expected_search_attributes[sa_datetime_key] = (
            "2024-11-13T00:00:01.731455544Z"  # This is a bug. The dex-server always returns utc time. See https://github.com/superdurable/dex/issues/261
            # "2024-11-12T18:00:01.731455544-06:00"
        )

        assert expected_search_attributes == returned_search_attributes

        self.client.wait_for_workflow_completion(wf_id)

        final_expected_search_attributes = dict()
        final_expected_search_attributes[sa_keyword_key] = final_sa_keyword
        final_expected_search_attributes[sa_double_key] = sa_double
        final_expected_search_attributes[sa_int_key] = final_sa_int
        final_expected_search_attributes[sa_bool_key] = final_sa_bool
        final_expected_search_attributes[sa_keyword_array_key] = final_sa_keyword_array
        final_expected_search_attributes[sa_datetime_key] = (
            "2024-12-14T00:00:01.731455544Z"  # This is a bug. The dex-server always returns utc time. See https://github.com/superdurable/dex/issues/261
            # "2024-12-13T18:00:01.731455544-06:00"
        )

        final_returned_search_attributes = self.client.get_all_search_attributes(
            PersistenceSearchAttributesWorkflow,
            wf_id,
        )

        assert final_expected_search_attributes == final_returned_search_attributes
