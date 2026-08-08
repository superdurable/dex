import React, {ReactNode} from 'react';

export type ScreenshotPlaceholderProps = {
  label?: string;
  caption?: string;
};

export default function ScreenshotPlaceholder({
  label = 'TODO: upload Dex Web screenshot',
  caption,
}: ScreenshotPlaceholderProps): ReactNode {
  return (
    <div className="screenshot-placeholder">
      <strong>{label}</strong>
      {caption ? <p>{caption}</p> : null}
    </div>
  );
}
