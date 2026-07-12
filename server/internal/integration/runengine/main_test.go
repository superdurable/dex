package runengine

import (
	"testing"

	"github.com/superdurable/dex/server/internal/integration/testhelpers"
)

func TestMain(m *testing.M) { testhelpers.RunMain(m, dbPrefix) }
