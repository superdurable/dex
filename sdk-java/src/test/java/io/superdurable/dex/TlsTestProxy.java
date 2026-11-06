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

import io.grpc.netty.shaded.io.netty.bootstrap.Bootstrap;
import io.grpc.netty.shaded.io.netty.bootstrap.ServerBootstrap;
import io.grpc.netty.shaded.io.netty.channel.Channel;
import io.grpc.netty.shaded.io.netty.channel.ChannelFuture;
import io.grpc.netty.shaded.io.netty.channel.ChannelHandlerContext;
import io.grpc.netty.shaded.io.netty.channel.ChannelInboundHandlerAdapter;
import io.grpc.netty.shaded.io.netty.channel.ChannelInitializer;
import io.grpc.netty.shaded.io.netty.channel.EventLoopGroup;
import io.grpc.netty.shaded.io.netty.channel.nio.NioEventLoopGroup;
import io.grpc.netty.shaded.io.netty.channel.socket.SocketChannel;
import io.grpc.netty.shaded.io.netty.channel.socket.nio.NioServerSocketChannel;
import io.grpc.netty.shaded.io.netty.channel.socket.nio.NioSocketChannel;
import io.grpc.netty.shaded.io.netty.handler.ssl.ApplicationProtocolConfig;
import io.grpc.netty.shaded.io.netty.handler.ssl.SslContext;
import io.grpc.netty.shaded.io.netty.handler.ssl.SslContextBuilder;
import io.grpc.netty.shaded.io.netty.handler.ssl.SslHandler;
import io.grpc.netty.shaded.io.netty.handler.ssl.SslHandshakeCompletionEvent;
import io.grpc.netty.shaded.io.netty.handler.ssl.SslProvider;
import io.grpc.netty.shaded.io.netty.util.ReferenceCountUtil;

import javax.net.ssl.ExtendedSSLSession;
import javax.net.ssl.SNIHostName;
import javax.net.ssl.SNIServerName;
import java.net.InetSocketAddress;
import java.util.List;
import java.util.concurrent.CopyOnWriteArrayList;
import java.util.concurrent.ConcurrentHashMap;
import java.util.Set;

final class TlsTestProxy implements AutoCloseable {
    private final EventLoopGroup eventLoops = new NioEventLoopGroup(2);
    private final Set<Channel> connections = ConcurrentHashMap.newKeySet();
    private final List<String> handshakes = new CopyOnWriteArrayList<String>();
    private final String upstreamHost;
    private final int upstreamPort;
    private Channel listener;

    TlsTestProxy(final TlsTestCertificates certificates, final String upstreamHost,
            final int upstreamPort, final String... protocols) throws Exception {
        this.upstreamHost = upstreamHost;
        this.upstreamPort = upstreamPort;
        final ApplicationProtocolConfig alpn = protocols.length == 0 ? ApplicationProtocolConfig.DISABLED
                : new ApplicationProtocolConfig(ApplicationProtocolConfig.Protocol.ALPN,
                        ApplicationProtocolConfig.SelectorFailureBehavior.NO_ADVERTISE,
                        ApplicationProtocolConfig.SelectedListenerFailureBehavior.ACCEPT, protocols);
        try {
            final SslContext context = SslContextBuilder.forServer(
                    certificates.certificate.toFile(), certificates.privateKey.toFile())
                    .sslProvider(SslProvider.OPENSSL).protocols("TLSv1.2")
                    .applicationProtocolConfig(alpn).build();
            listener = new ServerBootstrap().group(eventLoops).channel(NioServerSocketChannel.class)
                    .childHandler(new ProxyInitializer(context)).bind("127.0.0.1", 0).sync().channel();
        } catch (Exception failure) {
            close();
            throw failure;
        }
    }

    int port() {
        return ((InetSocketAddress) listener.localAddress()).getPort();
    }

    boolean hasHandshake(final String serverName, final String alpn) {
        return handshakes.contains(serverName + ":" + alpn);
    }

