import React, {type ReactNode, useEffect, useState} from 'react';
import clsx from 'clsx';

const REPOSITORY_URL = 'https://github.com/superdurable/dex';
const REPOSITORY_API = 'https://api.github.com/repos/superdurable/dex';
let cachedStars: number | null = null;
let starRequest: Promise<number | null> | null = null;

function loadStars() {
  if (cachedStars !== null) return Promise.resolve(cachedStars);
  if (!starRequest) {
    starRequest = fetch(REPOSITORY_API, {headers: {Accept: 'application/vnd.github+json'}})
      .then(async (response) => {
        if (!response.ok) return null;
        const data = (await response.json()) as {stargazers_count?: number};
        return typeof data.stargazers_count === 'number' ? data.stargazers_count : null;
      })
      .catch(() => null);
  }
  return starRequest.then((stars) => {
    if (stars !== null) cachedStars = stars;
    return stars;
  });
}

type Props = {
  mobile?: boolean;
  className?: string;
  onClick?: () => void;
};

export default function GitHubStarNavbarItem({
  mobile = false,
  className,
  onClick,
}: Props): ReactNode {
  const [stars, setStars] = useState<string>(() =>
    cachedStars === null ? '–' : new Intl.NumberFormat('en', {notation: 'compact'}).format(cachedStars),
  );

  useEffect(() => {
    let active = true;
    void loadStars().then((count) => {
      if (active && count !== null) {
        setStars(new Intl.NumberFormat('en', {notation: 'compact'}).format(count));
      }
    });
    return () => {
      active = false;
    };
  }, []);

  const link = (
    <a
      className={clsx(mobile ? 'menu__link' : 'navbar__link', 'github-star-link', className)}
      href={REPOSITORY_URL}
      target="_blank"
      rel="noreferrer"
      onClick={onClick}
      aria-label={`Star superdurable/dex on GitHub. ${stars} stars`}>
      <span>Star Us</span>
      <span className="github-star-stat" aria-hidden="true">
        <span>★</span>
        <span>Star</span>
        <strong>{stars}</strong>
      </span>
    </a>
  );

  return mobile ? <li className="menu__list-item">{link}</li> : link;
}
