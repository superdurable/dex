// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import React, {type ReactNode, useState} from 'react';
import {translate} from '@docusaurus/Translate';
import useBaseUrl from '@docusaurus/useBaseUrl';
import styles from './YouTubeVideo.module.css';

export type YouTubeVideoProps = {
  /** The YouTube video ID, such as the part after youtu.be/. */
  videoId: string;
  /** The video title. It names the play button and the player frame. */
  title: string;
  /** A self-hosted poster image path under static/, such as /img/videos/name.jpg. */
  poster: string;
};

// The player loads only after a click, so a reader who does not play the video sends no request to YouTube.
export default function YouTubeVideo({videoId, title, poster}: YouTubeVideoProps): ReactNode {
  const [isPlaying, setIsPlaying] = useState(false);
  const posterUrl = useBaseUrl(poster);
  const watchUrl = `https://youtu.be/${encodeURIComponent(videoId)}`;
  const playerUrl = `https://www.youtube-nocookie.com/embed/${encodeURIComponent(videoId)}?autoplay=1`;
  const playLabel = translate(
    {
      id: 'docs.video.play',
      message: 'Play video: {title}',
      description: 'The accessible name of the button that loads and plays an embedded video',
    },
    {title},
  );
  const watchLabel = translate({
    id: 'docs.video.watchOnYouTube',
    message: 'Watch on YouTube',
    description: 'The text link under an embedded video that opens it on YouTube',
  });
  return (
    <figure className={styles.video}>
      <div className={styles.frame}>
        {isPlaying ? (
          <iframe
            className={styles.player}
            src={playerUrl}
            title={title}
            allow="autoplay; encrypted-media; picture-in-picture; fullscreen"
            allowFullScreen
            referrerPolicy="strict-origin-when-cross-origin"
          />
        ) : (
          <button type="button" className={styles.poster} onClick={() => setIsPlaying(true)} aria-label={playLabel}>
            <img src={posterUrl} alt="" width={1280} height={720} loading="lazy" decoding="async" />
            <span className={styles.playIcon} aria-hidden="true">
              <svg viewBox="0 0 68 48" width="68" height="48">
                <rect width="68" height="48" rx="12" />
                <path d="M27 15v18l16-9z" />
              </svg>
            </span>
          </button>
        )}
      </div>
      <figcaption className={styles.caption}>
        <a href={watchUrl} target="_blank" rel="noopener noreferrer">
          {watchLabel}
        </a>
        {': '}
        {title}
      </figcaption>
    </figure>
  );
}