    String handshakeDescriptions() {
        return handshakes.toString();
    }

    @Override
    public void close() {
        if (listener != null) {
            listener.close().syncUninterruptibly();
        }
        for (final Channel connection : connections) {
            connection.close().syncUninterruptibly();
        }
        eventLoops.shutdownGracefully(0, 5, java.util.concurrent.TimeUnit.SECONDS).syncUninterruptibly();
    }

    private final class ProxyInitializer extends ChannelInitializer<SocketChannel> {
        private final SslContext context;

        private ProxyInitializer(final SslContext context) {
            this.context = context;
        }

        @Override
        protected void initChannel(final SocketChannel channel) {
            connections.add(channel);
            channel.closeFuture().addListener(closed -> connections.remove(channel));
            channel.pipeline().addLast(context.newHandler(channel.alloc()), new DownstreamRelay());
        }
    }

    private final class DownstreamRelay extends ChannelInboundHandlerAdapter {
        private Channel upstream;

        @Override
        public void channelActive(final ChannelHandlerContext context) {
            final Channel downstream = context.channel();
            downstream.config().setAutoRead(false);
            final ChannelFuture connected = new Bootstrap().group(downstream.eventLoop())
                    .channel(NioSocketChannel.class).handler(new UpstreamInitializer(downstream))
                    .connect(upstreamHost, upstreamPort);
            upstream = connected.channel();
            connections.add(upstream);
            upstream.closeFuture().addListener(closed -> connections.remove(upstream));
            connected.addListener(completed -> {
                if (completed.isSuccess()) {
                    downstream.config().setAutoRead(true);
                } else {
                    downstream.close();
                }
            });
        }

        @Override
        public void channelRead(final ChannelHandlerContext context, final Object message) {
            // Forward non-h2 sessions too, so the client must enforce ALPN itself.
            if (upstream.isActive()) {
                upstream.writeAndFlush(message);
            } else {
                ReferenceCountUtil.release(message);
                context.close();
            }
        }

        @Override
        public void userEventTriggered(final ChannelHandlerContext context, final Object event) throws Exception {
            if (event instanceof SslHandshakeCompletionEvent && ((SslHandshakeCompletionEvent) event).isSuccess()) {
                final SslHandler ssl = context.pipeline().get(SslHandler.class);
                String serverName = "";
                for (final SNIServerName name : ((ExtendedSSLSession) ssl.engine().getSession()).getRequestedServerNames()) {
                    if (name instanceof SNIHostName) {
                        serverName = ((SNIHostName) name).getAsciiName();
                    }
                }
                handshakes.add(serverName + ":" + ssl.applicationProtocol());
            }
            super.userEventTriggered(context, event);
        }

        @Override
        public void channelInactive(final ChannelHandlerContext context) {
            if (upstream != null) {
                upstream.close();
            }
        }

        @Override
        public void exceptionCaught(final ChannelHandlerContext context, final Throwable cause) {
            context.close();
        }
    }

    private static final class UpstreamInitializer extends ChannelInitializer<SocketChannel> {
        private final Channel downstream;

        private UpstreamInitializer(final Channel downstream) {
            this.downstream = downstream;
        }

        @Override
        protected void initChannel(final SocketChannel channel) {
            channel.pipeline().addLast(new UpstreamRelay(downstream));
        }
    }

    private static final class UpstreamRelay extends ChannelInboundHandlerAdapter {
        private final Channel downstream;

        private UpstreamRelay(final Channel downstream) {
            this.downstream = downstream;
        }

        @Override
        public void channelRead(final ChannelHandlerContext context, final Object message) {
            if (downstream.isActive()) {
                downstream.writeAndFlush(message);
            } else {
                ReferenceCountUtil.release(message);
                context.close();
            }
        }

        @Override
        public void channelInactive(final ChannelHandlerContext context) {
            downstream.close();
        }

        @Override
        public void exceptionCaught(final ChannelHandlerContext context, final Throwable cause) {
            context.close();
        }
    }
}
