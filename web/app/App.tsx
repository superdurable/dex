// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { Navigate, Route, Routes, useLocation, useParams } from 'react-router-dom';
import { AppFrame } from './components/frame/AppBar';
import { AppBarSlotsProvider } from './components/frame/AppBarSlots';
import { CurrentRunRedirect } from './flows/CurrentRunRedirect';
import { FlowSearchPage } from './flows/FlowSearchPage';
import { RunDetailsPage } from './flows/RunDetailsPage';
import { PreferencesProvider } from './providers';
import { FlowRenderingPage } from './rendering/FlowRenderingPage';
import { ThemeProvider } from './theme';
import { WorkQueueWorkspace } from './work-queue/WorkQueueWorkspace';
import { WebCatalogProvider } from './catalog/WebCatalogProvider';
import { ConnectorsPage } from './connectors/ConnectorsPage';
import { CONNECTORS_PATH, FLOWS_PATH, legacyV2RedirectPath, WORK_QUEUE_PATH } from './routes';

export function App() {
  return (
    <PreferencesProvider>
      <ThemeProvider>
        <WebCatalogProvider>
          <AppBarSlotsProvider>
            <AppFrame>
              <AppRoutes />
            </AppFrame>
          </AppBarSlotsProvider>
        </WebCatalogProvider>
      </ThemeProvider>
    </PreferencesProvider>
  );
}

export function AppRoutes() {
  return (
    <Routes>
      <Route path="/" element={<Navigate to={FLOWS_PATH} replace />} />
      <Route path="/v1" element={<Navigate to={FLOWS_PATH} replace />} />
      <Route path={FLOWS_PATH} element={<FlowSearchPage />} />
      <Route path="/v1/rendering" element={<FlowRenderingPage />} />
      <Route path="/v1/flows/:flowId" element={<CurrentFlowRoute />} />
      <Route path="/v1/flows/:flowId/:runId" element={<FlowRunRoute />} />
      <Route path={WORK_QUEUE_PATH} element={<WorkQueueWorkspace />} />
      <Route path={`${WORK_QUEUE_PATH}/:flowType`} element={<WorkQueueWorkspace />} />
      <Route path={`${WORK_QUEUE_PATH}/:flowType/:flowId`} element={<WorkQueueWorkspace />} />
      <Route path={`${CONNECTORS_PATH}/*`} element={<ConnectorsPage />} />
      <Route path="/v2/*" element={<LegacyV2Redirect />} />
      <Route path="/v2" element={<LegacyV2Redirect />} />
      <Route path="*" element={<Navigate to={FLOWS_PATH} replace />} />
    </Routes>
  );
}

/** Old Dex Web v2 bookmarks land on the page that now does the same job. */
function LegacyV2Redirect() {
  return <Navigate to={legacyV2RedirectPath(useLocation())} replace />;
}

function CurrentFlowRoute() {
  const { flowId = '' } = useParams();
  return <CurrentRunRedirect flowId={flowId} />;
}

function FlowRunRoute() {
  const { flowId = '', runId = '' } = useParams();
  const flowPath = `/v1/flows/${encodeURIComponent(flowId)}`;
  return (
    // Keyed by Flow ID, so each Flow opens fresh on its own remembered tab.
    <RunDetailsPage
      key={flowId}
      flowId={flowId}
      runId={runId}
      runPath={(chainRunID) => `${flowPath}/${encodeURIComponent(chainRunID)}`}
    />
  );
}
