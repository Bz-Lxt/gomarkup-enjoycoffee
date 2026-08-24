import { Navigate, Route, Routes } from 'react-router-dom';
import { AppShell } from '@/ui/AppShell';
import { ToastProvider } from '@/ui/Toast';
import { MetaProvider } from '@/lib/MetaContext';
import BeanBoardPage from '@/pages/BeanBoardPage';
import BrewSandboxPage from '@/pages/BrewSandboxPage';
import RadarWallPage from '@/pages/RadarWallPage';
import FlavorTreePage from '@/pages/FlavorTreePage';
import SettingsPage from '@/pages/SettingsPage';

export default function App() {
  return (
    <ToastProvider>
      <MetaProvider>
        <AppShell>
          <Routes>
            <Route path="/" element={<BeanBoardPage />} />
            <Route path="/brew" element={<BrewSandboxPage />} />
            <Route path="/brew/:brewId" element={<BrewSandboxPage />} />
            <Route path="/radar" element={<RadarWallPage />} />
            <Route path="/flavors" element={<FlavorTreePage />} />
            <Route path="/settings" element={<SettingsPage />} />
            <Route path="*" element={<Navigate to="/" replace />} />
          </Routes>
        </AppShell>
      </MetaProvider>
    </ToastProvider>
  );
}
