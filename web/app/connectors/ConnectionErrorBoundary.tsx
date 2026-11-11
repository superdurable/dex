// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { Component, type ErrorInfo, type ReactNode } from 'react';
import { CONNECTORS_COPY } from './copy';

interface ConnectionErrorBoundaryState {
  message: string | null;
}

/** Shows one connection's render error in place; key it per connection to retry. */
export class ConnectionErrorBoundary extends Component<
  { children: ReactNode; className?: string },
  ConnectionErrorBoundaryState
> {
  state: ConnectionErrorBoundaryState = { message: null };

  static getDerivedStateFromError(error: unknown): ConnectionErrorBoundaryState {
    return { message: error instanceof Error ? error.message : String(error) };
  }

  componentDidCatch(error: unknown, info: ErrorInfo) {
    console.error('A Connectors page connection failed to render', error, info.componentStack);
  }

  render() {
    if (this.state.message === null) return this.props.children;
    return <p className={this.props.className ?? 'v2-error'} role="alert">
      {CONNECTORS_COPY.connectionRenderFailed(this.state.message)}
    </p>;
  }
}
