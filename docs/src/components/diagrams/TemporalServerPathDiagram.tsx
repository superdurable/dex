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
import {Cloud, Copy, Database, HardDrive, History, Play, Server, Users} from 'lucide-react';

import {ConceptDiagramFrame} from '../conceptDiagram/ConceptDiagram';
import {TemporalLane, TemporalLaneArrow, TemporalLaneBox, TemporalLaneLayout, TemporalLaneNote, TemporalLaneRow} from './TemporalLanes';

/** The write and read path of each server: services on a database, or a log in object storage and a local store. */
export default function TemporalServerPathDiagram({markdownDescription}: {markdownDescription: string}): ReactNode {
  const clients = translate({id: 'docs.temporal.perf.path.clients', message: 'Workers and Clients'});
  return (
    <ConceptDiagramFrame label={markdownDescription}>
      <TemporalLaneLayout>
        <TemporalLane name={translate({id: 'docs.temporal.perf.path.temporal', message: 'Temporal Service'})} icon={History} tone="external">
          <TemporalLaneBox icon={Users} title={clients} />
          <TemporalLaneArrow tone="external" />
          <TemporalLaneBox
            icon={Server}
            title={translate({id: 'docs.temporal.perf.path.temporalServices', message: 'Frontend, History, Matching'})}
            detail={translate({id: 'docs.temporal.perf.path.temporalServicesDetail', message: 'History keeps in-memory caches'})}
          />
          <TemporalLaneArrow tone="external" label={translate({id: 'docs.temporal.perf.path.temporalReadWrite', message: 'reads and writes'})} />
          <TemporalLaneBox
            icon={Database}
            title={translate({id: 'docs.temporal.perf.path.temporalDatabase', message: 'Database'})}
            detail={translate({id: 'docs.temporal.perf.path.temporalDatabaseDetail', message: 'Cassandra, MySQL, or PostgreSQL'})}
          />
        </TemporalLane>
        <TemporalLane name={translate({id: 'docs.temporal.perf.path.dex', message: 'Dex Server'})} icon={Play} tone="accent">
          <TemporalLaneBox icon={Users} title={clients} />
          <TemporalLaneArrow tone="accent" />
          <TemporalLaneBox
            icon={Server}
            title={translate({id: 'docs.temporal.perf.path.dexNode', message: 'Active node'})}
            detail={translate({id: 'docs.temporal.perf.path.dexNodeDetail', message: 'batches the writes of many Flows'})}
          />
          <TemporalLaneArrow tone="accent" label={translate({id: 'docs.temporal.perf.path.dexWrite', message: 'write first, then acknowledge'})} />
          <TemporalLaneRow>
            <TemporalLaneBox
              icon={Cloud}
              title={translate({id: 'docs.temporal.perf.path.dexLog', message: 'Log in object storage'})}
              detail={translate({id: 'docs.temporal.perf.path.dexLogDetail', message: 'the record'})}
            />
            <TemporalLaneBox
              icon={HardDrive}
              title={translate({id: 'docs.temporal.perf.path.dexLocal', message: 'Local store'})}
              detail={translate({id: 'docs.temporal.perf.path.dexLocalDetail', message: 'serves reads from local disk'})}
            />
          </TemporalLaneRow>
          <TemporalLaneNote icon={Copy}>
            {translate({id: 'docs.temporal.perf.path.dexStandby', message: 'A hot standby follows the log'})}
          </TemporalLaneNote>
        </TemporalLane>
      </TemporalLaneLayout>
    </ConceptDiagramFrame>
  );
}
