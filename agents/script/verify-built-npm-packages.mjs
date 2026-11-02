// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// Checks the npm pack tarball of every TypeScript agents package before anything installs them.

import { execFileSync } from "node:child_process";
import { existsSync } from "node:fs";
import { join } from "node:path";

const [distDirectory, releaseVersion, ...packages] = process.argv.slice(2);
const corePackageName = "@superdurable/dex-agents";
const problems = [];

for (const packageDirectoryName of packages) {
  const packageName = packageDirectoryName === "core" ? corePackageName : `${corePackageName}-${packageDirectoryName}`;
  const tarball = join(distDirectory, `${packageName.slice(1).replace("/", "-")}-${releaseVersion}.tgz`);
  if (!existsSync(tarball)) {
    problems.push(`missing ${tarball}`);
    continue;
  }
  const members = execFileSync("tar", ["-tzf", tarball], { encoding: "utf8" }).split("\n").filter(Boolean);
  const manifest = JSON.parse(execFileSync("tar", ["-xzOf", tarball, "package/package.json"], { encoding: "utf8" }));
  const label = `${packageName}@${releaseVersion}`;

  for (const member of ["package/LICENSE", "package/README.md", "package/dist/src/index.js", "package/dist/src/index.d.ts"]) {
    if (!members.includes(member)) problems.push(`${label} lacks ${member}`);
  }
  for (const member of members) {
    if (!member.startsWith("package/dist/src/") && !["package/LICENSE", "package/README.md", "package/package.json"].includes(member)) {
      problems.push(`${label} contains ${member}`);
    }
  }
  if (manifest.name !== packageName || manifest.version !== releaseVersion) {
    problems.push(`${label} manifest is ${manifest.name}@${manifest.version}`);
  }
  if (manifest.private === true) problems.push(`${label} is private`);
  if (manifest.publishConfig?.access !== "public") problems.push(`${label} lacks publishConfig.access public`);
  if (manifest.license !== "LicenseRef-Sustainable-Use-1.0") problems.push(`${label} license is ${manifest.license}`);
  if (manifest.types === undefined || manifest.exports?.["."]?.types === undefined) problems.push(`${label} has no types`);
  if (manifest.repository?.directory !== `agents/typescript/${packageDirectoryName}`) {
    problems.push(`${label} repository.directory is ${manifest.repository?.directory}`);
  }
  for (const field of ["dependencies", "peerDependencies", "optionalDependencies"]) {
    for (const [dependency, range] of Object.entries(manifest[field] ?? {})) {
      if (/^(file|link|workspace|git|https?):/.test(range)) problems.push(`${label} ${field}.${dependency} is ${range}`);
    }
  }
  if (packageDirectoryName === "core") {
    // OpenTelemetry stays optional: only the API, only as an optional peer dependency.
    for (const field of ["dependencies", "optionalDependencies"]) {
      for (const dependency of Object.keys(manifest[field] ?? {})) {
        if (dependency.startsWith("@opentelemetry/")) problems.push(`${label} ${field} includes ${dependency}`);
      }
    }
    if (manifest.peerDependencies?.["@opentelemetry/api"] === undefined) {
      problems.push(`${label} must peer-depend on @opentelemetry/api`);
    }
    if (manifest.peerDependenciesMeta?.["@opentelemetry/api"]?.optional !== true) {
      problems.push(`${label} must mark @opentelemetry/api as an optional peer dependency`);
    }
  }
  if (packageDirectoryName !== "core") {
    if (manifest.peerDependencies?.[corePackageName] !== `^${releaseVersion}`) {
      problems.push(`${label} must peer-depend on ${corePackageName}@^${releaseVersion}`);
    }
    if (manifest.dependencies?.[corePackageName] !== undefined) {
      problems.push(`${label} must not bundle its own ${corePackageName} dependency`);
    }
  }
}

for (const problem of problems) console.error(problem);
if (problems.length > 0) process.exit(1);
console.log(`Verified ${packages.length} npm tarballs at ${releaseVersion}`);
