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
const cliRuntimePackageName = `${corePackageName}-cli`;
const problems = [];

// Maps an agents package, such as core, vercel-ai, cli/runtime, or cli/builtin, to its npm name and directory.
function describePackage(packageId) {
  if (packageId === "core") return { packageName: corePackageName, directory: "agents/typescript/core" };
  if (packageId === "cli/runtime") return { packageName: cliRuntimePackageName, directory: "agents/cli/typescript/runtime" };
  if (packageId.startsWith("cli/")) {
    const name = packageId.slice("cli/".length);
    return { packageName: `${corePackageName}-${name}-cli`, directory: `agents/cli/typescript/${name}`, command: `dex-agents-${name}` };
  }
  return { packageName: `${corePackageName}-${packageId}`, directory: `agents/typescript/${packageId}` };
}

function isAgentsPackageName(name) {
  return name === corePackageName || name.startsWith(`${corePackageName}-`);
}

for (const packageDirectoryName of packages) {
  const { packageName, directory, command } = describePackage(packageDirectoryName);
  const tarball = join(distDirectory, `${packageName.slice(1).replace("/", "-")}-${releaseVersion}.tgz`);
  if (!existsSync(tarball)) {
    problems.push(`missing ${tarball}`);
    continue;
  }
  const members = execFileSync("tar", ["-tzf", tarball], { encoding: "utf8" }).split("\n").filter(Boolean);
  const manifest = JSON.parse(execFileSync("tar", ["-xzOf", tarball, "package/package.json"], { encoding: "utf8" }));
  const label = `${packageName}@${releaseVersion}`;

  // The CLI runtime also ships the shared configuration schemas.
  const schemaMembers = packageName === cliRuntimePackageName
    ? ["package/dist/schemas/config.schema.json", "package/dist/schemas/session-settings.schema.json"]
    : [];
  for (const member of ["package/LICENSE", "package/README.md", "package/dist/src/index.js", "package/dist/src/index.d.ts", ...schemaMembers]) {
    if (!members.includes(member)) problems.push(`${label} lacks ${member}`);
  }
  for (const member of members) {
    if (!member.startsWith("package/dist/src/") && !["package/LICENSE", "package/README.md", "package/package.json", ...schemaMembers].includes(member)) {
      problems.push(`${label} contains ${member}`);
    }
  }
  if (command !== undefined) {
    const binFile = manifest.bin?.[command];
    if (typeof binFile !== "string" || !members.includes(`package/${binFile.replace(/^\.\//, "")}`)) {
      problems.push(`${label} bin ${command} is ${binFile}, not a packed file`);
    } else {
      const binSource = execFileSync("tar", ["-xzOf", tarball, `package/${binFile.replace(/^\.\//, "")}`], { encoding: "utf8" });
      if (!binSource.startsWith("#!/usr/bin/env node\n")) problems.push(`${label} bin ${binFile} lacks the node shebang`);
    }
  }
  if (manifest.name !== packageName || manifest.version !== releaseVersion) {
    problems.push(`${label} manifest is ${manifest.name}@${manifest.version}`);
  }
  if (manifest.private === true) problems.push(`${label} is private`);
  if (manifest.publishConfig?.access !== "public") problems.push(`${label} lacks publishConfig.access public`);
  if (manifest.license !== "LicenseRef-Sustainable-Use-1.0") problems.push(`${label} license is ${manifest.license}`);
  if (manifest.types === undefined || manifest.exports?.["."]?.types === undefined) problems.push(`${label} has no types`);
  for (const [subpath, target] of Object.entries(manifest.exports ?? {})) {
    for (const condition of ["types", "import"]) {
      const file = target?.[condition];
      if (typeof file !== "string" || !members.includes(`package/${file.replace(/^\.\//, "")}`)) {
        problems.push(`${label} export ${subpath} lacks its ${condition} file ${file}`);
      }
    }
  }
  for (const dependency of Object.keys(manifest.peerDependenciesMeta ?? {})) {
    if (manifest.peerDependencies?.[dependency] === undefined) {
      problems.push(`${label} peerDependenciesMeta.${dependency} names no peer dependency`);
    }
  }
  if (manifest.repository?.directory !== directory) {
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
    // pi-ai belongs to @superdurable/dex-agents-pi, so core users never install it.
    for (const field of ["dependencies", "peerDependencies", "optionalDependencies"]) {
      if (manifest[field]?.["@earendil-works/pi-ai"] !== undefined) problems.push(`${label} ${field} includes @earendil-works/pi-ai`);
    }
  }
  if (packageDirectoryName !== "core") {
    const requiredPeers = command === undefined ? [corePackageName] : [corePackageName, cliRuntimePackageName];
    for (const peer of requiredPeers) {
      if (manifest.peerDependencies?.[peer] === undefined) problems.push(`${label} must peer-depend on ${peer}@^${releaseVersion}`);
    }
    for (const [peer, range] of Object.entries(manifest.peerDependencies ?? {})) {
      if (isAgentsPackageName(peer) && range !== `^${releaseVersion}`) {
        problems.push(`${label} must peer-depend on ${peer}@^${releaseVersion}, not ${range}`);
      }
    }
    for (const field of ["dependencies", "optionalDependencies"]) {
      for (const dependency of Object.keys(manifest[field] ?? {})) {
        if (isAgentsPackageName(dependency)) problems.push(`${label} must not bundle its own ${dependency} dependency`);
      }
    }
  }
}

for (const problem of problems) console.error(problem);
if (problems.length > 0) process.exit(1);
console.log(`Verified ${packages.length} npm tarballs at ${releaseVersion}`);
