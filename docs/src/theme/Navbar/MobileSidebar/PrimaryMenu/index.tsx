import React, {type ReactNode} from 'react';
import {useThemeConfig} from '@docusaurus/theme-common';
import {useNavbarMobileSidebar} from '@docusaurus/theme-common/internal';
import NavbarItem, {type Props as NavbarItemConfig} from '@theme/NavbarItem';
import GitHubStarNavbarItem from '@site/src/components/GitHubStarNavbarItem';

export default function NavbarMobilePrimaryMenu(): ReactNode {
  const mobileSidebar = useNavbarMobileSidebar();
  const items = useThemeConfig().navbar.items as NavbarItemConfig[];
  const booking = items.filter((item) => item.className === 'header-booking-link');
  const primary = items.filter((item) => item.className !== 'header-booking-link');
  const close = () => mobileSidebar.toggle();

  return (
    <ul className="menu__list">
      {primary.map((item, index) => (
        <NavbarItem mobile {...item} onClick={close} key={index} />
      ))}
      <GitHubStarNavbarItem mobile onClick={close} />
      {booking.map((item, index) => (
        <NavbarItem mobile {...item} onClick={close} key={`booking-${index}`} />
      ))}
    </ul>
  );
}
