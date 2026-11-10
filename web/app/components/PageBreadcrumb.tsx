// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { Link } from 'react-router-dom';
import { FLOWS_PATH } from '../routes';

/** Leads a page other than a run back to the Flows search, the way the run page breadcrumb does. */
export function PageBreadcrumb({ pageName }: { pageName: string }) {
  return (
    <nav aria-label="Breadcrumb" className="breadcrumbs page-breadcrumbs">
      <Link to={FLOWS_PATH}>Flows</Link><span>/</span>
      <span aria-current="page">{pageName}</span>
    </nav>
  );
}
