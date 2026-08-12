import React, {type ReactNode} from 'react';
import Brand from '@site/src/components/Brand';

export default function NavbarLogo(): ReactNode {
  return (
    <a className="navbar__brand" href="https://superdurable.io/" aria-label="Super Durable home">
      <Brand />
    </a>
  );
}
