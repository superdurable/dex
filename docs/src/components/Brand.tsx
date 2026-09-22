import React, {type ReactNode} from 'react';
import DexMark from '@site/src/components/DexMark';

/**
 * Dex leads; Super Durable is the maker credit.
 *
 * Two lines rather than one, because the product being branded is Dex and the
 * single line put the company first and the product nowhere. Stacking is what
 * lets Dex be set larger than the company without the lockup growing wider than
 * the mark is tall.
 */
export default function Brand(): ReactNode {
  return (
    <>
      <span className="brand-symbol" aria-hidden="true">
        <DexMark size={30} />
      </span>
      <span className="wordmark-text">
        <b>Dex</b>
        <span>Super Durable</span>
      </span>
    </>
  );
}
