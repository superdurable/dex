// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::net::SocketAddr;
use std::sync::{Arc, Mutex};

use tokio::io::{AsyncReadExt, AsyncWriteExt};
use tokio::net::{TcpListener, TcpStream};
use tokio::task::{JoinHandle, JoinSet};

/// One request that the fake server received.
#[derive(Debug, Clone)]
pub(super) struct RecordedRequest {
    pub(super) method: String,
    /// Path and query, as sent on the request line.
    pub(super) target: String,
    pub(super) headers: Vec<(String, String)>,
}

impl RecordedRequest {
    /// Returns the value of the header `name`, compared case-insensitively.
    pub(super) fn header(&self, name: &str) -> Option<&str> {
        self.headers
            .iter()
            .find(|(header, _)| header.eq_ignore_ascii_case(name))
            .map(|(_, value)| value.as_str())
    }
}

/// How the fake server answers one request.
pub(super) enum FakeResponse {
    /// Sends a response. A `content-length` header in `headers` replaces the body length.
    Reply {
        status: u16,
        headers: Vec<(&'static str, String)>,
        body: Vec<u8>,
    },
    /// Reads the whole request, then closes the connection without answering.
    DropConnection,
}

impl FakeResponse {
    pub(super) fn status(status: u16) -> Self {
        Self::Reply {
            status,
            headers: Vec::new(),
            body: Vec::new(),
        }
    }

    pub(super) fn with_headers(status: u16, headers: Vec<(&'static str, String)>) -> Self {
        Self::Reply {
            status,
            headers,
            body: Vec::new(),
        }
    }
}

type Responder = Box<dyn FnMut(&RecordedRequest) -> FakeResponse + Send>;

/// A scripted HTTP/1.1 server standing in for Azure Blob Storage or GCS.
pub(super) struct FakeCloudServer {
    address: SocketAddr,
    requests: Arc<Mutex<Vec<RecordedRequest>>>,
    accept_task: JoinHandle<()>,
}

impl FakeCloudServer {
    pub(super) async fn start(
        responder: impl FnMut(&RecordedRequest) -> FakeResponse + Send + 'static,
    ) -> Self {
        let listener = TcpListener::bind("127.0.0.1:0")
            .await
            .expect("the fake cloud server binds");
        let address = listener
            .local_addr()
            .expect("the fake server has an address");
        let requests = Arc::new(Mutex::new(Vec::new()));
        let responder: Arc<Mutex<Responder>> = Arc::new(Mutex::new(Box::new(responder)));
        let recorded = Arc::clone(&requests);
        let accept_task = tokio::spawn(async move {
            let mut connection_tasks = JoinSet::new();
            loop {
                tokio::select! {
                    accepted = listener.accept() => {
                        let (connection, _) = accepted.expect("the fake server accepts connections");
                        let recorded = Arc::clone(&recorded);
                        let responder = Arc::clone(&responder);
                        connection_tasks.spawn(serve_connection(connection, recorded, responder));
                    }
                    completed = connection_tasks.join_next(), if !connection_tasks.is_empty() => {
                        completed.expect("a connection task completed")
                            .expect("the fake server connection task remains healthy");
                    }
                }
            }
        });
        Self {
            address,
            requests,
            accept_task,
        }
    }

    pub(super) fn endpoint(&self) -> String {
        format!("http://{}", self.address)
    }

    pub(super) fn requests(&self) -> Vec<RecordedRequest> {
        self.requests
            .lock()
            .expect("fake server records remain available")
            .clone()
    }

    /// Returns the recorded requests with `method`, excluding none.
    pub(super) fn requests_with_method(&self, method: &str) -> Vec<RecordedRequest> {
        self.requests()
            .into_iter()
            .filter(|request| request.method == method)
            .collect()
    }
}

impl Drop for FakeCloudServer {
    fn drop(&mut self) {
        self.accept_task.abort();
    }
}

async fn serve_connection(
    mut connection: TcpStream,
    recorded: Arc<Mutex<Vec<RecordedRequest>>>,
    responder: Arc<Mutex<Responder>>,
) {
    let mut buffer = Vec::new();
    loop {
        let Some(request) = read_request(&mut connection, &mut buffer).await else {
            return;
        };
        recorded
            .lock()
            .expect("fake server records remain available")
            .push(request.clone());
        let response = (responder.lock().expect("the responder remains available"))(&request);
        match response {
            FakeResponse::DropConnection => return,
            FakeResponse::Reply {
                status,
                headers,
                body,
            } => {
                let has_length = headers
                    .iter()
                    .any(|(name, _)| name.eq_ignore_ascii_case("content-length"));
                let mut head = format!("HTTP/1.1 {status} Fake\r\n");
                for (name, value) in &headers {
                    head.push_str(&format!("{name}: {value}\r\n"));
                }
                if !has_length {
                    head.push_str(&format!("content-length: {}\r\n", body.len()));
                }
                head.push_str("\r\n");
                if connection.write_all(head.as_bytes()).await.is_err()
                    || connection.write_all(&body).await.is_err()
                {
                    return;
                }
            }
        }
    }
}

/// Reads one request, or returns `None` when the client closed the connection.
async fn read_request(connection: &mut TcpStream, buffer: &mut Vec<u8>) -> Option<RecordedRequest> {
    let header_end = loop {
        if let Some(end) = buffer.windows(4).position(|window| window == b"\r\n\r\n") {
            break end;
        }
        let mut chunk = [0_u8; 16 * 1024];
        let count = connection.read(&mut chunk).await.ok()?;
        if count == 0 {
            return None;
        }
        buffer.extend_from_slice(&chunk[..count]);
    };
    let head = String::from_utf8(buffer[..header_end].to_vec()).expect("request headers are UTF-8");
    let mut lines = head.split("\r\n");
    let mut request_line = lines.next().expect("a request line").split(' ');
    let method = request_line.next().expect("a method").to_owned();
    let target = request_line.next().expect("a target").to_owned();
    let headers: Vec<(String, String)> = lines
        .filter_map(|line| line.split_once(':'))
        .map(|(name, value)| (name.trim().to_owned(), value.trim().to_owned()))
        .collect();
    let body_length: usize = headers
        .iter()
        .find(|(name, _)| name.eq_ignore_ascii_case("content-length"))
        .map_or(0, |(_, value)| {
            value.parse().expect("a numeric content-length")
        });
    let request_end = header_end + 4 + body_length;
    while buffer.len() < request_end {
        let mut chunk = [0_u8; 64 * 1024];
        let count = connection.read(&mut chunk).await.ok()?;
        if count == 0 {
            return None;
        }
        buffer.extend_from_slice(&chunk[..count]);
    }
    buffer.drain(..request_end);
    Some(RecordedRequest {
        method,
        target,
        headers,
    })
}
