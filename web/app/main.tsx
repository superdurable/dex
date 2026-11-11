// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { BrowserRouter } from 'react-router-dom';
import './globals.css';
import './v1-design.css';
import { App } from './App';
import { routerBasename } from '@/lib/basePath';

document.title = 'Dex · Super Durable';

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <BrowserRouter basename={routerBasename()}>
      <App />
    </BrowserRouter>
  </StrictMode>,
);
