// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { Navigate, Route, Routes, useParams } from 'react-router-dom';
import { AppHeader } from './components/AppHeader';
import { CurrentRunRedirect } from './flows/CurrentRunRedirect';
import { FlowSearchPage } from './flows/FlowSearchPage';
import { RunDetailsPage } from './flows/RunDetailsPage';
import { PreferencesProvider } from './providers';
import { FlowRenderingPage } from './rendering/FlowRenderingPage';
import {
  HomePage,
  SupervisionDetailPage,
  SupervisionHomePage,
  SupervisionListPage,
} from './supervision/SupervisionPages';
import { SupervisionProvider } from './supervision/SupervisionProvider';

export function App() {
  return (
    <PreferencesProvider>
      <SupervisionProvider>
        <AppHeader />
        <main className="app-main">
          <Routes>
            <Route path="/" element={<HomePage />} />
            <Route path="/supervision" element={<SupervisionHomePage />} />
            <Route path="/supervision/:flowType" element={<SupervisionListPage />} />
            <Route path="/supervision/:flowType/:flowId" element={<SupervisionDetailPage />} />
            <Route path="/flows" element={<FlowSearchPage />} />
            <Route path="/rendering" element={<FlowRenderingPage />} />
            <Route path="/flows/:flowId" element={<CurrentFlowRoute />} />
            <Route path="/flows/:flowId/:runId" element={<FlowRunRoute />} />
            <Route path="*" element={<Navigate to="/" replace />} />
          </Routes>
        </main>
      </SupervisionProvider>
    </PreferencesProvider>
  );
}

function CurrentFlowRoute() {
  const { flowId = '' } = useParams();
  return <CurrentRunRedirect flowId={flowId} />;
}

function FlowRunRoute() {
  const { flowId = '', runId = '' } = useParams();
  return <RunDetailsPage flowId={flowId} runId={runId} />;
}
