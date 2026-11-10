// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import { chmodSync, existsSync, mkdirSync, mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { createServer, type Server } from "node:http";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { after, before, describe, test } from "node:test";

import { pinnedDexcliVersion, readCliEnvironment, resolveDexcliExecutable } from "../../src/index.js";

describe("dexcli first-run download", () => {
  let server: Server;
  let baseUrl: string;
  let archive: Buffer;
  let checksum: string;
  const requests: string[] = [];
  const archiveName = `dexcli_v${pinnedDexcliVersion}_${process.platform}_${process.arch === "x64" ? "amd64" : process.arch}.tar.gz`;

  before(async () => {
    const staging = mkdtempSync(join(tmpdir(), "dexcli-archive-"));
    for (const name of ["dexcli", "dex-server-zd"]) {
      writeFileSync(join(staging, name), `#!/bin/sh\necho "${name} v${pinnedDexcliVersion}"\n`);
      chmodSync(join(staging, name), 0o755);
    }
    execFileSync("tar", ["-czf", join(staging, archiveName), "-C", staging, "dexcli", "dex-server-zd"]);
    archive = readFileSync(join(staging, archiveName));
    checksum = createHash("sha256").update(archive).digest("hex");
    server = createServer((request, response) => {
      requests.push(request.url ?? "");
      if (request.url === `/good/cli-v${pinnedDexcliVersion}/${archiveName}` || request.url === `/bad/cli-v${pinnedDexcliVersion}/${archiveName}`) {
        response.writeHead(200).end(archive);
      } else if (request.url === `/good/cli-v${pinnedDexcliVersion}/checksums.txt`) {
        response.writeHead(200).end(`${checksum}  ${archiveName}\n`);
      } else if (request.url === `/bad/cli-v${pinnedDexcliVersion}/checksums.txt`) {
        response.writeHead(200).end(`${"0".repeat(64)}  ${archiveName}\n`);
      } else {
        response.writeHead(404).end();
      }
    });
    await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
    const address = server.address();
    baseUrl = `http://127.0.0.1:${typeof address === "object" && address !== null ? address.port : 0}`;
  });

  after(() => {
    server?.close();
  });

  function environmentFor(home: string, path: string) {
    return readCliEnvironment({ HOME: home, PATH: "", DEX_AGENTS_DEXCLI_DOWNLOAD_URL: `${baseUrl}/${path}` });
  }

  test("a checked archive is unpacked into ~/.dex/bin once and reused", async () => {
    const home = mkdtempSync(join(tmpdir(), "dexcli-home-"));
    const lines: string[] = [];
    const dexcli = await resolveDexcliExecutable(environmentFor(home, "good"), (line) => lines.push(line));
    assert.equal(dexcli, join(home, ".dex", "bin", `dexcli-${pinnedDexcliVersion}`, "dexcli"));
    assert.equal(execFileSync(dexcli).toString().trim(), `dexcli v${pinnedDexcliVersion}`);
    assert.equal(existsSync(join(home, ".dex", "bin", `dexcli-${pinnedDexcliVersion}`, "dex-server-zd")), true);
    assert.match(lines.join("\n"), /Downloading dexcli/);
    const requestCount = requests.length;
    assert.equal(await resolveDexcliExecutable(environmentFor(home, "good"), () => undefined), dexcli);
    assert.equal(requests.length, requestCount);
  });

  test("an archive whose SHA-256 differs from checksums.txt is refused and nothing is installed", async () => {
    const home = mkdtempSync(join(tmpdir(), "dexcli-home-"));
    mkdirSync(join(home, ".dex"), { recursive: true });
    await assert.rejects(resolveDexcliExecutable(environmentFor(home, "bad"), () => undefined), /checksums\.txt says 0{64}/);
    assert.equal(existsSync(join(home, ".dex", "bin", `dexcli-${pinnedDexcliVersion}`)), false);
  });

  test("DEXCLI wins over every other source", async () => {
    const home = mkdtempSync(join(tmpdir(), "dexcli-home-"));
    const explicit = join(home, "my-dexcli");
    writeFileSync(explicit, "#!/bin/sh\n");
    chmodSync(explicit, 0o755);
    const environment = readCliEnvironment({ HOME: home, PATH: "", DEXCLI: explicit });
    assert.equal(await resolveDexcliExecutable(environment, () => undefined), explicit);
    await assert.rejects(
      resolveDexcliExecutable(readCliEnvironment({ HOME: home, DEXCLI: join(home, "missing") }), () => undefined),
      /DEXCLI is not an executable file/,
    );
  });
});
