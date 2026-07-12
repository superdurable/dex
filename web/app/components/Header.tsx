'use client';

import Link from 'next/link';
import { useRouter, useSearchParams, usePathname } from 'next/navigation';
import { useEffect, useState } from 'react';

interface HeaderProps {
  title?: string;
}

// Header renders a top bar with a Namespace input on the top-left. The input
// keeps the URL query in sync (?namespace=...), which is read by the List and
// Show pages. Show page links that need to preserve the current namespace
// should use the `?namespace=` query param.
export default function Header({ title = 'DEX Ops' }: HeaderProps) {
  const router = useRouter();
  const pathname = usePathname();
  const search = useSearchParams();
  const urlNs = search.get('namespace') ?? '';
  const [ns, setNs] = useState(urlNs);

  useEffect(() => {
    setNs(urlNs);
  }, [urlNs]);

  const apply = (value: string) => {
    const params = new URLSearchParams(search.toString());
    if (value) params.set('namespace', value);
    else params.delete('namespace');
    router.replace(`${pathname}?${params.toString()}`);
  };

  return (
    <div className="bg-white border-b border-gray-200">
      <div className="max-w-[95%] 2xl:max-w-[90%] mx-auto py-3 px-4 flex items-center gap-4">
        <div className="flex items-center gap-2">
          <label htmlFor="namespace-input" className="text-sm font-medium text-gray-700">
            Namespace
          </label>
          <input
            id="namespace-input"
            data-testid="namespace-input"
            type="text"
            value={ns}
            onChange={(e) => setNs(e.target.value)}
            onBlur={(e) => apply(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter') apply((e.target as HTMLInputElement).value);
            }}
            placeholder="default"
            className="border border-gray-300 rounded px-2 py-1 text-sm w-48 focus:outline-none focus:ring-2 focus:ring-blue-400"
          />
        </div>
        <div className="flex-1" />
        <Link
          href={`/${urlNs ? `?namespace=${encodeURIComponent(urlNs)}` : ''}`}
          className="text-sm text-blue-600 hover:text-blue-800 hover:underline"
        >
          Runs
        </Link>
        <h1 className="text-base font-semibold text-gray-800">{title}</h1>
      </div>
    </div>
  );
}
