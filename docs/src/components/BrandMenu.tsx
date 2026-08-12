import React, {type ReactNode, useEffect, useRef} from 'react';

export type BrandMenuItem = {
  href: string;
  label: string;
  description: string;
  external?: boolean;
};

export default function BrandMenu({
  label,
  items,
  mobile = false,
  onNavigate,
}: {
  label: string;
  items: BrandMenuItem[];
  mobile?: boolean;
  onNavigate?: () => void;
}): ReactNode {
  const detailsRef = useRef<HTMLDetailsElement>(null);

  useEffect(() => {
    const close = () => detailsRef.current?.removeAttribute('open');
    const closeOutside = (event: PointerEvent | FocusEvent) => {
      if (detailsRef.current?.open && !detailsRef.current.contains(event.target as Node)) close();
    };
    const closeWithEscape = (event: KeyboardEvent) => {
      if (event.key !== 'Escape' || !detailsRef.current?.open) return;
      close();
      detailsRef.current.querySelector<HTMLElement>('summary')?.focus();
    };
    document.addEventListener('pointerdown', closeOutside);
    document.addEventListener('focusin', closeOutside);
    document.addEventListener('keydown', closeWithEscape);
    return () => {
      document.removeEventListener('pointerdown', closeOutside);
      document.removeEventListener('focusin', closeOutside);
      document.removeEventListener('keydown', closeWithEscape);
    };
  }, []);

  return (
    <details className={mobile ? 'mobile-services-menu' : 'services-menu'} ref={detailsRef}>
      <summary>
        {label} <span aria-hidden="true">⌄</span>
      </summary>
      <div className={mobile ? undefined : 'services-popover'}>
        {items.map((item) => (
          <a
            href={item.href}
            key={item.href}
            target={item.external ? '_blank' : undefined}
            rel={item.external ? 'noreferrer' : undefined}
            onClick={() => {
              detailsRef.current?.removeAttribute('open');
              onNavigate?.();
            }}>
            {mobile ? (
              item.label
            ) : (
              <>
                <strong>{item.label}</strong>
                <span>{item.description}</span>
              </>
            )}
          </a>
        ))}
      </div>
    </details>
  );
}
