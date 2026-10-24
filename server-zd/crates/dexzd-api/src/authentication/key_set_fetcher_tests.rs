// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Key set retrieval from an in-process HTTP key server, and startup checks of files and URLs.

use std::collections::BTreeMap;
use std::path::PathBuf;
use std::sync::{Arc, Mutex};
use std::time::Duration;

use serde_json::{Value, json};
use tokio::io::{AsyncReadExt, AsyncWriteExt};
use tokio::net::{TcpListener, TcpStream};
use tokio::task::JoinHandle;

use super::json_web_key_set::KeySetRefreshPolicy;
use super::json_web_token::JwsAlgorithm;
use super::token_signing_tests::{
    ManualWallClock, TEST_AUDIENCE, TEST_START_UNIX_SECONDS, TestSigningKey, key_set_document,
};
use super::{
    AuthenticationSetupError, CredentialAuthenticatorConfig, EndpointUrlError,
    JwtAuthenticatorConfig, PublicApiAuthenticator,
};

const KEY_SET_PATH: &str = "/keys";
const DISCOVERY_PATH: &str = "/.well-known/openid-configuration";
/// A self-signed P-256 certificate, valid until 2126, used only to load a CA bundle.
const TEST_CA_CERTIFICATE: &str = "-----BEGIN CERTIFICATE-----
MIIBYjCCAQigAwIBAgIUBqITLu+qfMFSJQtICNzguVxexTAwCgYIKoZIzj0EAwIw
FjEUMBIGA1UEAwwLZGV4LXRlc3QtY2EwIBcNMjYxMDI0MTc0ODI0WhgPMjEyNjA5
MzAxNzQ4MjRaMBYxFDASBgNVBAMMC2RleC10ZXN0LWNhMFkwEwYHKoZIzj0CAQYI
KoZIzj0DAQcDQgAEBQ//vu1RKUUn6TykzzafsGXnhOICFNcPmguvb9tne90/hAD8
NAczkZfLHTqP2vcDAqyUbfrr/R780rSAUvTzy6MyMDAwHQYDVR0OBBYEFBYrL72c
zIlz8pTHLpF6Nwj+Fy0tMA8GA1UdEwEB/wQFMAMBAf8wCgYIKoZIzj0EAwIDSAAw
RQIhAImFZTs9gDSJSBpVUPHssOvPKkXdWzWE8QGTOtcG7PwcAiBLhPyHGBS/N5sC
bPo0pgYBD/GvqWjT84ktCa6nsQkwcQ==
-----END CERTIFICATE-----
";

/// Refreshes fast enough for real-time tests while keeping the Go Server's structure.
const FAST_REFRESH: KeySetRefreshPolicy = KeySetRefreshPolicy {
    refresh_interval: Duration::from_millis(400),
    unknown_key_refresh_interval: Duration::from_millis(100),
    fetch_timeout: Duration::from_secs(2),
};

struct KeyServerState {
    key_set: Value,
    discovery_issuer: Option<String>,
    status: u16,
    requests: BTreeMap<String, usize>,
}

/// An HTTP/1.1 key server on loopback that serves OIDC discovery and a replaceable key set.
struct TestKeyServer {
    url: String,
    state: Arc<Mutex<KeyServerState>>,
    serving: JoinHandle<()>,
}

impl TestKeyServer {
    async fn start(key_set: Value) -> Self {
        let listener = TcpListener::bind("127.0.0.1:0")
            .await
            .expect("a loopback port is available");
        let url = format!(
            "http://{}",
            listener.local_addr().expect("the listener has an address")
        );
        let state = Arc::new(Mutex::new(KeyServerState {
            key_set,
            discovery_issuer: None,
            status: 200,
            requests: BTreeMap::new(),
        }));
        let serving = tokio::spawn(serve_key_server(listener, url.clone(), Arc::clone(&state)));
        Self {
            url,
            state,
            serving,
        }
    }

    fn update(&self, change: impl FnOnce(&mut KeyServerState)) {
        change(&mut self.state.lock().expect("the key server lock"));
    }

