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
import { ThemeProvider } from './theme';
import { QueueWorkspace } from './v2/queue/QueueWorkspace';
import { HomePage, RunWorkspace } from './v2/RunWorkspace';
import { WebCatalogProvider } from './v2/WebCatalogProvider';

export function App() {
  return (
    <PreferencesProvider>
      <ThemeProvider>
        <WebCatalogProvider>
          <AppHeader />
          <main className="app-main">
            <Routes>
              <Route path="/" element={<HomePage />} />
              <Route path="/v2" element={<Navigate to="/v2/run" replace />} />
              <Route path="/v2/run" element={<RunWorkspace />} />
              <Route path="/v2/run/:flowType" element={<RunWorkspace />} />
              <Route path="/v2/run/:flowType/:flowId" element={<RunWorkspace />} />
              <Route path="/v2/queue" element={<QueueWorkspace />} />
              <Route path="/v2/queue/:flowType" element={<QueueWorkspace />} />
              <Route path="/v2/queue/:flowType/:flowId" element={<QueueWorkspace />} />
              <Route path="/v1" element={<Navigate to="/v1/flows" replace />} />
              <Route path="/v1/flows" element={<FlowSearchPage />} />
              <Route path="/v1/rendering" element={<FlowRenderingPage />} />
              <Route path="/v1/flows/:flowId" element={<CurrentFlowRoute />} />
              <Route path="/v1/flows/:flowId/:runId" element={<FlowRunRoute />} />
              <Route path="*" element={<Navigate to="/" replace />} />
            </Routes>
          </main>
        </WebCatalogProvider>
      </ThemeProvider>
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
