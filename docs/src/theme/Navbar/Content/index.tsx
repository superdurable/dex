import React, {type ReactNode} from 'react';
import {useNavbarMobileSidebar} from '@docusaurus/theme-common/internal';
import NavbarColorModeToggle from '@theme/Navbar/ColorModeToggle';
import NavbarMobileSidebarToggle from '@theme/Navbar/MobileSidebar/Toggle';
import NavbarLogo from '@theme/Navbar/Logo';
import BrandMenu from '@site/src/components/BrandMenu';
import GitHubStarNavbarItem from '@site/src/components/GitHubStarNavbarItem';
import LocaleSwitcher from '@site/src/components/LocaleSwitcher';
import {BOOKING_URL, BYOC_URL, DOC_ITEMS} from '@site/src/components/brandNavigation';

export default function NavbarContent(): ReactNode {
  const mobileSidebar = useNavbarMobileSidebar();

  return (
    <div className="navbar__inner brand-navbar-inner">
      <div className="brand-navbar-start">
        <NavbarLogo />
      </div>
      <nav className="desktop-nav" aria-label="Primary navigation">
        <a href="https://superdurable.io/dex">Dex</a>
        <BrandMenu label="Docs" items={DOC_ITEMS} />
        <a href={BYOC_URL}>Dex BYOC</a>
        <GitHubStarNavbarItem />
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
