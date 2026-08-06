# Portions of this file are derived from indeedeng/iwf-java-sdk.
# Those portions are licensed under the Apache License, Version 2.0.
# See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
#
# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications are licensed under the Super Durable Source License 1.0.
# Third-Party Materials remain under the Apache License, Version 2.0.
# See LICENSE and LEGACY_NOTICES.md.

from dex import Client

from . import iwf_flows


def compile_movement_options_override(client: Client) -> None:
    client.start_flow(
        iwf_flows.STATE_OPTIONS_OVERRIDE,
        "options-override",
        "input",
    )
    output: str = client.wait_for_flow("options-override", str)
    del output
