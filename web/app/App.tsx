import { Navigate, Route, Routes, useParams } from 'react-router-dom';
import { AppHeader } from './components/AppHeader';
import { CurrentRunRedirect } from './flows/CurrentRunRedirect';
import { FlowSearchPage } from './flows/FlowSearchPage';
import { RunDetailsPage } from './flows/RunDetailsPage';
import { PreferencesProvider } from './providers';

export function App() {
  return (
    <PreferencesProvider>
      <AppHeader />
      <main className="app-main">
        <Routes>
          <Route path="/" element={<FlowSearchPage />} />
          <Route path="/flows/:flowId" element={<CurrentFlowRoute />} />
          <Route path="/flows/:flowId/:runId" element={<FlowRunRoute />} />
          <Route path="*" element={<Navigate to="/" replace />} />
        </Routes>
      </main>
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
