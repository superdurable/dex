// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { existsSync, mkdirSync, openSync, closeSync, ftruncateSync, readdirSync, readFileSync, rmSync, writeFileSync, writeSync } from "node:fs";
import { join } from "node:path";
import { after, afterEach, before, describe, test } from "node:test";
import { pathToFileURL } from "node:url";
import { deflateSync } from "node:zlib";

import { maxImageBytes } from "@superdurable/dex-agents";
import { PrivateDexEngine } from "@superdurable/dex-agents/testing";

import type { AgentHarness } from "../../src/index.js";
import { CliProcess, SessionClients, TestHome, enginePorts } from "../fixtures/cli-test-support.js";
import { scriptedHarness, scriptedHarnessWithoutImages } from "../fixtures/scripted-harness.js";

const noConnectors = "connectors: {enabled: []}\nlocal_tools: {browser: false}\n";

/** Returns a 1x1 PNG. */
function pngBytes(): Buffer {
  const chunk = (kind: string, data: Buffer): Buffer => {
    const length = Buffer.alloc(4);
    length.writeUInt32BE(data.length);
    const body = Buffer.concat([Buffer.from(kind, "latin1"), data]);
    const checksum = Buffer.alloc(4);
    checksum.writeUInt32BE(crc32(body));
    return Buffer.concat([length, body, checksum]);
  };
  const header = Buffer.from([0, 0, 0, 1, 0, 0, 0, 1, 8, 2, 0, 0, 0]);
  return Buffer.concat([
    Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]),
    chunk("IHDR", header),
    chunk("IDAT", deflateSync(Buffer.from([0, 0xff, 0, 0]))),
    chunk("IEND", Buffer.alloc(0)),
  ]);
}

/** Returns bytes that pass the structural JPEG check: SOI, a marker, and EOI. */
function jpegBytes(): Buffer {
  return Buffer.concat([Buffer.from([0xff, 0xd8, 0xff, 0xdb]), Buffer.alloc(16), Buffer.from([0xff, 0xd9])]);
}

function crc32(data: Buffer): number {
  let crc = 0xffffffff;
  for (const byte of data) {
    crc ^= byte;
    for (let bit = 0; bit < 8; bit += 1) {
      crc = crc & 1 ? (crc >>> 1) ^ 0xedb88320 : crc >>> 1;
    }
  }
  return (crc ^ 0xffffffff) >>> 0;
}

function sha256Of(data: Buffer): string {
  return createHash("sha256").update(data).digest("hex");
}

