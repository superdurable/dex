import React, {
  Children,
  isValidElement,
  ReactElement,
  ReactNode,
  useEffect,
  useMemo,
  useState,
} from 'react';

export type SdkLanguage = 'python' | 'go' | 'java' | 'typescript';

const STORAGE_KEY = 'dex-docs-preferred-sdk';
const DEFAULT_SDK: SdkLanguage = 'python';

const LABELS: Record<SdkLanguage, string> = {
  python: 'Python',
  go: 'Go',
  java: 'Java',
  typescript: 'TypeScript',
};

const ORDER: SdkLanguage[] = ['python', 'go', 'java', 'typescript'];

export type SdkSnippetProps = {
  lang: SdkLanguage;
  children: ReactNode;
};

export function SdkSnippet({children}: SdkSnippetProps): ReactNode {
  return <>{children}</>;
}

export type SdkTabsProps = {
  children: ReactNode;
  python?: ReactNode;
  go?: ReactNode;
  java?: ReactNode;
  typescript?: ReactNode;
};

function readPreference(): SdkLanguage {
  if (typeof window === 'undefined') {
    return DEFAULT_SDK;
  }
  const stored = window.localStorage.getItem(STORAGE_KEY);
  if (stored === 'python' || stored === 'go' || stored === 'java' || stored === 'typescript') {
    return stored;
  }
  return DEFAULT_SDK;
}

function collectSnippets(props: SdkTabsProps): Partial<Record<SdkLanguage, ReactNode>> {
  const snippets: Partial<Record<SdkLanguage, ReactNode>> = {};
  for (const language of ORDER) {
    if (props[language] != null) {
      snippets[language] = props[language];
    }
  }
  Children.forEach(props.children, (child) => {
    if (!isValidElement(child)) {
      return;
    }
    const element = child as ReactElement<SdkSnippetProps>;
    const lang = element.props.lang;
    if (lang === 'python' || lang === 'go' || lang === 'java' || lang === 'typescript') {
      snippets[lang] = element.props.children;
    }
  });
  return snippets;
}

export default function SdkTabs(props: SdkTabsProps): ReactNode {
  const snippets = useMemo(() => collectSnippets(props), [props]);
  const available = ORDER.filter((language) => snippets[language] != null);
  const [active, setActive] = useState<SdkLanguage>(DEFAULT_SDK);

  useEffect(() => {
    const preferred = readPreference();
    setActive(available.includes(preferred) ? preferred : available[0] ?? DEFAULT_SDK);
  }, [available.join(',')]);

  const select = (language: SdkLanguage) => {
    setActive(language);
    window.localStorage.setItem(STORAGE_KEY, language);
  };

  if (available.length === 0) {
    return null;
  }

  const current = available.includes(active) ? active : available[0];

  return (
    <div className="sdk-tabs">
      <div className="sdk-tabs__bar" role="tablist" aria-label="SDK language">
        {available.map((language) => (
          <button
            key={language}
            type="button"
            role="tab"
            aria-selected={language === current}
            className={
              language === current
                ? 'sdk-tabs__button sdk-tabs__button--active'
                : 'sdk-tabs__button'
            }
            onClick={() => select(language)}>
            {LABELS[language]}
          </button>
        ))}
      </div>
      <div className="sdk-tabs__panel" role="tabpanel">
        {snippets[current]}
      </div>
    </div>
  );
}
