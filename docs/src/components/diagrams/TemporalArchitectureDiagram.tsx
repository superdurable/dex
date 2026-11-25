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
import {Cloud, Cpu, Database, HardDrive, Layers, Server} from 'lucide-react';
import type {LucideIcon} from 'lucide-react';

import {Arrow, ConceptDiagramFrame, DiagramIcon, Eyebrow} from '../conceptDiagram/ConceptDiagram';
import styles from './TemporalArchitectureDiagram.module.css';

type BoxTone = 'code' | 'external' | 'dex';

function Box({icon, tone, title, detail}: {icon: LucideIcon; tone: BoxTone; title: string; detail?: string}): ReactNode {
  return (
    <div className={`${styles.box} ${styles[tone]}`}>
      <DiagramIcon icon={icon} size={15} />
      <span>
        <b>{title}</b>
        {detail ? <small>{detail}</small> : null}
      </span>
    </div>
  );
}

function defaultDescription(): string {
  return translate({
    id: 'docs.temporal.overview.architecture.description',
    message:
      'Temporal: your Worker keeps an in-memory cache of Workflow state and talks to the Temporal Service, which runs Frontend, History, Matching, and Worker services on a database (Cassandra, MySQL, or PostgreSQL). Dex: your Worker keeps a blob cache on disk and talks to one Dex Server binary, which keeps current state in a local disk store and writes to object storage.',
    description: 'The text alternative of the Dex vs. Temporal architecture diagram',
  });
}

/** The two architectures side by side: Temporal's services on a database, and one Dex Server on object storage. */
export default function TemporalArchitectureDiagram({markdownDescription}: {markdownDescription?: string}): ReactNode {
  const yourWorker = translate({
    id: 'docs.temporal.overview.architecture.yourWorker',
    message: 'Your Worker',
    description: 'The application Worker box in the Dex vs. Temporal architecture diagram',
  });
  const services = [
    translate({id: 'docs.temporal.overview.architecture.frontend', message: 'Frontend', description: 'A Temporal service name'}),
    translate({id: 'docs.temporal.overview.architecture.history', message: 'History', description: 'A Temporal service name'}),
    translate({id: 'docs.temporal.overview.architecture.matching', message: 'Matching', description: 'A Temporal service name'}),
    translate({id: 'docs.temporal.overview.architecture.workerService', message: 'Worker', description: 'The Temporal Worker service name'}),
  ];
  return (
    <ConceptDiagramFrame label={markdownDescription ?? defaultDescription()}>
      <div className={styles.layout}>
        <div className={`${styles.lane} ${styles.temporalLane}`}>
          <Eyebrow icon={Layers}>Temporal</Eyebrow>
          <Box
            icon={Cpu}
            tone="code"
            title={yourWorker}
            detail={translate({
              id: 'docs.temporal.overview.architecture.temporalWorkerCache',
              message: 'Workflow state cached in memory',
              description: 'The cache detail of the Temporal Worker box',
            })}
          />
          <Arrow direction="down" tone="external" />
          <div className={styles.service}>
            <span className={styles.serviceName}>
              <DiagramIcon icon={Server} size={14} />
              {translate({
                id: 'docs.temporal.overview.architecture.temporalService',
                message: 'Temporal Service',
                description: 'The group of Temporal services in the architecture diagram',
              })}
            </span>
            <div className={styles.serviceGrid}>
              {services.map((name) => (
                <span key={name} className={styles.serviceChip}>
                  {name}
                </span>
              ))}
            </div>
          </div>
          <Arrow direction="down" tone="external" />
          <Box
            icon={Database}
            tone="external"
            title={translate({
              id: 'docs.temporal.overview.architecture.database',
              message: 'Database',
              description: 'The database box under the Temporal Service',
            })}
            detail={translate({
              id: 'docs.temporal.overview.architecture.databaseDetail',
              message: 'Cassandra, MySQL, or PostgreSQL',
              description: 'The databases Temporal supports',
            })}
          />
        </div>
        <div className={`${styles.lane} ${styles.dexLane}`}>
          <Eyebrow icon={Layers}>Dex</Eyebrow>
          <Box
            icon={Cpu}
            tone="code"
            title={yourWorker}
            detail={translate({
              id: 'docs.temporal.overview.architecture.dexWorkerCache',
              message: 'Blob cache on disk',
              description: 'The cache detail of the Dex Worker box',
            })}
          />
          <Arrow direction="down" tone="accent" />
          <div className={`${styles.service} ${styles.dexService}`}>
            <span className={styles.serviceName}>
              <DiagramIcon icon={Server} size={14} />
              {translate({
                id: 'docs.temporal.overview.architecture.dexServer',
                message: 'Dex Server: one binary',
                description: 'The Dex Server group in the architecture diagram',
              })}
            </span>
            <span className={`${styles.serviceChip} ${styles.wideChip}`}>
              <DiagramIcon icon={HardDrive} size={13} />
              {translate({
                id: 'docs.temporal.overview.architecture.localStore',
                message: 'Local disk store',
                description: 'The local disk store inside Dex Server',
              })}
            </span>
          </div>
          <Arrow direction="down" tone="accent" />
          <Box
            icon={Cloud}
            tone="external"
            title={translate({
              id: 'docs.temporal.overview.architecture.objectStorage',
              message: 'Object storage',
              description: 'The object storage box under Dex Server',
            })}
            detail={translate({
              id: 'docs.temporal.overview.architecture.objectStorageDetail',
              message: 'S3, GCS, Azure Blob, Aliyun OSS, Tencent COS, or S3-compatible',
              description: 'The object storage services Dex Server supports',
            })}
          />
        </div>
      </div>
    </ConceptDiagramFrame>
  );
}
