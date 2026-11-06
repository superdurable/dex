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

import com.google.common.net.InetAddresses;
import io.grpc.ManagedChannelBuilder;
import io.grpc.netty.shaded.io.grpc.netty.GrpcSslContexts;
import io.grpc.netty.shaded.io.grpc.netty.NettyChannelBuilder;
import io.grpc.netty.shaded.io.netty.handler.ssl.ApplicationProtocolConfig;

import javax.net.ssl.TrustManager;
import javax.net.ssl.TrustManagerFactory;
import javax.net.ssl.X509TrustManager;
import java.io.ByteArrayInputStream;
import java.io.IOException;
import java.nio.charset.StandardCharsets;
import java.security.GeneralSecurityException;
import java.security.KeyStore;
import java.security.cert.CertificateException;
import java.security.cert.CertificateFactory;
import java.security.cert.X509Certificate;
import java.util.ArrayList;
import java.util.List;
import java.util.Locale;
import java.util.regex.Matcher;
import java.util.regex.Pattern;

final class FlowServiceTlsTransport {
    private static final Pattern PEM_CERTIFICATE = Pattern.compile(
            "-----BEGIN CERTIFICATE-----.*?-----END CERTIFICATE-----", Pattern.DOTALL);
    private static final Pattern INVALID_DNS_AUTHORITY_CHARACTERS = Pattern.compile("[\\s?#@]");
    private static final Pattern INVALID_NAME_CHARACTERS = Pattern.compile("[\\s/#?@\\[\\]]");

    private FlowServiceTlsTransport() {
    }

    static ManagedChannelBuilder<?> channelBuilder(final String address, final FlowServiceTlsOptions options) {
        if (options == null) {
            return ManagedChannelBuilder.forTarget(address).usePlaintext();
        }
        final String target = validateTarget(address);
        try {
            final NettyChannelBuilder builder = NettyChannelBuilder.forTarget(target)
                    .sslContext(GrpcSslContexts.forClient()
                            .protocols("TLSv1.3", "TLSv1.2")
                            .applicationProtocolConfig(new ApplicationProtocolConfig(
                                    ApplicationProtocolConfig.Protocol.ALPN,
                                    ApplicationProtocolConfig.SelectorFailureBehavior.NO_ADVERTISE,
                                    ApplicationProtocolConfig.SelectedListenerFailureBehavior.ACCEPT, "h2"))
                            .trustManager(trustManager(options.getAdditionalRootCAsPem()))
                            .build());
            if (options.getServerName() != null) {
                builder.overrideAuthority(options.getServerName());
            }
            return builder;
        } catch (GeneralSecurityException | IOException failure) {
            throw new IllegalArgumentException("cannot initialize flowServiceTls using JDK default roots and additional root CAs", failure);
        }
    }

    static String validateTarget(final String address) {
        if (address == null) {
            throw unsupportedAddress();
        }
        String endpoint = address;
        String target = address;
        final int schemeEnd = address.indexOf("://");
        if (schemeEnd >= 0 && address.substring(0, schemeEnd).equalsIgnoreCase("dns")) {
            final String dnsTarget = address.substring(schemeEnd + 3);
            final int authorityEnd = dnsTarget.indexOf('/');
            if (authorityEnd < 0 || INVALID_DNS_AUTHORITY_CHARACTERS.matcher(dnsTarget.substring(0, authorityEnd)).find()) {
                throw unsupportedAddress();
            }
            endpoint = dnsTarget.substring(authorityEnd + 1);
            target = "dns://" + dnsTarget;
        } else {
            final int colon = address.indexOf(':');
            final String scheme = colon < 0 ? "" : address.substring(0, colon).toLowerCase(Locale.ROOT);
            if (scheme.equals("dns") || scheme.equals("unix") || scheme.equals("unix-abstract")
                    || scheme.equals("passthrough") || scheme.equals("ipv4") || scheme.equals("ipv6")) {
                throw unsupportedAddress();
            }
        }
        if (endpoint.indexOf('/') >= 0 || endpoint.indexOf('?') >= 0 || endpoint.indexOf('#') >= 0) {
            throw unsupportedAddress();
        }
        final int separator = endpoint.lastIndexOf(':');
        if (separator < 0) {
            throw unsupportedAddress();
        }
        String host = endpoint.substring(0, separator);
        if (host.startsWith("[") && host.endsWith("]")) {
            host = host.substring(1, host.length() - 1);
            if (!host.contains(":") || !InetAddresses.isInetAddress(host)) {
                throw unsupportedAddress();
            }
        } else if (host.contains(":")) {
            throw unsupportedAddress();
        }
        if (!isValidServerName(host)) {
            throw unsupportedAddress();
        }
        final String port = endpoint.substring(separator + 1);
        try {
            if (!port.matches("[0-9]+") || Integer.parseInt(port) < 1 || Integer.parseInt(port) > 65535) {
                throw new NumberFormatException();
            }
        } catch (NumberFormatException failure) {
            throw new IllegalArgumentException("flowServiceTls requires serverAddress port 1-65535");
        }
        return target;
    }

