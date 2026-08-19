import React, {type ReactNode} from 'react';
import {useNavbarMobileSidebar} from '@docusaurus/theme-common/internal';
import BrandMenu from '@site/src/components/BrandMenu';
import GitHubStarNavbarItem from '@site/src/components/GitHubStarNavbarItem';
import LocaleSwitcher from '@site/src/components/LocaleSwitcher';
import {BOOKING_URL, DOC_ITEMS, SERVICE_ITEMS} from '@site/src/components/brandNavigation';

export default function NavbarMobilePrimaryMenu(): ReactNode {
  const mobileSidebar = useNavbarMobileSidebar();
  const close = () => mobileSidebar.toggle();

  return (
    <ul className="menu__list brand-mobile-nav">
      <li className="menu__list-item"><a className="menu__link" href="https://superdurable.io/dex" onClick={close}>Dex</a></li>
      <li className="menu__list-item"><BrandMenu label="Docs" items={DOC_ITEMS} mobile onNavigate={close} /></li>
      <li className="menu__list-item"><BrandMenu label="Services" items={SERVICE_ITEMS} mobile onNavigate={close} /></li>
      <GitHubStarNavbarItem mobile onClick={close} />
      <li className="menu__list-item locale-switcher-mobile">
        <LocaleSwitcher />
      </li>
      <li className="menu__list-item">
        <a className="button header-booking-link" href={BOOKING_URL} target="_blank" rel="noreferrer" onClick={close}>Book a call <span aria-hidden="true">↗</span></a>
      </li>
    </ul>
  );
}
