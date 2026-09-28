// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { useEffect, useRef, useState } from 'react';
import type { IScannerControls } from '@zxing/browser';
import { qrScannerFailureMessage, startQRCodeScanner, stopQRCodeScanner } from './qrScanner';

export function QRCodeScannerDialog({ fieldDescription, onCancel, onScan }: {
  fieldDescription: string;
  onCancel: () => void;
  onScan: (value: string) => void;
}) {
  const dialogRef = useRef<HTMLDialogElement>(null);
  const previewRef = useRef<HTMLVideoElement>(null);
  const controlsRef = useRef<IScannerControls | undefined>(undefined);
  const onScanRef = useRef(onScan);
  const [status, setStatus] = useState('Starting camera…');
  const [error, setError] = useState('');
  onScanRef.current = onScan;

  useEffect(() => {
    if (dialogRef.current && !dialogRef.current.open) dialogRef.current.showModal();
    dialogRef.current?.querySelector<HTMLButtonElement>('[data-qr-close]')?.focus();
  }, []);

  useEffect(() => {
    const preview = previewRef.current;
    if (!preview) return undefined;
    let isCurrent = true;
    void startQRCodeScanner(preview, (value) => {
      if (isCurrent) onScanRef.current(value);
    }).then((controls) => {
      if (!isCurrent) {
        stopQRCodeScanner(controls, preview);
        return;
      }
      controlsRef.current = controls;
      setStatus('Point the camera at a QR code.');
    }).catch((scannerError: unknown) => {
      if (isCurrent) setError(qrScannerFailureMessage(scannerError));
    });
    return () => {
      isCurrent = false;
      stopQRCodeScanner(controlsRef.current, preview);
      controlsRef.current = undefined;
    };
  }, []);

  return (
    <dialog
      aria-labelledby="qr-scanner-title"
      className="qr-scanner-dialog"
      ref={dialogRef}
      onCancel={(event) => {
        event.preventDefault();
        onCancel();
      }}
    >
      <header>
        <div>
          <h2 id="qr-scanner-title">Scan QR code</h2>
          <p>{fieldDescription}</p>
        </div>
        <button aria-label="Close QR scanner" data-qr-close onClick={onCancel} type="button">×</button>
      </header>
      <video aria-label="QR camera preview" autoPlay muted playsInline ref={previewRef} />
      {error ? <p className="v2-error" role="alert">{error}</p> : <p role="status">{status}</p>}
      <footer>
        <button className="v2-ghost" onClick={onCancel} type="button">
          {error ? 'Enter manually' : 'Cancel'}
        </button>
      </footer>
    </dialog>
  );
}
