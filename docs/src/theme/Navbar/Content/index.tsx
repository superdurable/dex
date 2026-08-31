import React, {type ReactNode} from 'react';
import {useNavbarMobileSidebar} from '@docusaurus/theme-common/internal';
import NavbarColorModeToggle from '@theme/Navbar/ColorModeToggle';
import NavbarMobileSidebarToggle from '@theme/Navbar/MobileSidebar/Toggle';
import NavbarLogo from '@theme/Navbar/Logo';
import LocaleSwitcher from '@site/src/components/LocaleSwitcher';
import {BOOKING_URL, GITHUB_URL} from '@site/src/components/brandNavigation';

export default function NavbarContent(): ReactNode {
  const mobileSidebar = useNavbarMobileSidebar();

  return (
    <div className="navbar__inner brand-navbar-inner">
      <div className="brand-navbar-start">
        <NavbarLogo />
      </div>
      <nav className="desktop-nav" aria-label="Primary navigation">
        <a href="https://superdurable.io/dex">Dex</a>
        <a href={GITHUB_URL} target="_blank" rel="noreferrer">
          GitHub <span className="external-arrow" aria-hidden="true">↗</span>
        </a>
      </nav>
      <div className="header-actions">
        <LocaleSwitcher />
        <NavbarColorModeToggle />
        <a className="button button-small header-cta" href={BOOKING_URL} target="_blank" rel="noreferrer">
          Book a call <span aria-hidden="true">↗</span>
        </a>
      </div>
      {!mobileSidebar.disabled && (
        <div className="mobile-navbar-toggle">
          <NavbarMobileSidebarToggle />
        </div>
      )}
    </div>
  );
}
