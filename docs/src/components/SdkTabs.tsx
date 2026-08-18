import React, {
  Children,
  isValidElement,
  ReactElement,
  ReactNode,
  useEffect,
  useMemo,
  useState,
} from 'react';
import useDocusaurusContext from '@docusaurus/useDocusaurusContext';

const EXAMPLE_BASE = 'https://github.com/superdurable/dex/tree/main';

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
  example?: string;
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
  examples?: Partial<Record<SdkLanguage, string>>;
};

type SnippetRecord = {
  body: ReactNode;
  example?: string;
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

function collectSnippets(props: SdkTabsProps): Partial<Record<SdkLanguage, SnippetRecord>> {
  const snippets: Partial<Record<SdkLanguage, SnippetRecord>> = {};
  for (const language of ORDER) {
    if (props[language] != null) {
      snippets[language] = {body: props[language], example: props.examples?.[language]};
    }
  }
  Children.forEach(props.children, (child) => {
    if (!isValidElement(child)) {
      return;
    }
    const element = child as ReactElement<SdkSnippetProps>;
    const lang = element.props.lang;
    if (lang === 'python' || lang === 'go' || lang === 'java' || lang === 'typescript') {
      snippets[lang] = {
        body: element.props.children,
        example: element.props.example ?? props.examples?.[lang],
      };
    }
  });
  return snippets;
}

export default function SdkTabs(props: SdkTabsProps): ReactNode {
  const {i18n} = useDocusaurusContext();
  const snippets = useMemo(() => collectSnippets(props), [props]);
  const available = ORDER.filter((language) => snippets[language] != null);
  const [active, setActive] = useState<SdkLanguage>(DEFAULT_SDK);
  const exampleLabel = i18n.currentLocale === 'zh-Hans' ? '例子' : 'Example';

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
  const example = snippets[current]?.example;

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
        {snippets[current]?.body}
        {example ? (
          <p className="sdk-tabs__example">
            {exampleLabel}:{' '}
            <a href={`${EXAMPLE_BASE}/${example}`}>{example}</a>
          </p>
        ) : null}
      </div>
    </div>
  );
}
