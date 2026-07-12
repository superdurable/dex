package matchingfanin

import (
	"testing"

	"github.com/superdurable/dex/server/internal/integration/testhelpers"
)

const dbPrefix = "dex_test_integration_matchingfanin"

func TestMain(m *testing.M) { testhelpers.RunMain(m, dbPrefix) }
