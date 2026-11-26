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
import {Download, HardDrive, Hash, History, MemoryStick, Play, RotateCcw, Scale} from 'lucide-react';

import {ConceptDiagramFrame} from '../conceptDiagram/ConceptDiagram';
import {TemporalLane, TemporalLaneBox, TemporalLaneLayout} from './TemporalLanes';

/** Where each Worker keeps its cache, what limits it, and what a miss costs. */
export default function TemporalWorkerCacheDiagram({markdownDescription}: {markdownDescription: string}): ReactNode {
  return (
    <ConceptDiagramFrame label={markdownDescription}>
      <TemporalLaneLayout>
        <TemporalLane name={translate({id: 'docs.temporal.perf.cache.temporal', message: 'Temporal Worker'})} icon={History} tone="external">
          <TemporalLaneBox
            icon={MemoryStick}
            title={translate({id: 'docs.temporal.perf.cache.temporalWhere', message: 'Workflow cache in memory'})}
            detail={translate({id: 'docs.temporal.perf.cache.temporalWhereDetail', message: 'state of each cached Workflow Execution'})}
          />
          <TemporalLaneBox
            icon={Hash}
            title={translate({id: 'docs.temporal.perf.cache.temporalLimit', message: 'Limit: a count of executions'})}
            detail={translate({id: 'docs.temporal.perf.cache.temporalLimitDetail', message: 'Python default 1,000'})}
          />
          <TemporalLaneBox
            icon={RotateCcw}
            title={translate({id: 'docs.temporal.perf.cache.temporalMiss', message: 'Evicted: replay'})}
            detail={translate({id: 'docs.temporal.perf.cache.temporalMissDetail', message: 'the Event History, from the start'})}
          />
        </TemporalLane>
        <TemporalLane name={translate({id: 'docs.temporal.perf.cache.dex', message: 'Dex Worker'})} icon={Play} tone="accent">
          <TemporalLaneBox
            icon={HardDrive}
            title={translate({id: 'docs.temporal.perf.cache.dexWhere', message: 'Blob cache on disk'})}
            detail={translate({id: 'docs.temporal.perf.cache.dexWhereDetail', message: 'copies of large values'})}
          />
          <TemporalLaneBox
            icon={Scale}
            title={translate({id: 'docs.temporal.perf.cache.dexLimit', message: 'Limit: bytes'})}
            detail={translate({id: 'docs.temporal.perf.cache.dexLimitDetail', message: '1 GiB in the benchmark'})}
          />
          <TemporalLaneBox
            icon={Download}
            title={translate({id: 'docs.temporal.perf.cache.dexMiss', message: 'Miss: download the value'})}
            detail={translate({id: 'docs.temporal.perf.cache.dexMissDetail', message: 'state is saved in Dex Server'})}
          />
        </TemporalLane>
      </TemporalLaneLayout>
    </ConceptDiagramFrame>
  );
}
