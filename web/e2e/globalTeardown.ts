import { execa } from 'execa';
import { deleteState, readState } from './state';

const TEARDOWN_STACK = process.env.E2E_TEARDOWN_STACK === '1';

export default async function globalTeardown(): Promise<void> {
  let state;
  try {
    state = readState();
  } catch {
    return;
  }

  if (TEARDOWN_STACK) {
    console.log(
      `[globalTeardown] docker compose ${state.composeArgs.join(' ')} -p ${state.composeProject} down -v`,
    );
    try {
      await execa(
        'docker',
        ['compose', ...state.composeArgs, '-p', state.composeProject, 'down', '-v'],
        { stdio: 'inherit', timeout: 60_000 },
      );
    } catch (err) {
      console.warn('[globalTeardown] docker compose down failed:', err);
    }
  } else {
    console.log(
      '[globalTeardown] leaving stack up (set E2E_TEARDOWN_STACK=1 to run docker compose down -v)',
    );
  }

  deleteState();
}
