// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package web

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/superdurable/dex/web/api"
)

type connectorPackageIdentity = api.V2ConnectorPackageIdentity

var connectorReleaseTagPattern = regexp.MustCompile(`^connectors/[a-z0-9][a-z0-9-]*(?:/[a-z0-9][a-z0-9-]*)*/v[0-9]+\.[0-9]+\.[0-9]+(?:-rc\.[0-9]+)?$`)
var connectorPackageVersionPattern = regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+(?:[-+.a-zA-Z0-9]*)$`)

func connectorReleaseModuleIdentity(identity connectorDefinitionIdentity) (connectorDefinitionIdentity, error) {
	if identity.ReleaseTag == "" && identity.Package == nil {
		return identity, nil
	}
	if !connectorReleaseTagPattern.MatchString(identity.ReleaseTag) || identity.Package == nil ||
		strings.TrimSpace(identity.Package.Name) == "" || !connectorPackageVersionPattern.MatchString(identity.Package.Version) {
		return identity, fmt.Errorf("Connector releaseTag and exact package identity are required")
	}
	switch identity.Package.Ecosystem {
	case "go", "pypi", "npm":
	default:
		return identity, fmt.Errorf("Connector package ecosystem must be go, pypi, or npm")
	}
	separator := strings.LastIndexByte(identity.ReleaseTag, '/')
	modulePath := "github.com/superdurable/dex-connectors-library/" + identity.ReleaseTag[:separator]
	moduleVersion := identity.ReleaseTag[separator+1:]
	if (identity.ModulePath != "" && identity.ModulePath != modulePath) ||
		(identity.ModuleVersion != "" && identity.ModuleVersion != moduleVersion) {
		return identity, fmt.Errorf("Connector legacy module identity conflicts with releaseTag")
	}
	identity.ModulePath, identity.ModuleVersion = modulePath, moduleVersion
	return identity, nil
}
