package asyncpickup

import (
	"testing"

	"github.com/superdurable/dex/server/internal/integration/testhelpers"
)

const dbPrefix = "dex_test_integration_asyncpickup"

func TestMain(m *testing.M) { testhelpers.RunMain(m, dbPrefix) }
