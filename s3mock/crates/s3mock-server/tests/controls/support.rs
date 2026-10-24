// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Isolated loopback server and HTTP admin client for testing controls.

use std::sync::Arc;
use std::time::Duration;

use aws_sdk_s3::Client;
use aws_sdk_s3::config::{
    BehaviorVersion, Credentials, Region, RequestChecksumCalculation, ResponseChecksumValidation,
    retry::RetryConfig,
};
use clap::Parser;
use hmac::{Hmac, Mac};
use s3mock::{Durability, Store, StoreConfig};
use s3mock_server::{ServerConfig, ServerHandle};
use serde_json::{Value, json};
use sha2::{Digest, Sha256};
use tokio::io::{AsyncReadExt, AsyncWriteExt};
use tokio::net::TcpStream;

pub(crate) struct RunningServer {
    pub(crate) server: ServerHandle,
    pub(crate) client: Client,
    pub(crate) directory: tempfile::TempDir,
    is_strict: bool,
}

impl RunningServer {
    pub(crate) async fn start(arguments: &[&str]) -> Self {
        let directory = tempfile::tempdir().expect("the controls integration fixture succeeds");
        let config =
            ServerConfig::parse_from(std::iter::once("s3mock").chain(arguments.iter().copied()));
        let store = Store::open(&StoreConfig {
            root_directory: directory.path().to_path_buf(),
            durability: Durability::Relaxed,
            etag_mode: config.etag_mode.into(),
        })
        .expect("the controls integration fixture succeeds");
        store
            .create_bucket("testing", false)
            .expect("the controls integration fixture succeeds");
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0")
            .await
            .expect("the controls integration fixture succeeds");
        let server = s3mock_server::start_with_config(Arc::new(store), listener, config)
            .expect("the controls integration fixture succeeds");
        let client = client(&server, "s3mock", "s3mock");
        Self {
            server,
            client,
            directory,
            is_strict: arguments
                .windows(2)
                .any(|pair| pair == ["--signature-verification", "strict"]),
        }
    }

    pub(crate) async fn raw(
        &self,
        method: &str,
        path: &str,
        headers: &str,
        body: &[u8],
    ) -> Vec<u8> {
        self.raw_with_host(
            method,
            path,
            headers,
            body,
            &self.server.local_address().to_string(),
        )
        .await
    }

    pub(crate) async fn raw_with_host(
        &self,
        method: &str,
        path: &str,
        headers: &str,
        body: &[u8],
        host: &str,
    ) -> Vec<u8> {
        let mut stream = TcpStream::connect(self.server.local_address())
            .await
            .expect("the controls integration fixture succeeds");
        let head = format!(
            "{method} {path} HTTP/1.1\r\nhost: {}\r\nconnection: close\r\ncontent-length: {}\r\n{headers}\r\n",
            host,
            body.len()
        );
        stream
            .write_all(head.as_bytes())
            .await
            .expect("the controls integration fixture succeeds");
        stream
            .write_all(body)
            .await
            .expect("the controls integration fixture succeeds");
        let mut response = Vec::new();
        let result =
            tokio::time::timeout(Duration::from_secs(10), stream.read_to_end(&mut response))
                .await
                .expect("the controls integration fixture succeeds");
        if let Err(error) = result {
            // Early header rejection can close with unread request bytes after sending a complete response.
            assert_eq!(error.kind(), std::io::ErrorKind::ConnectionReset);
            let boundary = response
                .windows(4)
                .position(|window| window == b"\r\n\r\n")
                .expect("response headers arrived before reset");
            let headers = String::from_utf8_lossy(&response[..boundary]);
            let length: usize = headers
                .lines()
                .find_map(|line| line.strip_prefix("content-length: "))
                .expect("error response has content length")
                .parse()
                .expect("content length is numeric");
            assert_eq!(
                response.len() - boundary - 4,
                length,
                "error response is complete"
            );
        }
        response
    }

    pub(crate) async fn raw_signed(
        &self,
        method: &str,
        path: &str,
        access_key_id: &str,
        secret_access_key: &str,
        body: &[u8],
    ) -> Vec<u8> {
        let headers = self.signed_headers(method, path, access_key_id, secret_access_key, body);
        self.raw(method, path, &headers, body).await
    }

    pub(crate) async fn admin(&self, method: &str, path: &str, body: Value) -> (u16, Value) {
        let bytes = if body.is_null() {
            Vec::new()
        } else {
            serde_json::to_vec(&body).expect("the controls integration fixture succeeds")
        };
        let response = self
            .raw(
                method,
                &format!("/_s3mock/{path}"),
                &self.admin_headers(method, path, &bytes),
                &bytes,
            )
            .await;
        let boundary = response
            .windows(4)
            .position(|window| window == b"\r\n\r\n")
            .expect("the controls integration fixture succeeds");
        let status = std::str::from_utf8(&response[9..12])
            .expect("the controls integration fixture succeeds")
            .parse()
            .expect("the controls integration fixture succeeds");
        (
            status,
            serde_json::from_slice(&response[boundary + 4..])
                .expect("the controls integration fixture succeeds"),
        )
    }

