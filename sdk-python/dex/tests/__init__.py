# Legacy Materials in this file remain under their original licenses.
# See LEGACY_NOTICES.md.

# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications after the Legacy Cutoff are licensed under the
# Super Durable Source License 1.0.
# Legacy Materials remain under their original licenses.
# See LICENSE and LEGACY_NOTICES.md.

from dex.registry import Registry
from dex.tests.workflows.abnormal_exit_workflow import AbnormalExitWorkflow
from dex.tests.workflows.basic_workflow import BasicWorkflow
from dex.tests.workflows.conditional_complete_workflow import (
    ConditionalCompleteWorkflow,
)
from dex.tests.workflows.describe_workflow import DescribeWorkflow
from dex.tests.workflows.empty_data_workflow import EmptyDataWorkflow
from dex.tests.workflows.internal_channel_workflow import InternalChannelWorkflow
from dex.tests.workflows.internal_channel_workflow_with_no_prefix_channel import (
    InternalChannelWorkflowWithNoPrefixChannel,
)
from dex.tests.workflows.java_duplicate_rpc_memo_workflow import (
    JavaDuplicateRpcMemoWorkflow,
)
from dex.tests.workflows.persistence_data_attributes_workflow import (
    PersistenceDataAttributesWorkflow,
)
from dex.tests.workflows.persistence_search_attributes_workflow import (
    PersistenceSearchAttributesWorkflow,
)
from dex.tests.workflows.persistence_state_execution_local_workflow import (
    PersistenceStateExecutionLocalWorkflow,
)
from dex.tests.workflows.recovery_workflow import RecoveryWorkflow
from dex.tests.workflows.rpc_memo_workflow import RpcMemoWorkflow
from dex.tests.workflows.rpc_workflow import RPCWorkflow
from dex.tests.workflows.state_options_override_workflow import (
    StateOptionsOverrideWorkflow,
)
from dex.tests.workflows.state_options_workflow import (
    StateOptionsWorkflow1,
    StateOptionsWorkflow2,
)
from dex.tests.workflows.timer_workflow import TimerWorkflow
from dex.tests.workflows.wait_for_state_with_state_execution_id_workflow import (
    WaitForStateWithStateExecutionIdWorkflow,
)
from dex.tests.workflows.wait_for_state_with_wait_for_key_workflow import (
    WaitForStateWithWaitForKeyWorkflow,
)
from dex.tests.workflows.wait_internal_channel_workflow import (
    WaitInternalChannelWorkflow,
)
from dex.tests.workflows.wait_signal_workflow import WaitSignalWorkflow

registry = Registry()

registry.add_workflow(AbnormalExitWorkflow())
registry.add_workflow(BasicWorkflow())
registry.add_workflow(ConditionalCompleteWorkflow())
registry.add_workflow(DescribeWorkflow())
registry.add_workflow(EmptyDataWorkflow())
registry.add_workflow(InternalChannelWorkflow())
registry.add_workflow(InternalChannelWorkflowWithNoPrefixChannel())
registry.add_workflow(JavaDuplicateRpcMemoWorkflow())
registry.add_workflow(PersistenceDataAttributesWorkflow())
registry.add_workflow(PersistenceSearchAttributesWorkflow())
registry.add_workflow(PersistenceStateExecutionLocalWorkflow())
registry.add_workflow(RecoveryWorkflow())
registry.add_workflow(RpcMemoWorkflow())
registry.add_workflow(RPCWorkflow())
registry.add_workflow(StateOptionsOverrideWorkflow())
registry.add_workflow(StateOptionsWorkflow1())
registry.add_workflow(StateOptionsWorkflow2())
registry.add_workflow(TimerWorkflow())
registry.add_workflow(WaitForStateWithStateExecutionIdWorkflow())
registry.add_workflow(WaitForStateWithWaitForKeyWorkflow())
registry.add_workflow(WaitInternalChannelWorkflow())
registry.add_workflow(WaitSignalWorkflow())
