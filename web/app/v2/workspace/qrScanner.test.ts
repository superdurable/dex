// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { IScannerControls } from '@zxing/browser';
import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  qrScannerFailureMessage,
  startQRCodeScanner,
  stopQRCodeScanner,
  type QRScannerEnvironment,
} from './qrScanner';

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('QR scanner', () => {
  it('requires a secure context before loading the reader', async () => {
    const loadReader = vi.fn();
    await expect(startQRCodeScanner(preview(), vi.fn(), environment({
      isSecureContext: false,
      loadReader,
    }))).rejects.toThrow('HTTPS or localhost');
    expect(loadReader).not.toHaveBeenCalled();
  });

  it('reports browsers without camera access before loading the reader', async () => {
    const loadReader = vi.fn();
    await expect(startQRCodeScanner(preview(), vi.fn(), environment({
      mediaDevices: undefined,
      loadReader,
    }))).rejects.toThrow('does not provide camera access');
    expect(loadReader).not.toHaveBeenCalled();
  });

  it('requests the rear camera and returns the decoded text without submitting an Action', async () => {
    const stop = vi.fn();
    const controls: IScannerControls = { stop };
    const onResult = vi.fn();
    const decodeFromConstraints = vi.fn(async (constraints, scannerPreview, callback) => {
      callback({ getText: () => 'raw:qr/value' }, undefined, controls);
      return controls;
    });

    await startQRCodeScanner(preview(), onResult, environment({
      loadReader: async () => ({ decodeFromConstraints }),
    }));

    expect(decodeFromConstraints).toHaveBeenCalledWith(
      { audio: false, video: { facingMode: { ideal: 'environment' } } },
      expect.anything(),
      expect.any(Function),
    );
    expect(onResult).toHaveBeenCalledWith('raw:qr/value');
    expect(stop).toHaveBeenCalledOnce();
  });

  it('stops scanner controls and every camera track', () => {
    const stopControls = vi.fn();
    const stopFirstTrack = vi.fn();
    const stopSecondTrack = vi.fn();
    class FakeMediaStream {
      getTracks() {
        return [{ stop: stopFirstTrack }, { stop: stopSecondTrack }];
      }
    }
    vi.stubGlobal('MediaStream', FakeMediaStream);
    const scannerPreview = preview(new FakeMediaStream() as unknown as MediaStream);

    stopQRCodeScanner({ stop: stopControls }, scannerPreview);

    expect(stopControls).toHaveBeenCalledOnce();
    expect(stopFirstTrack).toHaveBeenCalledOnce();
    expect(stopSecondTrack).toHaveBeenCalledOnce();
    expect(scannerPreview.srcObject).toBeNull();
  });

  it('releases an attached camera stream when scanner startup fails', async () => {
    const stopTrack = vi.fn();
    class FakeMediaStream {
      getTracks() {
        return [{ stop: stopTrack }];
      }
    }
    vi.stubGlobal('MediaStream', FakeMediaStream);
    const scannerPreview = preview(new FakeMediaStream() as unknown as MediaStream);

    await expect(startQRCodeScanner(scannerPreview, vi.fn(), environment({
      loadReader: async () => ({
        decodeFromConstraints: async () => { throw new Error('Scanner failed.'); },
      }),
    }))).rejects.toThrow('Scanner failed.');

    expect(stopTrack).toHaveBeenCalledOnce();
    expect(scannerPreview.srcObject).toBeNull();
  });

  it('keeps a manual-entry fallback for camera failures', () => {
    expect(qrScannerFailureMessage({ name: 'NotAllowedError' })).toContain('denied');
    expect(qrScannerFailureMessage({ name: 'NotFoundError' })).toContain('No camera');
    expect(qrScannerFailureMessage(new Error('Camera policy blocked access.')))
      .toBe('Camera policy blocked access.');
    expect(qrScannerFailureMessage('unknown')).toContain('manually');
  });
});

function environment(overrides: Partial<QRScannerEnvironment>): QRScannerEnvironment {
  return {
    isSecureContext: true,
    mediaDevices: { getUserMedia: vi.fn() } as unknown as MediaDevices,
    loadReader: async () => { throw new Error('Reader was not configured.'); },
    ...overrides,
  };
}

function preview(stream: MediaStream | null = null): HTMLVideoElement {
  return { srcObject: stream } as HTMLVideoElement;
}