    fn requests(&self, path: &str) -> usize {
        self.state
            .lock()
            .expect("the key server lock")
            .requests
            .get(path)
            .copied()
            .unwrap_or(0)
    }
}

impl Drop for TestKeyServer {
    fn drop(&mut self) {
        self.serving.abort();
    }
}

async fn serve_key_server(listener: TcpListener, url: String, state: Arc<Mutex<KeyServerState>>) {
    loop {
        let Ok((connection, _peer)) = listener.accept().await else {
            return;
        };
        respond(connection, &url, &state).await;
    }
}

async fn respond(mut connection: TcpStream, url: &str, state: &Mutex<KeyServerState>) {
    let mut request = Vec::new();
    let mut buffer = [0_u8; 1024];
    while !request.windows(4).any(|window| window == b"\r\n\r\n") {
        match connection.read(&mut buffer).await {
            Ok(0) | Err(_) => return,
            Ok(read) => request.extend_from_slice(&buffer[..read]),
        }
    }
    let request = String::from_utf8_lossy(&request);
    let path = request
        .split_whitespace()
        .nth(1)
        .unwrap_or_default()
        .to_owned();
    let (status, body) = {
        let mut state = state.lock().expect("the key server lock");
        *state.requests.entry(path.clone()).or_default() += 1;
        let body = match path.as_str() {
            DISCOVERY_PATH => json!({
                "issuer": state.discovery_issuer.clone().unwrap_or_else(|| url.to_owned()),
                "jwks_uri": format!("{url}{KEY_SET_PATH}"),
            }),
            KEY_SET_PATH => state.key_set.clone(),
            _ => json!({}),
        };
        let status = if path == KEY_SET_PATH || path == DISCOVERY_PATH {
            state.status
        } else {
            404
        };
        (status, body.to_string())
    };
    let response = format!(
        "HTTP/1.1 {status} Test\r\nContent-Type: application/json\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{body}",
        body.len()
    );
    if connection.write_all(response.as_bytes()).await.is_err() {
        return;
    }
    if let Err(error) = connection.shutdown().await {
        tracing::debug!(%error, "the test key server could not close a connection");
    }
}

fn jwt_config(issuer: &str, jwks_url: Option<String>) -> CredentialAuthenticatorConfig {
    CredentialAuthenticatorConfig::Jwt(JwtAuthenticatorConfig {
        issuer: issuer.to_owned(),
        audiences: vec![TEST_AUDIENCE.to_owned()],
        jwks_url,
        allow_insecure_jwks_url: false,
        ca_file: None,
    })
}

fn authenticator(config: CredentialAuthenticatorConfig) -> PublicApiAuthenticator {
    PublicApiAuthenticator::with_key_set_refresh_policy(
        &[config],
        ManualWallClock::starting_at_test_start(),
        FAST_REFRESH,
    )
    .expect("the authenticator is built")
}

fn token(key: &TestSigningKey, key_id: &str, issuer: &str) -> String {
    key.token(
        key_id,
        &json!({
            "iss": issuer,
            "aud": [TEST_AUDIENCE],
            "exp": TEST_START_UNIX_SECONDS + 600,
        }),
    )
}

