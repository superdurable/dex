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
import { appendFileSync, chmodSync, existsSync, mkdirSync, mkdtempSync, readdirSync, writeFileSync } from "node:fs";
import { createServer, type Server } from "node:http";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { after, before, describe, test } from "node:test";

import { pinnedDexcliVersion, readCliEnvironment, resolveDexcliExecutable } from "../../src/index.js";
import { buildTarGz, type TarEntry } from "../fixtures/tar-archive-builder.js";

const archiveName = `dexcli_v${pinnedDexcliVersion}_${process.platform}_${process.arch === "x64" ? "amd64" : process.arch}.tar.gz`;
const script = (name: string): string => `#!/bin/sh\necho "${name} v${pinnedDexcliVersion}"\n`;
const goodEntries: readonly TarEntry[] = [
  { name: "dexcli", content: script("dexcli") },
  { name: "dex-server-zd", content: script("dex-server-zd") },
  { name: "LICENSES/", typeFlag: "5" },
  { name: "LICENSES/MIT.txt", content: "license" },
];

describe("dexcli first-run download", () => {
  let server: Server;
  let baseUrl: string;
  const archives = new Map<string, Buffer>();
  const requests: string[] = [];

  before(async () => {
    archives.set("good", buildTarGz(goodEntries));
    archives.set("symlink", buildTarGz([{ name: "dexcli", typeFlag: "2", linkName: "/bin/sh" }, goodEntries[1]!]));
    archives.set("parent", buildTarGz([...goodEntries, { name: "../escaped", content: "x" }]));
    archives.set("absolute", buildTarGz([...goodEntries, { name: "/tmp/escaped", content: "x" }]));
    archives.set("hardlink", buildTarGz([...goodEntries, { name: "copy", typeFlag: "1", linkName: "dexcli" }]));
    server = createServer((request, response) => {
      requests.push(request.url ?? "");
      const [, kind, release, file] = (request.url ?? "").split("/");
      const archive = kind === undefined ? undefined : archives.get(kind);
      if (archive === undefined || release !== `cli-v${pinnedDexcliVersion}`) {
        response.writeHead(404).end();
      } else if (file === archiveName) {
        response.writeHead(200).end(archive);
      } else if (file === "checksums.txt") {
        const digest = kind === "good-bad-checksum" ? "0".repeat(64) : createHash("sha256").update(archive).digest("hex");
        response.writeHead(200).end(`${digest}  ${archiveName}\n`);
      } else {
        response.writeHead(404).end();
      }
    });
    archives.set("good-bad-checksum", archives.get("good")!);
    await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
    const address = server.address();
    baseUrl = `http://127.0.0.1:${typeof address === "object" && address !== null ? address.port : 0}`;
  });

  after(() => {
    server?.close();
  });

  function environmentFor(home: string, kind: string, path = "") {
    return readCliEnvironment({ HOME: home, PATH: path, DEX_AGENTS_DEXCLI_DOWNLOAD_URL: `${baseUrl}/${kind}` });
  }

  function newHome(): string {
    return mkdtempSync(join(tmpdir(), "dexcli-home-"));
  }

  test("a checked archive installs only its two executables, and the cache is reused", async () => {
    const home = newHome();
    const lines: string[] = [];
    const dexcli = await resolveDexcliExecutable(environmentFor(home, "good"), (line) => lines.push(line));
    const installDirectory = join(home, ".dex", "bin", `dexcli-${pinnedDexcliVersion}`);
    assert.equal(dexcli, join(installDirectory, "dexcli"));
    assert.equal(execFileSync(dexcli).toString().trim(), `dexcli v${pinnedDexcliVersion}`);
    assert.deepEqual(readdirSync(installDirectory).sort(), ["dex-server-zd", "dexcli", "install.json"]);
    assert.match(lines.join("\n"), /Downloading dexcli/);
    const requestCount = requests.length;
    assert.equal(await resolveDexcliExecutable(environmentFor(home, "good"), () => undefined), dexcli);
    assert.equal(requests.length, requestCount);
  });

  test("a cached executable that changed after install is refused", async () => {
    const home = newHome();
    const dexcli = await resolveDexcliExecutable(environmentFor(home, "good"), () => undefined);
    appendFileSync(join(home, ".dex", "bin", `dexcli-${pinnedDexcliVersion}`, "dex-server-zd"), "echo tampered\n");
    await assert.rejects(
      resolveDexcliExecutable(environmentFor(home, "good"), () => undefined),
      /has a dex-server-zd that changed since it was installed; delete that directory/,
    );
    assert.equal(existsSync(dexcli), true);
  });

  test("an archive whose SHA-256 differs from checksums.txt is refused and nothing is installed", async () => {
    const home = newHome();
    await assert.rejects(resolveDexcliExecutable(environmentFor(home, "good-bad-checksum"), () => undefined), /checksums\.txt says 0{64}/);
    assert.equal(existsSync(join(home, ".dex", "bin", `dexcli-${pinnedDexcliVersion}`)), false);
  });

  for (const [kind, message] of [
    ["symlink", /entry dexcli is not a regular file or a directory/],
    ["parent", /"\.\.\/escaped" is not a safe relative path/],
    ["absolute", /"\/tmp\/escaped" is not a safe relative path/],
    ["hardlink", /entry copy is not a regular file or a directory/],
  ] as const) {
    test(`an archive with a ${kind} entry is refused and writes nothing`, async () => {
      const home = newHome();
      await assert.rejects(resolveDexcliExecutable(environmentFor(home, kind), () => undefined), message);
      const binDirectory = join(home, ".dex", "bin");
      assert.deepEqual(existsSync(binDirectory) ? readdirSync(binDirectory) : [], []);
      assert.equal(existsSync(join(home, ".dex", "escaped")), false);
    });
  }

  test("the PATH scan passes an old dexcli and takes a newer one later on PATH", async () => {
    const home = newHome();
    const oldDirectory = join(home, "old");
    const newDirectory = join(home, "new");
    for (const [directory, version] of [
      [oldDirectory, "1.0.0"],
      [newDirectory, "99.0.0"],
    ] as const) {
      mkdirSync(directory);
      writeFileSync(join(directory, "dexcli"), `#!/bin/sh\necho "dexcli v${version}"\n`);
      chmodSync(join(directory, "dexcli"), 0o755);
    }
    const environment = readCliEnvironment({
      HOME: home,
      PATH: `${oldDirectory}:${newDirectory}`,
      DEX_AGENTS_DEXCLI_DOWNLOAD_URL: `${baseUrl}/missing`,
    });
    assert.equal(await resolveDexcliExecutable(environment, () => undefined), join(newDirectory, "dexcli"));
  });

  test("a download URL must be https, or http on a loopback host", async () => {
    const home = newHome();
    const environment = readCliEnvironment({ HOME: home, PATH: "", DEX_AGENTS_DEXCLI_DOWNLOAD_URL: "http://mirror.example.com/dex" });
    await assert.rejects(resolveDexcliExecutable(environment, () => undefined), /must be an https URL, or http on 127\.0\.0\.1/);
  });

  test("DEXCLI wins over every other source", async () => {
    const home = newHome();
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
