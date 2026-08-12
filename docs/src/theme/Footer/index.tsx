import React, {type ReactNode} from 'react';
import Brand from '@site/src/components/Brand';

export default function Footer(): ReactNode {
  return (
    <footer className="site-footer">
      <div className="footer-brand">
        <a className="wordmark" href="https://superdurable.io/" aria-label="Super Durable home"><Brand /></a>
        <p>Simple, reliable and efficient solutions to complex problems.</p>
      </div>
      <div className="footer-links">
        <div>
          <p className="footer-label">Explore</p>
          <a href="https://superdurable.io/dex">Dex</a>
          <a href="/">Docs</a>
          <a href="https://superdurable.io/byoc">Dex BYOC</a>
          <a href="https://superdurable.io/consulting">Consulting</a>
        </div>
        <div>
          <p className="footer-label">Company</p>
          <a href="https://superdurable.io/team">Team</a>
          <a href="https://github.com/superdurable/dex">GitHub</a>
        </div>
      </div>
      <div className="footer-bottom">
        <span>© {new Date().getFullYear()} Super Durable</span>
        <span>Durable Execution, Re-defined.</span>
      </div>
    </footer>
  );
}
