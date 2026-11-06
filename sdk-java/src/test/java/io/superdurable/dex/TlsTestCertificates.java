/*
 * Copyright (c) 2026 Super Durable, Inc.
 *
 * Licensed under the Sustainable Use License 1.0.
 * You may not use this file except in compliance with the License.
 * See the LICENSE file in the repository root.
 *
 * SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0
 */

package io.superdurable.dex;

import java.io.IOException;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.List;
import java.util.concurrent.TimeUnit;

final class TlsTestCertificates {
    final Path certificate;
    final Path privateKey;
    final String caPem;

    private TlsTestCertificates(final Path certificate, final Path privateKey, final String caPem) {
        this.certificate = certificate;
        this.privateKey = privateKey;
        this.caPem = caPem;
    }

    static TlsTestCertificates create(final Path directory, final String name) throws Exception {
        Files.createDirectories(directory);
        final Path extensions = directory.resolve(name + "-extensions.cnf");
        Files.write(extensions, Arrays.asList(
                "[ca]", "basicConstraints = critical, CA:TRUE", "keyUsage = critical, keyCertSign",
                "[server]", "basicConstraints = CA:FALSE", "keyUsage = critical, digitalSignature",
                "extendedKeyUsage = serverAuth", "subjectAltName = DNS:localhost, DNS:dex.test, IP:127.0.0.1, IP:::1"),
                StandardCharsets.US_ASCII);
        final Path caKey = directory.resolve(name + "-ca.key");
        final Path caCsr = directory.resolve(name + "-ca.csr");
        final Path caCertificate = directory.resolve(name + "-ca.pem");
        final Path serverKey = directory.resolve(name + "-server.key");
        final Path serverCsr = directory.resolve(name + "-server.csr");
        final Path serverCertificate = directory.resolve(name + "-server.pem");
        final Path pkcs8Key = directory.resolve(name + "-pkcs8.key");
        openssl(directory, "ecparam", "-name", "prime256v1", "-genkey", "-noout", "-out", caKey.toString());
        openssl(directory, "req", "-new", "-key", caKey.toString(), "-subj", "/CN=Dex test CA " + name,
                "-out", caCsr.toString());
        openssl(directory, "x509", "-req", "-in", caCsr.toString(), "-signkey", caKey.toString(), "-days", "1",
                "-extfile", extensions.toString(), "-extensions", "ca", "-out", caCertificate.toString());
        openssl(directory, "ecparam", "-name", "prime256v1", "-genkey", "-noout", "-out", serverKey.toString());
        openssl(directory, "req", "-new", "-key", serverKey.toString(), "-subj", "/CN=localhost",
                "-out", serverCsr.toString());
        openssl(directory, "x509", "-req", "-in", serverCsr.toString(), "-CA", caCertificate.toString(),
                "-CAkey", caKey.toString(), "-set_serial", "2", "-days", "1", "-extfile", extensions.toString(),
                "-extensions", "server", "-out", serverCertificate.toString());
        openssl(directory, "pkcs8", "-topk8", "-nocrypt", "-in", serverKey.toString(), "-out", pkcs8Key.toString());
        return new TlsTestCertificates(serverCertificate, pkcs8Key,
                new String(Files.readAllBytes(caCertificate), StandardCharsets.US_ASCII));
    }

    private static void openssl(final Path directory, final String... arguments) throws Exception {
        final List<String> command = new ArrayList<String>();
        command.add("openssl");
        command.addAll(Arrays.asList(arguments));
        final Path output = directory.resolve("openssl.log");
        final Process process = new ProcessBuilder(command).redirectErrorStream(true)
                .redirectOutput(output.toFile()).start();
        try {
            if (!process.waitFor(20, TimeUnit.SECONDS)) {
                throw new IOException("openssl test certificate generation timed out");
            }
            if (process.exitValue() != 0) {
                throw new IOException("openssl test certificate generation failed; see " + output);
            }
        } finally {
            process.destroyForcibly();
            process.waitFor();
        }
    }
}
