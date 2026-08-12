import React, {type ReactNode, useEffect, useRef} from 'react';
import Link from '@docusaurus/Link';
import {useLocation} from '@docusaurus/router';

export default function ProductBar(): ReactNode {
  const detailsRef = useRef<HTMLDetailsElement>(null);
  const location = useLocation();
  const isCloud = location.pathname === '/cloud' || location.pathname.startsWith('/cloud/');

  useEffect(() => {
    function close(event: MouseEvent | FocusEvent) {
      if (!detailsRef.current?.contains(event.target as Node)) detailsRef.current?.removeAttribute('open');
    }
    function closeWithEscape(event: KeyboardEvent) {
      if (event.key === 'Escape') {
        detailsRef.current?.removeAttribute('open');
        detailsRef.current?.querySelector('summary')?.focus();
      }
    }
    document.addEventListener('mousedown', close);
    document.addEventListener('focusin', close);
    document.addEventListener('keydown', closeWithEscape);
    return () => {
      document.removeEventListener('mousedown', close);
      document.removeEventListener('focusin', close);
      document.removeEventListener('keydown', closeWithEscape);
    };
  }, []);

  return (
    <div className="product-bar" aria-label="Documentation products">
      <div className="product-bar-inner">
        <span className="product-bar-label">Documentation</span>
        <details className="product-switcher" ref={detailsRef}>
          <summary>
            {isCloud ? 'Dex Cloud / BYOC' : 'Dex OSS'} <span aria-hidden="true">⌄</span>
          </summary>
          <div className="product-switcher-menu">
            <Link className={!isCloud ? 'active' : undefined} to="/" onClick={() => detailsRef.current?.removeAttribute('open')}>
              Dex OSS
            </Link>
            <Link className={isCloud ? 'active' : undefined} to="/cloud" onClick={() => detailsRef.current?.removeAttribute('open')}>
              Dex Cloud / BYOC
            </Link>
          </div>
        </details>
      </div>
    </div>
  );
}
