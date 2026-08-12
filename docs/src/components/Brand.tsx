import React, {type ReactNode} from 'react';
import useBaseUrl from '@docusaurus/useBaseUrl';

export default function Brand(): ReactNode {
  const logo = useBaseUrl('/img/brand/super-durable-logo.png');

  return (
    <>
      <span className="brand-symbol" aria-hidden="true">
        <img src={logo} alt="" />
      </span>
      <span className="wordmark-text">
        <span>SUPER</span> DURABLE
      </span>
    </>
  );
}
