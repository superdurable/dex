'use client';

import { createContext } from 'react';

// Section identifies which sub-area of a step node the user wants the side
// panel to focus on. `null` means the overview (live wait condition + all
// linked events). `'wait'` filters to WaitFor / WAITING_FOR_CONDITION
// information only; `'execute'` filters to StepExecuteCompleted (and any
// StepsUnblocked piggybacked on it).
export type Section = 'wait' | 'execute' | null;

export interface SectionSelection {
  stepExeId: string;
  section: Section;
}

// Context shared by WorkflowGraph and StepNode so each sub-section in the
// node body can drive the side-panel filter. Default is a no-op so the
// node still renders under static-render / test contexts.
export const SectionSelectionContext = createContext<{
  selection: SectionSelection | null;
  setSelection: (s: SectionSelection | null) => void;
}>({
  selection: null,
  setSelection: () => {},
});