    static boolean isValidServerName(final String name) {
        return !name.isEmpty() && !INVALID_NAME_CHARACTERS.matcher(name).find()
                && (!name.contains(":") || (InetAddresses.isInetAddress(name) && !name.contains("%")));
    }

    static List<X509Certificate> parseAdditionalRootCertificates(final String pem) {
        final List<X509Certificate> certificates = new ArrayList<X509Certificate>();
        if (pem == null || pem.isEmpty()) {
            return certificates;
        }
        try {
            final CertificateFactory factory = CertificateFactory.getInstance("X.509");
            final Matcher matcher = PEM_CERTIFICATE.matcher(pem);
            while (matcher.find()) {
                try {
                    certificates.add((X509Certificate) factory.generateCertificate(new ByteArrayInputStream(
                            matcher.group().getBytes(StandardCharsets.US_ASCII))));
                } catch (CertificateException failure) {
                    // Match Go's PEM bundle behavior: a later valid certificate can still supply a root.
                }
            }
        } catch (CertificateException failure) {
            throw new IllegalArgumentException("cannot initialize flowServiceTls X.509 certificate parser", failure);
        }
        if (certificates.isEmpty()) {
            throw new IllegalArgumentException("flowServiceTls.additionalRootCAsPem contains no PEM certificate");
        }
        return certificates;
    }

    static X509TrustManager trustManager(final String additionalRootCAsPem)
            throws GeneralSecurityException, IOException {
        final TrustManagerFactory factory = TrustManagerFactory.getInstance(TrustManagerFactory.getDefaultAlgorithm());
        factory.init((KeyStore) null);
        final X509TrustManager defaultTrustManager = x509TrustManager(factory);
        final List<X509Certificate> additionalRoots = parseAdditionalRootCertificates(additionalRootCAsPem);
        if (additionalRoots.isEmpty()) {
            return defaultTrustManager;
        }
        // Keep every active JDK trust anchor, including javax.net.ssl.trustStore overrides, before adding roots.
        final KeyStore combinedRoots = KeyStore.getInstance("JKS");
        combinedRoots.load(null, null);
        int certificateIndex = 0;
        for (final X509Certificate certificate : defaultTrustManager.getAcceptedIssuers()) {
            combinedRoots.setCertificateEntry("default-" + certificateIndex++, certificate);
        }
        for (final X509Certificate certificate : additionalRoots) {
            combinedRoots.setCertificateEntry("additional-" + certificateIndex++, certificate);
        }
        factory.init(combinedRoots);
        return x509TrustManager(factory);
    }

    private static X509TrustManager x509TrustManager(final TrustManagerFactory factory) {
        for (final TrustManager manager : factory.getTrustManagers()) {
            if (manager instanceof X509TrustManager) {
                return (X509TrustManager) manager;
            }
        }
        throw new IllegalArgumentException("flowServiceTls requires a JDK default X.509 trust manager");
    }

    private static IllegalArgumentException unsupportedAddress() {
        return new IllegalArgumentException("flowServiceTls requires serverAddress as host:port, dns:///host:port, or dns://authority/host:port");
    }
}
