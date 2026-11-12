// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// Output canary for a TypeScript command, loaded with NODE_OPTIONS=--require.
// The Worker host does what sitecustomize.py does: raw fd writes and a child
// with inherited output. Node has no atomic dup2, so the TypeScript launcher
// redirects only its JavaScript output; its canary writes through that.
"use strict";

const fs = require("node:fs");
const path = require("node:path");
const childProcess = require("node:child_process");

const directory = process.env.DEX_TUI_E2E_CANARY_DIR;
const canaryText = "TUIE2ECANARY";

function writeWorkerHostCanaries(role) {
  for (const fd of [1, 2]) {
    fs.writeSync(fd, `${canaryText} ${role} fd ${fd}\n`);
  }
  childProcess.spawnSync(
    "/bin/sh",
    ["-c", `echo ${canaryText} ${role} child fd 1; echo ${canaryText} ${role} child fd 2 >&2`],
    { stdio: "inherit" },
  );
}

function writeLauncherCanaries(role) {
  process.stdout.write(`${canaryText} ${role} fd 1\n`);
  process.stderr.write(`${canaryText} ${role} fd 2\n`);
  console.log(`${canaryText} ${role} console log`);
  console.error(`${canaryText} ${role} console error`);
}

if (directory) {
  const role = process.argv.includes("__worker-host") ? "worker-host" : "launcher";
  const trigger = path.join(directory, "trigger");
  const timer = setInterval(() => {
    if (!fs.existsSync(trigger)) {
      return;
    }
    clearInterval(timer);
    if (role === "worker-host") {
      writeWorkerHostCanaries(role);
    } else {
      writeLauncherCanaries(role);
    }
    fs.writeFileSync(path.join(directory, `${role}-${process.pid}.written`), role);
  }, 100);
  timer.unref();
}
