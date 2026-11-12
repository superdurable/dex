/*
 * Copyright (c) 2026 Super Durable, Inc.
 *
 * Licensed under the Super Durable Source License 1.0.
 * You may not use this file except in compliance with the License.
 * See the LICENSE file in the repository root.
 *
 * SPDX-License-Identifier: LicenseRef-Super-Durable-1.0
 */

import {isSdkLanguage, persistSdkPreference, readSdkPreference} from '../components/sdkPreference';

// A link such as /quick-start?sdk=go selects that SDK on arrival, the same as clicking its tab.
export function onRouteDidUpdate({location}: {location: {search: string}}): void {
  const language = new URLSearchParams(location.search).get('sdk');
  if (isSdkLanguage(language) && readSdkPreference() !== language) {
    persistSdkPreference(language);
  }
}
