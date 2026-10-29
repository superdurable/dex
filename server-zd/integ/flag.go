// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package integ

import (
	"flag"
	"os"
)

var dexServerBinary = flag.String("dex-server-binary", os.Getenv("DEX_SERVER_BINARY"), "dex-server-zd binary to launch per scenario")
var repeatIntegTest = flag.Int("repeat", 1, "number of scenario repetitions")
var repeatInterval = flag.Int("intervalMs", 0, "interval between repetitions in milliseconds")
var testSearchIntegTest = flag.Bool("search", true, "run search integration scenarios")
var searchWaitTimeIntegTest = flag.Int("searchWaitMs", 2000, "search indexing wait in milliseconds")
