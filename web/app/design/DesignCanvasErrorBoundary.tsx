// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { Component, type ErrorInfo, type ReactNode } from 'react';

interface DesignCanvasErrorBoundaryState {
  message: string | null;
}

/**
 * Keeps a graph the canvas cannot draw from taking the page down with it.
 *
 * Give it a key per definition, so choosing another definition draws again.
 */
export class DesignCanvasErrorBoundary extends Component<{ children: ReactNode }, DesignCanvasErrorBoundaryState> {
  state: DesignCanvasErrorBoundaryState = { message: null };

  static getDerivedStateFromError(error: unknown): DesignCanvasErrorBoundaryState {
    return { message: error instanceof Error ? error.message : String(error) };
  }

  componentDidCatch(error: unknown, info: ErrorInfo) {
    console.error('The Flow design graph failed to draw', error, info.componentStack);
  }

  render() {
    if (this.state.message === null) return this.props.children;
    return <DesignUnavailable title="This Flow design graph could not be drawn" detail={this.state.message} />;
  }
}

/** What the canvas shows instead of a graph that is too large or invalid. */
export function DesignUnavailable({ title, detail }: { title: string; detail: string }) {
  return (
    <div className="dg-empty dg-unavailable" role="alert">
      <b>{title}</b>
      <span>{detail}</span>
    </div>
  );
}
