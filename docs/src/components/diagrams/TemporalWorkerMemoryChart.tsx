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

import {ConceptDiagramFrame} from '../conceptDiagram/ConceptDiagram';
import styles from './TemporalComparison.module.css';

type Point = [seconds: number, workingSetMiB: number];

// Worker container working set from bench/results/<run>/stats.csv of superdurable/dex-vs-temporal-demo,
// the sample nearest each 5 s after the first rush message, plus the sample right after the OOM kill.
const benchmark: {limitMiB: number; oomSeconds: number; temporal: Point[]; dex: Point[]} = {
  limitMiB: 512,
  oomSeconds: 330,
  temporal: [
    [-15, 52], [-10, 52], [-5, 52], [0, 54], [5, 88], [10, 117], [15, 148], [20, 175], [25, 201], [30, 232],
    [35, 254], [40, 287], [45, 290], [50, 321], [55, 344], [60, 368], [65, 399], [70, 423], [75, 452], [80, 482],
    [85, 499], [90, 510], [95, 501], [100, 501], [105, 505], [110, 502], [115, 510], [120, 509], [125, 511],
    [130, 504], [135, 505], [140, 507], [145, 506], [150, 508], [155, 508], [160, 507], [165, 504], [170, 503],
    [175, 503], [180, 505], [185, 508], [190, 504], [195, 506], [200, 505], [205, 509], [210, 507], [215, 504],
    [220, 505], [225, 510], [230, 509], [235, 505], [240, 508], [245, 506], [250, 506], [255, 506], [260, 508],
    [265, 506], [270, 506], [275, 510], [280, 507], [285, 507], [290, 506], [295, 510], [300, 508], [305, 509],
    [310, 509], [315, 510], [320, 510], [325, 509], [330, 510], [331, 84], [335, 461], [340, 505], [345, 506],
    [350, 502], [355, 505], [360, 502], [365, 503], [370, 506], [375, 505], [380, 505], [385, 505], [390, 510],
    [395, 504], [400, 505], [405, 504], [410, 509], [415, 506], [420, 508], [425, 506], [430, 510], [435, 512],
    [440, 506], [445, 508], [450, 505], [455, 504], [460, 508], [465, 504], [470, 512], [475, 507], [480, 510],
    [485, 506], [490, 505], [495, 510], [500, 507], [505, 508], [510, 510], [515, 505], [520, 506], [525, 509],
    [530, 507], [535, 510], [540, 505], [545, 506], [550, 508], [555, 505], [560, 505], [565, 510], [570, 509],
    [575, 509], [580, 507], [585, 505], [590, 508], [595, 505], [600, 508], [605, 506], [610, 506], [615, 511],
    [620, 508], [625, 509], [630, 506], [635, 507], [640, 508], [645, 507], [650, 506], [655, 508], [660, 509],
    [665, 507],
  ],
  dex: [
    [-15, 41], [-10, 41], [-5, 41], [0, 42], [5, 48], [10, 49], [15, 56], [20, 55], [25, 57], [30, 58], [35, 57],
    [40, 59], [45, 59], [50, 60], [55, 59], [60, 52], [65, 54], [70, 51], [75, 60], [80, 61], [85, 62], [90, 63],
    [95, 61], [100, 56], [105, 56], [110, 56], [115, 58], [120, 57], [125, 57], [130, 56], [135, 58], [140, 57],
    [145, 58], [150, 73], [155, 73], [160, 72], [165, 73], [170, 72], [175, 73], [180, 72], [185, 73], [190, 76],
    [195, 59], [200, 59], [205, 60], [210, 60], [215, 61], [220, 57], [225, 60], [230, 59], [235, 61], [240, 84],
    [245, 84], [250, 83], [255, 85], [260, 83], [265, 80], [270, 82], [275, 81], [280, 82], [285, 58], [290, 59],
  ],
};

