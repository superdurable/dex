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
import { HomePage, V2Workspace } from './v2/V2Workspace';
import { WebCatalogProvider } from './v2/WebCatalogProvider';

export function App() {
  return (
    <PreferencesProvider>
      <WebCatalogProvider>
        <AppHeader />
        <main className="app-main">
          <Routes>
            <Route path="/" element={<HomePage />} />
            <Route path="/v2" element={<V2Workspace />} />
            <Route path="/v2/:flowType" element={<V2Workspace />} />
            <Route path="/v2/:flowType/:flowId" element={<V2Workspace />} />
            <Route path="/v1" element={<Navigate to="/v1/flows" replace />} />
            <Route path="/v1/flows" element={<FlowSearchPage />} />
            <Route path="/v1/rendering" element={<FlowRenderingPage />} />
            <Route path="/v1/flows/:flowId" element={<CurrentFlowRoute />} />
            <Route path="/v1/flows/:flowId/:runId" element={<FlowRunRoute />} />
            <Route path="*" element={<Navigate to="/" replace />} />
          </Routes>
        </main>
      </WebCatalogProvider>
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
