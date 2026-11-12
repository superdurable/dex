// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::net::SocketAddr;
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{Arc, Mutex};

use tokio::io::{AsyncReadExt, AsyncWriteExt};
use tokio::net::{TcpListener, TcpStream};
use tokio::task::{JoinHandle, JoinSet};

type RecordedConnection = Arc<Mutex<Vec<u8>>>;
type RecordedConnections = Arc<Mutex<Vec<RecordedConnection>>>;

pub(super) struct RecordingS3Proxy {
    address: SocketAddr,
    connections: RecordedConnections,
    accept_task: Option<JoinHandle<()>>,
}

/// Which single byte, if any, the proxy alters while forwarding.
#[derive(Clone, Copy, PartialEq, Eq)]
pub(super) enum ProxyTampering {
    ForwardUnchanged,
    CorruptFirstUploadPartBody,
    CorruptFirstResponseEtag,
}

impl RecordingS3Proxy {
    pub(super) async fn start(upstream_address: SocketAddr) -> Self {
        Self::start_with_forwarding(upstream_address, ProxyTampering::ForwardUnchanged).await
    }

    pub(super) async fn start_with_forwarding(
        upstream_address: SocketAddr,
        tampering: ProxyTampering,
    ) -> Self {
        let listener = TcpListener::bind("127.0.0.1:0")
            .await
            .expect("the recording proxy binds");
        let address = listener.local_addr().expect("the proxy has an address");
        let connections = Arc::new(Mutex::new(Vec::new()));
        let recorded_connections = Arc::clone(&connections);
        let has_tampered = Arc::new(AtomicBool::new(
            tampering == ProxyTampering::ForwardUnchanged,
        ));
        let accept_task = tokio::spawn(async move {
            let mut connection_tasks = JoinSet::new();
            loop {
                tokio::select! {
                    accepted = listener.accept() => {
                        let (client, _) = accepted.expect("the recording proxy accepts connections");
                        let recorded_bytes = Arc::new(Mutex::new(Vec::new()));
                        recorded_connections.lock().expect("proxy records remain available")
                            .push(Arc::clone(&recorded_bytes));
                        let has_tampered = Arc::clone(&has_tampered);
                        connection_tasks.spawn(async move {
                            if let Err(error) = forward_connection(client, upstream_address, recorded_bytes, tampering, has_tampered).await {
                                tracing::debug!(?error, "recording proxy connection closed");
                            }
                        });
                    }
                    completed = connection_tasks.join_next(), if !connection_tasks.is_empty() => {
                        completed.expect("a connection task completed")
                            .expect("the recording proxy task remains healthy");
                    }
                }
            }
        });
        Self {
            address,
            connections,
            accept_task: Some(accept_task),
        }
    }

    pub(super) fn address(&self) -> SocketAddr {
        self.address
    }

    pub(super) fn multipart_request_headers(&self) -> Vec<String> {
        self.request_headers(&[
            b"POST /anonymous-file-uploads/multipart?".as_slice(),
            b"PUT /anonymous-file-uploads/multipart?".as_slice(),
        ])
    }

    /// Returns the header blocks of recorded requests whose request line starts with a prefix.
    pub(super) fn request_headers(&self, request_line_prefixes: &[&[u8]]) -> Vec<String> {
        let connections = self
            .connections
            .lock()
            .expect("proxy records remain available");
        let mut requests = Vec::new();
        for connection in connections.iter() {
            let bytes = connection.lock().expect("proxy bytes remain available");
            for prefix in request_line_prefixes.iter().copied() {
                for (index, window) in bytes.windows(prefix.len()).enumerate() {
                    if window != prefix {
                        continue;
                    }
                    let request = &bytes[index..];
                    let Some(end) = request.windows(4).position(|window| window == b"\r\n\r\n")
                    else {
                        continue;
                    };
                    if end > 16 * 1024 {
                        continue;
                    }
                    let header = std::str::from_utf8(&request[..end])
                        .expect("recorded request headers are UTF-8");
                    requests.push(header.to_owned());
                }
            }
        }
        requests
    }

