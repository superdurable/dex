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

package extensions

import (
	"fmt"

	"github.com/superdurable/dex/server/config"
)

var sqlRegistry = map[string]SQLDBExtension{}

// RegisterSQLDBExtension will register a SQL extension
func RegisterSQLDBExtension(name string, ext SQLDBExtension) {
	if _, ok := sqlRegistry[name]; ok {
		panic("SQL extension " + name + " already registered")
	}
	sqlRegistry[name] = ext
}

// NewSQLSession returns a regular session
func NewSQLSession(cfg *config.SQL) (SQLDBSession, error) {
	ext, ok := sqlRegistry[cfg.DBExtensionName]

	if !ok {
		return nil, fmt.Errorf("not supported SQLDBExtensionName %v, only supported: %v", cfg.DBExtensionName, sqlRegistry)
	}

	return ext.StartDBSession(cfg)
}

// NewSQLAdminSession returns a AdminDB
func NewSQLAdminSession(cfg *config.SQL) (SQLAdminDBSession, error) {
	ext, ok := sqlRegistry[cfg.DBExtensionName]

	if !ok {
		return nil, fmt.Errorf("not supported SQLDBExtensionName %v, only supported: %v", cfg.DBExtensionName, sqlRegistry)
	}

	return ext.StartAdminDBSession(cfg)
}
