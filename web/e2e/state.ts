import fs from 'node:fs';
import path from 'node:path';

export interface E2eState {
  // Compose -f args (base + backend override), e.g. ["-f","…e2e.yaml","-f","…postgres.yaml"].
  composeArgs: string[];
  composeProject: string;
  runIds: string[];
  completedRunId: string | null;
  namespace: string;
  flowType: string;
  opsAddress: string;
  benchPort: number;
  startedAt: string;
}

export const STATE_PATH = path.resolve(__dirname, '.state.json');

export function readState(): E2eState {
  if (!fs.existsSync(STATE_PATH)) {
    throw new Error(`Missing ${STATE_PATH}; did globalSetup run?`);
  }
  return JSON.parse(fs.readFileSync(STATE_PATH, 'utf8')) as E2eState;
}

export function writeState(s: E2eState): void {
  fs.mkdirSync(path.dirname(STATE_PATH), { recursive: true });
  fs.writeFileSync(STATE_PATH, JSON.stringify(s, null, 2));
}

export function deleteState(): void {
  if (fs.existsSync(STATE_PATH)) fs.unlinkSync(STATE_PATH);
}
