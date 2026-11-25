/*
 * Copyright (c) 2026 Super Durable, Inc.
 *
 * Licensed under the Super Durable Source License 1.0.
 * You may not use this file except in compliance with the License.
 * See the LICENSE file in the repository root.
 *
 * SPDX-License-Identifier: LicenseRef-Super-Durable-1.0
 */

import React, {type ReactNode} from 'react';
import {translate} from '@docusaurus/Translate';
import {Cloud, Copy, Database, History, Play, Search, Server} from 'lucide-react';

import {ConceptDiagramFrame} from '../conceptDiagram/ConceptDiagram';
import {TemporalLane, TemporalLaneArrow, TemporalLaneBox, TemporalLaneLayout, TemporalLaneRow} from './TemporalLanes';

/** What an operator deploys and keeps running for each server. */
export default function TemporalDeploymentDiagram({markdownDescription}: {markdownDescription: string}): ReactNode {
  return (
    <ConceptDiagramFrame label={markdownDescription}>
      <TemporalLaneLayout>
        <TemporalLane name={translate({id: 'docs.temporal.ops.deploy.temporal', message: 'Temporal Service'})} icon={History} tone="external">
          <TemporalLaneRow>
            <TemporalLaneBox icon={Server} title="Frontend" />
            <TemporalLaneBox icon={Server} title="History" />
          </TemporalLaneRow>
          <TemporalLaneRow>
            <TemporalLaneBox icon={Server} title="Matching" />
            <TemporalLaneBox icon={Server} title="Worker" />
          </TemporalLaneRow>
          <TemporalLaneArrow tone="external" />
          <TemporalLaneRow>
            <TemporalLaneBox
              icon={Database}
              title={translate({id: 'docs.temporal.ops.deploy.temporalDatabase', message: 'Database'})}
              detail={translate({id: 'docs.temporal.ops.deploy.temporalDatabaseDetail', message: 'Cassandra, MySQL, or PostgreSQL'})}
            />
            <TemporalLaneBox
              icon={Search}
              title={translate({id: 'docs.temporal.ops.deploy.temporalVisibility', message: 'Visibility store'})}
              detail={translate({id: 'docs.temporal.ops.deploy.temporalVisibilityDetail', message: 'SQL or Elasticsearch'})}
            />
          </TemporalLaneRow>
        </TemporalLane>
        <TemporalLane name={translate({id: 'docs.temporal.ops.deploy.dex', message: 'Dex Server'})} icon={Play} tone="accent">
          <TemporalLaneRow>
            <TemporalLaneBox
              icon={Server}
              title={translate({id: 'docs.temporal.ops.deploy.dexActive', message: 'Active node'})}
              detail={translate({id: 'docs.temporal.ops.deploy.dexActiveDetail', message: 'one binary, holds the lease'})}
            />
            <TemporalLaneBox
              icon={Copy}
              title={translate({id: 'docs.temporal.ops.deploy.dexStandby', message: 'Standby node'})}
              detail={translate({id: 'docs.temporal.ops.deploy.dexStandbyDetail', message: 'optional, takes over'})}
            />
          </TemporalLaneRow>
          <TemporalLaneArrow tone="accent" />
          <TemporalLaneBox
            icon={Cloud}
            title={translate({id: 'docs.temporal.ops.deploy.dexStorage', message: 'Object storage'})}
            detail={translate({id: 'docs.temporal.ops.deploy.dexStorageDetail', message: 'Amazon S3 or S3-compatible, or db9'})}
          />
        </TemporalLane>
      </TemporalLaneLayout>
    </ConceptDiagramFrame>
  );
}
