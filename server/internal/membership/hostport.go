// Copyright (c) 2023-2026 Super Durable, Inc.
//
// This file is part of Dex
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program. If not, see <https://www.gnu.org/licenses/>.

package membership

import "strconv"

func ParseHostPort(addr string) (string, int) {
	host := "0.0.0.0"
	port := 7946

	if addr == "" {
		return host, port
	}

	for i := len(addr) - 1; i >= 0; i-- {
		if addr[i] == ':' {
			host = addr[:i]
			if parsedPort, err := strconv.Atoi(addr[i+1:]); err == nil {
				port = parsedPort
			}
			break
		}
	}

	if host == "" {
		host = "0.0.0.0"
	}
	return host, port
}
