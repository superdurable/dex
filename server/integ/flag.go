// Copyright (c) 2022-2026 Super Durable, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package integ

import (
	"flag"
)

var repeatIntegTest = flag.Int("repeat", 1, "the number of repeat")

var repeatInterval = flag.Int("intervalMs", 0, "the interval between test in milliseconds")

var cadenceIntegTest = flag.Bool("cadence", true, "run integ test against cadence")

var temporalIntegTest = flag.Bool("temporal", true, "run integ test against temporal")

var testSearchIntegTest = flag.Bool("search", true, "run search integ test against temporal/Cadence")

var searchWaitTimeIntegTest = flag.Int("searchWaitMs", 2000, "the amount of time to wait for ElasticSearch being able to search in milliseconds")

var temporalHostPort = flag.String("temporalHostPort", "", "temporal host port")

var dexServerAddress = flag.String("dexServerAddress", "", "existing Dex gRPC address")

var dependencyWaitSeconds = flag.Int("dependencyWaitSeconds", 60, "the number of seconds waiting for dependencies to be up(Cadence/Temporal)")

var disableStickyCache = flag.Bool("disableStickyCache", false, "disable Temporal/Cadence sticky execution")