#[tokio::test]
async fn discovered_keys_rotate_through_unknown_key_and_periodic_refreshes() {
    let first = TestSigningKey::generate(JwsAlgorithm::EdDsa);
    let second = TestSigningKey::generate(JwsAlgorithm::EdDsa);
    let server = TestKeyServer::start(key_set_document(&[first.public_jwk("first")])).await;
    let issuer = server.url.clone();
    let authenticator = authenticator(jwt_config(&issuer, None));
    assert_eq!(server.requests(DISCOVERY_PATH), 0);
    assert!(
        authenticator
            .authenticate(&token(&first, "first", &issuer))
            .await
            .is_ok()
    );
    assert_eq!(server.requests(DISCOVERY_PATH), 1);
    assert_eq!(server.requests(KEY_SET_PATH), 1);

    server.update(|state| {
        state.key_set = key_set_document(&[first.public_jwk("first"), second.public_jwk("second")]);
    });
    tokio::time::sleep(FAST_REFRESH.unknown_key_refresh_interval).await;
    assert!(
        authenticator
            .authenticate(&token(&second, "second", &issuer))
            .await
            .is_ok()
    );
    assert_eq!(server.requests(KEY_SET_PATH), 2);

    server.update(|state| state.key_set = key_set_document(&[second.public_jwk("second")]));
    tokio::time::sleep(FAST_REFRESH.refresh_interval).await;
    let retired = token(&first, "first", &issuer);
    assert!(authenticator.authenticate(&retired).await.is_ok());
    tokio::time::timeout(Duration::from_secs(5), async {
        while authenticator.authenticate(&retired).await.is_ok() {
            tokio::time::sleep(Duration::from_millis(20)).await;
        }
    })
    .await
    .expect("the periodic refresh retires the removed key");
    assert!(server.requests(KEY_SET_PATH) >= 3);
    assert_eq!(
        server.requests(DISCOVERY_PATH),
        server.requests(KEY_SET_PATH)
    );
}

#[tokio::test]
async fn an_explicit_key_set_url_skips_discovery() {
    let key = TestSigningKey::generate(JwsAlgorithm::Es384);
    let server = TestKeyServer::start(key_set_document(&[key.public_jwk("explicit")])).await;
    let issuer = "https://issuer.example.com";
    let authenticator = authenticator(jwt_config(
        issuer,
        Some(format!("{}{KEY_SET_PATH}", server.url)),
    ));
    assert!(
        authenticator
            .authenticate(&token(&key, "explicit", issuer))
            .await
            .is_ok()
    );
    assert_eq!(server.requests(DISCOVERY_PATH), 0);
    assert_eq!(server.requests(KEY_SET_PATH), 1);
}

#[tokio::test]
async fn a_mismatched_discovery_issuer_or_a_failed_response_yields_no_keys() {
    let key = TestSigningKey::generate(JwsAlgorithm::Ps256);
    let server = TestKeyServer::start(key_set_document(&[key.public_jwk("key")])).await;
    let issuer = server.url.clone();
    server
        .update(|state| state.discovery_issuer = Some("https://elsewhere.example.com".to_owned()));
    let authenticator = authenticator(jwt_config(&issuer, None));
    assert!(
        authenticator
            .authenticate(&token(&key, "key", &issuer))
            .await
            .is_err()
    );
    assert_eq!(server.requests(KEY_SET_PATH), 0);

    let failing = TestKeyServer::start(key_set_document(&[key.public_jwk("key")])).await;
    failing.update(|state| state.status = 503);
    let authenticator = authenticator_with_explicit_url(&failing);
    assert!(
        authenticator
            .authenticate(&token(&key, "key", "https://issuer.example.com"))
            .await
            .is_err()
    );
    failing.update(|state| state.status = 200);
    tokio::time::sleep(FAST_REFRESH.unknown_key_refresh_interval).await;
    assert!(
        authenticator
            .authenticate(&token(&key, "key", "https://issuer.example.com"))
            .await
            .is_ok()
    );
}

fn authenticator_with_explicit_url(server: &TestKeyServer) -> PublicApiAuthenticator {
    authenticator(jwt_config(
        "https://issuer.example.com",
        Some(format!("{}{KEY_SET_PATH}", server.url)),
    ))
}

fn setup_error(config: CredentialAuthenticatorConfig) -> AuthenticationSetupError {
    match PublicApiAuthenticator::new(&[config], ManualWallClock::starting_at_test_start()) {
        Ok(_) => panic!("the authenticator must fail to build"),
        Err(error) => error,
    }
}

fn jwt_with(change: impl FnOnce(&mut JwtAuthenticatorConfig)) -> CredentialAuthenticatorConfig {
    let mut config = JwtAuthenticatorConfig {
        issuer: "https://issuer.example.com".to_owned(),
        audiences: vec![TEST_AUDIENCE.to_owned()],
        jwks_url: None,
        allow_insecure_jwks_url: false,
        ca_file: None,
    };
    change(&mut config);
    CredentialAuthenticatorConfig::Jwt(config)
}

