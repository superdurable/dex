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
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// requireEntityStoreSmokeUserProfileRow polls PostgreSQL because Attribute Sync is asynchronous.
func requireEntityStoreSmokeUserProfileRow(t *testing.T, userID string) {
	t.Helper()
	composeProject := os.Getenv("DEX_EXAMPLES_ENTITY_STORE_COMPOSE_PROJECT")
	composeFile := os.Getenv("DEX_EXAMPLES_ENTITY_STORE_COMPOSE_FILE")
	require.NotEmpty(t, composeProject, "DEX_EXAMPLES_ENTITY_STORE_COMPOSE_PROJECT must be set; run ./run-e2e-tests.sh")
	require.NotEmpty(t, composeFile, "DEX_EXAMPLES_ENTITY_STORE_COMPOSE_FILE must be set; run ./run-e2e-tests.sh")

	// userID comes from newFlowID and contains no quote characters.
	query := "SELECT display_name, email, marketing_opt_in, credits, weight, " +
		"to_char(last_logged_in_time AT TIME ZONE 'UTC', 'YYYY-MM-DD\"T\"HH24:MI:SS\"Z\"'), " +
		"metadata->>'source', metadata->'tags'->>0 " +
		"FROM user_profiles WHERE user_id = '" + userID + "'"
	expectedRow := strings.Join([]string{
		"Smoke Tester",
		userID + "@example.com",
		"t",
		"120",
		"59.5",
		"2026-08-11T15:30:00Z",
		"smoke",
		"example",
	}, "|")

	checkRowSynced := func(collect *assert.CollectT) {
		command := exec.Command(
			"docker", "compose", "-p", composeProject, "-f", composeFile,
			"exec", "-T", "entity-postgres",
			"psql", "-U", "entity_store", "-d", "entity_store",
			"-v", "ON_ERROR_STOP=1", "-At", "-F", "|", "-c", query,
		)
		output, err := command.CombinedOutput()
		if !assert.NoError(collect, err, "psql output: %s", output) {
			return
		}
		assert.Equal(collect, expectedRow, strings.TrimSpace(string(output)),
			"user_profiles row for %s", userID)
	}
	require.EventuallyWithT(t, checkRowSynced, 30*time.Second, 200*time.Millisecond,
		"user_profiles row for %s did not sync", userID)
	t.Logf("entity-store row assertion passed: user_id=%s row=%s", userID, expectedRow)
}