    fn admin_headers(&self, method: &str, path: &str, body: &[u8]) -> String {
        if !self.is_strict {
            return "content-type: application/json\r\n".into();
        }
        let mut headers = self.signed_headers(
            method,
            &format!("/_s3mock/{path}"),
            "s3mock",
            "s3mock",
            body,
        );
        headers.push_str("content-type: application/json\r\n");
        headers
    }

    fn signed_headers(
        &self,
        method: &str,
        path: &str,
        access_key_id: &str,
        secret_access_key: &str,
        body: &[u8],
    ) -> String {
        let timestamp = "20261023T000000Z";
        let scope = "20261023/us-east-1/s3/aws4_request";
        let host = self.server.local_address().to_string();
        let payload_hash = hex::encode(Sha256::digest(body));
        let canonical_headers = format!("host:{host}\nx-amz-date:{timestamp}\n");
        let signed_headers = "host;x-amz-date";
        let (raw_path, raw_query) = path.split_once('?').unwrap_or((path, ""));
        let mut query_parts = raw_query
            .split('&')
            .filter(|part| !part.is_empty())
            .map(|part| {
                let (name, value) = part.split_once('=').unwrap_or((part, ""));
                format!("{name}={value}")
            })
            .collect::<Vec<_>>();
        query_parts.sort();
        let canonical_query = query_parts.join("&");
        let canonical = format!(
            "{method}\n{raw_path}\n{canonical_query}\n{canonical_headers}\n{signed_headers}\n{payload_hash}"
        );
        let string_to_sign = format!(
            "AWS4-HMAC-SHA256\n{timestamp}\n{scope}\n{}",
            hex::encode(Sha256::digest(canonical.as_bytes()))
        );
        let signing_key = test_hmac(format!("AWS4{secret_access_key}").as_bytes(), b"20261023");
        let signing_key = test_hmac(&signing_key, b"us-east-1");
        let signing_key = test_hmac(&signing_key, b"s3");
        let signing_key = test_hmac(&signing_key, b"aws4_request");
        let signature = hex::encode(test_hmac(&signing_key, string_to_sign.as_bytes()));
        format!(
            "x-amz-content-sha256: {payload_hash}\r\nx-amz-date: {timestamp}\r\nauthorization: AWS4-HMAC-SHA256 Credential={access_key_id}/{scope}, SignedHeaders={signed_headers}, Signature={signature}\r\n"
        )
    }

    pub(crate) async fn rules(&self, rules: Value) {
        let (status, _) = self.admin("PUT", "faults", json!({"rules": rules})).await;
        assert_eq!(status, 200);
    }

    pub(crate) async fn put(&self, key: &str, body: &'static str) {
        self.client
            .put_object()
            .bucket("testing")
            .key(key)
            .body(body.as_bytes().to_vec().into())
            .send()
            .await
            .expect("the controls integration fixture succeeds");
    }

    pub(crate) async fn get(&self, key: &str) -> Vec<u8> {
        self.client
            .get_object()
            .bucket("testing")
            .key(key)
            .send()
            .await
            .expect("the controls integration fixture succeeds")
            .body
            .collect()
            .await
            .expect("the controls integration fixture succeeds")
            .into_bytes()
            .to_vec()
    }

    pub(crate) async fn requests(&self, count: usize) -> Value {
        let deadline = tokio::time::Instant::now() + Duration::from_secs(5);
        loop {
            let (_, log) = self.admin("GET", "requests", json!(null)).await;
            if log["requests"]
                .as_array()
                .expect("the controls integration fixture succeeds")
                .len()
                >= count
            {
                return log;
            }
            assert!(
                tokio::time::Instant::now() < deadline,
                "request observations did not settle"
            );
            tokio::task::yield_now().await;
        }
    }
}

fn test_hmac(key: &[u8], message: &[u8]) -> Vec<u8> {
    let mut mac = Hmac::<Sha256>::new_from_slice(key).expect("test signing key is valid");
    mac.update(message);
    mac.finalize().into_bytes().to_vec()
}

pub(crate) fn client(server: &ServerHandle, access_key: &str, secret: &str) -> Client {
    Client::from_conf(
        aws_sdk_s3::Config::builder()
            .behavior_version(BehaviorVersion::latest())
            .region(Region::new("us-east-1"))
            .endpoint_url(format!("http://{}", server.local_address()))
            .force_path_style(true)
            .retry_config(RetryConfig::disabled())
            .request_checksum_calculation(RequestChecksumCalculation::WhenRequired)
            .response_checksum_validation(ResponseChecksumValidation::WhenRequired)
            .credentials_provider(Credentials::new(access_key, secret, None, None, "testing"))
            .build(),
    )
}
