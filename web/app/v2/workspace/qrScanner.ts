// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { IScannerControls } from '@zxing/browser';

interface QRCodeReader {
  decodeFromConstraints(
    constraints: MediaStreamConstraints,
    preview: HTMLVideoElement,
    callback: (result: { getText(): string } | undefined, error: unknown, controls: IScannerControls) => void,
  ): Promise<IScannerControls>;
}

export interface QRScannerEnvironment {
  isSecureContext: boolean;
  mediaDevices?: MediaDevices;
  loadReader: () => Promise<QRCodeReader>;
}

export async function startQRCodeScanner(
  preview: HTMLVideoElement,
  onResult: (value: string) => void,
  environment: QRScannerEnvironment = browserQRScannerEnvironment(),
): Promise<IScannerControls> {
  if (!environment.isSecureContext) {
    throw new Error('QR camera capture requires HTTPS or localhost.');
  }
  if (!environment.mediaDevices?.getUserMedia) {
    throw new Error('This browser does not provide camera access.');
  }
  try {
    const reader = await environment.loadReader();
    return await reader.decodeFromConstraints(
      { audio: false, video: { facingMode: { ideal: 'environment' } } },
      preview,
      (result, _error, controls) => {
        if (!result) return;
        stopQRCodeScanner(controls, preview);
        onResult(result.getText());
      },
    );
  } catch (error) {
    stopQRCodeScanner(undefined, preview);
    throw error;
  }
}

export function stopQRCodeScanner(
  controls: IScannerControls | undefined,
  preview: HTMLVideoElement | null,
): void {
  try {
    controls?.stop();
  } catch (error) {
    console.error('Failed to stop QR scanner controls.', error);
  }
  if (!preview) return;
  const stream = preview.srcObject;
  if (typeof MediaStream === 'undefined' || !(stream instanceof MediaStream)) return;
  for (const track of stream.getTracks()) track.stop();
  preview.srcObject = null;
}

export function qrScannerFailureMessage(error: unknown): string {
  const errorName = typeof error === 'object' && error !== null && 'name' in error
    ? String(error.name)
    : '';
  if (errorName === 'NotAllowedError' || errorName === 'SecurityError') {
    return 'Camera access was denied. Allow camera access or enter the value manually.';
  }
  if (errorName === 'NotFoundError' || errorName === 'DevicesNotFoundError') {
    return 'No camera was found. Enter the value manually.';
  }
  if (error instanceof Error && error.message !== '') return error.message;
  return 'The QR scanner could not start. Enter the value manually.';
}

function browserQRScannerEnvironment(): QRScannerEnvironment {
  return {
    isSecureContext: window.isSecureContext,
    mediaDevices: navigator.mediaDevices,
    loadReader: async () => {
      const { BrowserQRCodeReader } = await import('@zxing/browser');
      return new BrowserQRCodeReader();
    },
  };
}
