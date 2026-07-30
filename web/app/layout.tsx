import type { Metadata } from 'next';
import './globals.css';
import '@xyflow/react/dist/style.css';
import { PreferencesProvider } from './providers';
import { AppHeader } from './components/AppHeader';

export const metadata: Metadata = {
  title: 'Dex Web',
  description: 'Inspect and operate Dex flows',
};

export default function RootLayout({ children }: Readonly<{ children: React.ReactNode }>) {
  return (
    <html lang="en">
      <body>
        <PreferencesProvider>
          <AppHeader />
          <main className="app-main">{children}</main>
        </PreferencesProvider>
      </body>
    </html>
  );
}