describe("terminal chat images", () => {
  let engine: PrivateDexEngine;
  let sessionClients: SessionClients;
  const processes: CliProcess[] = [];
  const homes: TestHome[] = [];

  before(async () => {
    engine = await PrivateDexEngine.start();
    sessionClients = new SessionClients(engine);
  });

  afterEach(async () => {
    for (const cli of processes.splice(0)) {
      await cli.kill();
    }
  });

  after(async () => {
    await sessionClients?.close();
    await engine?.stop();
    for (const home of homes) {
      rmSync(home.directory, { recursive: true, force: true });
    }
  });

  function newHome(config: string, harness: AgentHarness = scriptedHarness): TestHome {
    const home = new TestHome(enginePorts(engine), harness);
    homes.push(home);
    home.writeConfig(config);
    return home;
  }

  function start(home: TestHome): CliProcess {
    const cli = CliProcess.start(home);
    processes.push(cli);
    return cli;
  }

  test("an attached image reaches the model from the local media store", async () => {
    const home = newHome(noConnectors);
    const png = pngBytes();
    const jpeg = jpegBytes();
    writeFileSync(join(home.directory, "chart.png"), png);
    const photo = join(home.directory, "my photos", "photo.jpg");
    mkdirSync(join(home.directory, "my photos"));
    writeFileSync(photo, jpeg);
    const cli = start(home);
    const sessionId = await cli.waitForSession();
    assert.match(cli.stdout.all, /\/image <path> attaches an image to the next one/);
    cli.writeLine("/image ~/chart.png");
    await cli.stdout.waitFor(`Attached chart.png (image/png, ${png.length} bytes); it goes with your next message (1 image attached).`);
    cli.writeLine(`/image "${photo}"`);
    await cli.stdout.waitFor(`Attached photo.jpg (image/jpeg, ${jpeg.length} bytes)`);
    await cli.stdout.waitFor("(2 images attached)");
    cli.writeLine("images");
    await cli.stdout.waitFor(`saw image ${sha256Of(png)} ${png.length}; saw image ${sha256Of(jpeg)} ${jpeg.length}`);
    // The next message carries no images: they went with the previous one.
    cli.writeLine("thanks");
    await cli.stdout.waitFor("echo: thanks");
    // The command's Worker answers the snapshot RPC, so read it before the command exits.
    const snapshot = await (await sessionClients.of(home)).getAgentSnapshot(sessionId);
    cli.writeLine("/exit");
    assert.equal(await cli.waitForExit(), 0);

    for (const data of [png, jpeg]) {
      assert.deepEqual(readFileSync(join(home.mediaDirectory, "sha256", sha256Of(data))), data);
    }
    // The store holds a copy: removing the original keeps the image.
    rmSync(join(home.directory, "chart.png"));
    assert.equal(existsSync(join(home.mediaDirectory, "sha256", sha256Of(png))), true);

    const userMessages = snapshot.messages.filter((message) => message.role === "user");
    assert.deepEqual(
      userMessages.map((message) => message.text),
      ["images", "thanks"],
    );
    const [first, second] = userMessages;
    assert.deepEqual(
      (first?.images ?? []).map((image) => [image.sha256, image.mimeType]),
      [
        [sha256Of(png), "image/png"],
        [sha256Of(jpeg), "image/jpeg"],
      ],
    );
    const mediaUri = pathToFileURL(join(home.mediaDirectory, "sha256")).href;
    assert.ok((first?.images ?? []).every((image) => image.uri.startsWith(mediaUri)), JSON.stringify(first?.images));
    assert.deepEqual(second?.images ?? [], []);
  });

  test("an invalid image file attaches nothing", async () => {
    const home = newHome(noConnectors);
    writeFileSync(join(home.directory, "notes.txt"), "not an image");
    const oversized = join(home.directory, "huge.png");
    // A sparse file: the size check must refuse it before reading it.
    const descriptor = openSync(oversized, "w");
    writeSync(descriptor, pngBytes());
    ftruncateSync(descriptor, maxImageBytes + 1);
    closeSync(descriptor);
    const cli = start(home);
    await cli.waitForSession();
    cli.writeLine("/image");
    await cli.stdout.waitFor("Image not attached: usage: /image <path>");
    cli.writeLine("/image ~/missing.png");
    await cli.stdout.waitFor(`Image not attached: ${join(home.directory, "missing.png")} does not exist`);
    cli.writeLine("/image ~/notes.txt");
    await cli.stdout.waitFor(`Image not attached: ${join(home.directory, "notes.txt")}: image is not PNG, JPEG, GIF, or WebP`);
    cli.writeLine("/image ~/huge.png");
    await cli.stdout.waitFor(`Image not attached: ${oversized} has 3,750,001 bytes; an image may have at most 3,750,000`);
    cli.writeLine("/image ~/");
    await cli.stdout.waitFor(`Image not attached: ${home.directory} is not a regular file`);
    cli.writeLine("images");
    await cli.stdout.waitFor("saw no images");
    cli.writeLine("/exit");
    assert.equal(await cli.waitForExit(), 0);
    const stored = join(home.mediaDirectory, "sha256");
    assert.ok(!existsSync(stored) || readdirSync(stored).length === 0);
  });

  test("a harness without image support refuses the message and says so", async () => {
    const home = newHome(noConnectors, scriptedHarnessWithoutImages);
    writeFileSync(join(home.directory, "chart.png"), pngBytes());
    const cli = start(home);
    await cli.waitForSession();
    cli.writeLine("/image ~/chart.png");
    await cli.stdout.waitFor("Attached chart.png");
    cli.writeLine("what is this?");
    await cli.stdout.waitFor(
      "Not sent: this agent does not take images. The attached images (1 image) were removed; send the message again without them.",
    );
    cli.writeLine("what is this?");
    await cli.stdout.waitFor("echo: what is this?");
    cli.writeLine("/exit");
    assert.equal(await cli.waitForExit(), 0);
  });

  test("an s3 media store needs the AWS SDK package", async () => {
    // This package installs @superdurable/dex-agents without its optional @aws-sdk/client-s3 peer.
    const home = newHome(`${noConnectors}media: {store: "s3://agent-media"}\n`);
    const cli = start(home);
    assert.equal(await cli.waitForExit(), 2);
    assert.match(
      cli.stderr.all,
      /media\.store s3:\/\/agent-media needs the @aws-sdk\/client-s3 package next to @superdurable\/dex-agents; install it with: npm install @aws-sdk\/client-s3/,
    );
  });

  test("an endpoint without an s3 store is a configuration error", async () => {
    const home = newHome('media: {endpoint: "http://127.0.0.1:9000"}\n');
    const cli = start(home);
    assert.equal(await cli.waitForExit(), 2);
    assert.match(cli.stderr.all, /invalid: schema at \/media \(not\)/);
  });
});
