package membership

import (
	"net"
	"strconv"

	"github.com/superdurable/dex/server/internal/errors"
)

func splitHostPort(addr string) (string, int, errors.CategorizedError) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", 0, errors.NewInternalError("failed to parse bind address", err)
	}
	portInt, err := strconv.Atoi(port)
	if err != nil {
		return "", 0, errors.NewInternalError("failed to parse bind port", err)
	}

	return host, portInt, nil
}
