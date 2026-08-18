export type SdkLanguage = 'python' | 'go' | 'java' | 'typescript';

export const SDK_LANGUAGES: SdkLanguage[] = ['python', 'go', 'java', 'typescript'];
export const DEFAULT_SDK: SdkLanguage = 'python';
export const SDK_PREFERENCE_KEY = 'dex-docs-preferred-sdk';

const SDK_PREFERENCE_EVENT = 'dex-docs-preferred-sdk';

export function isSdkLanguage(value: string | null | undefined): value is SdkLanguage {
  return value === 'python' || value === 'go' || value === 'java' || value === 'typescript';
}

export function readSdkPreference(): SdkLanguage {
  if (typeof window === 'undefined') {
    return DEFAULT_SDK;
  }
  const stored = window.localStorage.getItem(SDK_PREFERENCE_KEY);
  return isSdkLanguage(stored) ? stored : DEFAULT_SDK;
}

export function persistSdkPreference(language: SdkLanguage): void {
  window.localStorage.setItem(SDK_PREFERENCE_KEY, language);
  window.dispatchEvent(new CustomEvent(SDK_PREFERENCE_EVENT, {detail: language}));
}

export function subscribeSdkPreference(listener: (language: SdkLanguage) => void): () => void {
  const onCustom = (event: Event) => {
    const language = (event as CustomEvent<SdkLanguage>).detail;
    if (isSdkLanguage(language)) {
      listener(language);
    }
  };
  const onStorage = (event: StorageEvent) => {
    if (event.key !== SDK_PREFERENCE_KEY || !isSdkLanguage(event.newValue)) {
      return;
    }
    listener(event.newValue);
  };
  window.addEventListener(SDK_PREFERENCE_EVENT, onCustom);
  window.addEventListener('storage', onStorage);
  return () => {
    window.removeEventListener(SDK_PREFERENCE_EVENT, onCustom);
    window.removeEventListener('storage', onStorage);
  };
}