    pub(super) async fn shutdown(mut self) {
        let accept_task = self.accept_task.take().expect("the proxy task is active");
        accept_task.abort();
        match accept_task.await {
            Err(error) if error.is_cancelled() => {}
            result => panic!("the recording proxy must stop cleanly: {result:?}"),
        }
    }
}

impl Drop for RecordingS3Proxy {
    fn drop(&mut self) {
        if let Some(accept_task) = &self.accept_task {
            accept_task.abort();
        }
    }
}

async fn forward_connection(
    client: TcpStream,
    upstream_address: SocketAddr,
    recorded_bytes: RecordedConnection,
    tampering: ProxyTampering,
    has_tampered: Arc<AtomicBool>,
) -> std::io::Result<()> {
    let upstream = TcpStream::connect(upstream_address).await?;
    let (mut client_reader, mut client_writer) = client.into_split();
    let (mut upstream_reader, mut upstream_writer) = upstream.into_split();
    let request_forward = async {
        let mut buffer = [0_u8; 16 * 1024];
        let mut forwarded_length = 0;
        let mut corrupted_offset = None;
        loop {
            let count = client_reader.read(&mut buffer).await?;
            if count == 0 {
                upstream_writer.shutdown().await?;
                break;
            }
            {
                let mut recorded = recorded_bytes.lock().expect("proxy bytes remain available");
                recorded.extend_from_slice(&buffer[..count]);
                if tampering == ProxyTampering::CorruptFirstUploadPartBody
                    && corrupted_offset.is_none()
                    && !has_tampered.load(Ordering::Acquire)
                {
                    corrupted_offset = first_upload_part_body_offset(&recorded);
                }
            }
            if let Some(offset) = corrupted_offset
                && (forwarded_length..forwarded_length + count).contains(&offset)
                && !has_tampered.swap(true, Ordering::AcqRel)
            {
                buffer[offset - forwarded_length] ^= 0xff;
            }
            forwarded_length += count;
            upstream_writer.write_all(&buffer[..count]).await?;
        }
        Ok::<(), std::io::Error>(())
    };
    let response_forward = async {
        if tampering != ProxyTampering::CorruptFirstResponseEtag {
            tokio::io::copy(&mut upstream_reader, &mut client_writer).await?;
            client_writer.shutdown().await?;
            return Ok::<(), std::io::Error>(());
        }
        let mut buffer = [0_u8; 16 * 1024];
        let mut received = Vec::new();
        let mut corrupted_offset = None;
        loop {
            let count = upstream_reader.read(&mut buffer).await?;
            if count == 0 {
                client_writer.shutdown().await?;
                break;
            }
            let forwarded_length = received.len();
            received.extend_from_slice(&buffer[..count]);
            if corrupted_offset.is_none() && !has_tampered.load(Ordering::Acquire) {
                corrupted_offset = first_etag_header_value_offset(&received);
            }
            if let Some(offset) = corrupted_offset
                && (forwarded_length..received.len()).contains(&offset)
                && !has_tampered.swap(true, Ordering::AcqRel)
            {
                let byte = &mut buffer[offset - forwarded_length];
                *byte = if *byte == b'0' { b'1' } else { b'0' };
            }
            client_writer.write_all(&buffer[..count]).await?;
        }
        Ok(())
    };
    tokio::try_join!(request_forward, response_forward)?;
    Ok(())
}

/// Returns the stream offset of the first body byte of the first UploadPart request.
fn first_upload_part_body_offset(bytes: &[u8]) -> Option<usize> {
    let marker = b"partNumber=";
    let request_line = bytes
        .windows(marker.len())
        .position(|window| window == marker)?;
    let header_end = bytes[request_line..]
        .windows(4)
        .position(|window| window == b"\r\n\r\n")?;
    Some(request_line + header_end + 4)
}

/// Returns the stream offset of the first character inside the first quoted `etag` header.
fn first_etag_header_value_offset(bytes: &[u8]) -> Option<usize> {
    let marker = b"\r\netag: \"";
    let header = bytes
        .windows(marker.len())
        .position(|window| window == marker)?;
    Some(header + marker.len())
}