const width = 540;
const height = 320;
const margin = {top: 24, right: 10, bottom: 48, left: 40};
const xDomain: [number, number] = [-15, 670];
const yMaxMiB = 600;
const xTicks = [0, 100, 200, 300, 400, 500, 600];
const yTicks = [0, 100, 200, 300, 400, 500];

function x(seconds: number): number {
  return margin.left + ((seconds - xDomain[0]) / (xDomain[1] - xDomain[0])) * (width - margin.left - margin.right);
}

function y(mib: number): number {
  return margin.top + (1 - mib / yMaxMiB) * (height - margin.top - margin.bottom);
}

function linePath(points: Point[]): string {
  return points.map(([seconds, mib], index) => `${index === 0 ? 'M' : 'L'}${x(seconds).toFixed(1)},${y(mib).toFixed(1)}`).join('');
}

/** Worker memory over time in the Temporal default-cache and Dex runs with a 512 MiB container limit. */
export default function TemporalWorkerMemoryChart({markdownDescription}: {markdownDescription: string}): ReactNode {
  return (
    <ConceptDiagramFrame label={markdownDescription}>
      <svg className={styles.chart} viewBox={`0 0 ${width} ${height}`} aria-hidden="true" focusable="false">
        {yTicks.map((tick) => (
          <g key={`y${tick}`}>
            <line className={styles.grid} x1={margin.left} x2={width - margin.right} y1={y(tick)} y2={y(tick)} />
            <text className={styles.tick} x={margin.left - 8} y={y(tick) + 4} textAnchor="end">
              {tick}
            </text>
          </g>
        ))}
        {xTicks.map((tick) => (
          <text key={`x${tick}`} className={styles.tick} x={x(tick)} y={height - margin.bottom + 17} textAnchor="middle">
            {tick}
          </text>
        ))}
        <line className={styles.axis} x1={margin.left} x2={width - margin.right} y1={y(0)} y2={y(0)} />
        <text className={styles.axisTitle} x={0} y={margin.top - 10}>
          {translate({id: 'docs.temporal.perf.chart.yAxis', message: 'Working set (MiB)'})}
        </text>
        <text className={styles.axisTitle} x={(margin.left + width - margin.right) / 2} y={height - 6} textAnchor="middle">
          {translate({id: 'docs.temporal.perf.chart.xAxis', message: 'Seconds after the customers start to return'})}
        </text>

        <line className={styles.limit} x1={margin.left} x2={width - margin.right} y1={y(benchmark.limitMiB)} y2={y(benchmark.limitMiB)} />
        <text className={styles.limitLabel} x={width - margin.right} y={y(benchmark.limitMiB) - 8} textAnchor="end">
          {translate({id: 'docs.temporal.perf.chart.limit', message: '512 MiB container limit'})}
        </text>

        <line className={styles.oomLine} x1={x(benchmark.oomSeconds)} x2={x(benchmark.oomSeconds)} y1={y(benchmark.limitMiB)} y2={y(0)} />
        <circle className={styles.oomDot} cx={x(benchmark.oomSeconds)} cy={y(benchmark.limitMiB)} r={4.5} />
        <text className={styles.limitLabel} x={x(benchmark.oomSeconds) + 8} y={y(320)}>
          {translate({id: 'docs.temporal.perf.chart.oom', message: 'OOM kill at 330 s'})}
        </text>

        <path className={styles.seriesTemporal} d={linePath(benchmark.temporal)} />
        <path className={styles.seriesDex} d={linePath(benchmark.dex)} />

        <text className={styles.labelTemporal} x={x(345)} y={y(450)}>
          {translate({id: 'docs.temporal.perf.chart.temporal', message: 'Temporal, default cache'})}
        </text>
        <text className={styles.labelDex} x={x(345)} y={y(140)}>
          {translate({id: 'docs.temporal.perf.chart.dex', message: 'Dex, peak 85.5 MiB'})}
        </text>
      </svg>
    </ConceptDiagramFrame>
  );
}