#[tokio::test]
async fn startup_checks_token_files_ca_bundles_and_urls() {
    let directory = tempfile::TempDir::new().expect("a temporary directory");
    let missing = directory.path().join("missing.yaml");
    assert!(matches!(
        setup_error(CredentialAuthenticatorConfig::StaticTokens { file: missing }),
        AuthenticationSetupError::StaticTokenFile { .. }
    ));
    let malformed = directory.path().join("malformed.yaml");
    std::fs::write(&malformed, "tokens:\n  - sha256: short\n").expect("the file is written");
    let error = setup_error(CredentialAuthenticatorConfig::StaticTokens { file: malformed });
    assert!(error.to_string().contains("tokens[0].sha256"), "{error}");

    let empty_bundle = directory.path().join("empty.pem");
    std::fs::write(&empty_bundle, "no certificates here\n").expect("the file is written");
    for ca_file in [directory.path().join("missing.pem"), empty_bundle] {
        assert!(matches!(
            setup_error(jwt_with(|config| config.ca_file = Some(ca_file.clone()))),
            AuthenticationSetupError::CaFile { .. }
        ));
    }
    let bundle: PathBuf = directory.path().join("ca.pem");
    std::fs::write(&bundle, TEST_CA_CERTIFICATE).expect("the bundle is written");
    PublicApiAuthenticator::new(
        &[jwt_with(|config| config.ca_file = Some(bundle))],
        ManualWallClock::starting_at_test_start(),
    )
    .expect("a valid CA bundle loads");

    assert!(matches!(
        setup_error(jwt_with(
            |config| config.issuer = "http://issuer.example.com".to_owned()
        )),
        AuthenticationSetupError::EndpointUrl {
            field: "issuer",
            error: EndpointUrlError::RequiresHttps,
            ..
        }
    ));
    assert!(matches!(
        setup_error(jwt_with(|config| {
            config.jwks_url = Some("http://keys.internal/jwks".to_owned());
        })),
        AuthenticationSetupError::EndpointUrl {
            field: "jwksURL",
            error: EndpointUrlError::RequiresHttps,
            ..
        }
    ));
    PublicApiAuthenticator::new(
        &[jwt_with(|config| {
            config.jwks_url = Some("http://keys.internal/jwks".to_owned());
            config.allow_insecure_jwks_url = true;
        })],
        ManualWallClock::starting_at_test_start(),
    )
    .expect("the opt-in permits a plain HTTP key set URL");
}

#[test]
fn endpoint_urls_follow_the_go_server_rule() {
    use super::check_endpoint_url;
    for url in [
        "https://issuer.example.com",
        "https://issuer.example.com/realms/dex/",
        "http://localhost:8080",
        "http://127.0.0.1",
        "http://127.8.9.10:1",
        "http://[::1]:9000/keys",
    ] {
        assert_eq!(check_endpoint_url(url, false), Ok(()), "{url}");
    }
    for (url, error) in [
        ("", EndpointUrlError::NotAbsolute),
        ("issuer.example.com", EndpointUrlError::NotAbsolute),
        ("/keys", EndpointUrlError::NotAbsolute),
        ("http://issuer.example.com", EndpointUrlError::RequiresHttps),
        ("http://10.0.0.1/keys", EndpointUrlError::RequiresHttps),
        (
            "http://localhost.example.com",
            EndpointUrlError::RequiresHttps,
        ),
        ("ftp://issuer.example.com", EndpointUrlError::RequiresHttps),
    ] {
        assert_eq!(check_endpoint_url(url, false), Err(error), "{url}");
    }
    assert_eq!(
        check_endpoint_url("http://keys.internal/jwks", true),
        Ok(())
    );
    assert_eq!(
        check_endpoint_url("ftp://keys.internal/jwks", true),
        Err(EndpointUrlError::UnsupportedScheme)
    );
}
