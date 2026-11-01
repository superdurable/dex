// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Admin credential checks in front of AdminService on an in-process server.

use std::sync::Arc;
use std::time::Duration;

use dexzd_protocol::dex::admin_service_client::AdminServiceClient;
use dexzd_protocol::dex::{ErrorSubStatus, ServiceErrorResponse, StepDownActiveNodeRequest};
use prost::Message;
use serde_json::{Value, json};
use tokio::task::JoinHandle;
use tokio_stream::wrappers::TcpListenerStream;
use tonic::transport::Channel;
use tonic::{Code, Request, Status};

use super::json_web_key_set::{JsonWebKeySetCache, KeySetRefreshPolicy};
use super::json_web_token::{JwsAlgorithm, JwtVerifier};
use super::static_token_file::StaticTokenHashes;
use super::token_signing_tests::{
    ManualWallClock, ScriptedKeySetSource, TEST_AUDIENCE, TEST_ISSUER, TEST_START_UNIX_SECONDS,
    TestSigningKey, key_set_document,
};
use super::watched_file::WatchedFile;
use super::{
    AuthenticatedService, CredentialAuthenticator, JwtAuthenticatorConfig, PublicApiAuthenticator,
};
use crate::admin_service::dex_admin_service_tests::{
    ScriptedActiveNodeStepDown, outcome_with_takeover,
};
use crate::flow_operation_error::GoogleRpcStatus;
use crate::{ActiveNodeStepDown, DexAdminService};

const KEY_ID: &str = "admin-test-key";
const ADMIN_AUDIENCE: &str = "dex-operators";
const STATIC_TOKEN: &str = "static-token-for-admin-authentication-tests";
const MISSING_CREDENTIAL: &str = "a bearer credential is required";
const INVALID_CREDENTIAL: &str = "the credential is not valid";
const NOT_ADMIN_CREDENTIAL: &str =
    "admin RPCs require a JWT whose audience is listed in auth.adminAudiences";

struct AdminTestServer {
    channel: Channel,
    signing_key: TestSigningKey,
    key_set_source: Arc<ScriptedKeySetSource>,
    step_down: Arc<ScriptedActiveNodeStepDown>,
    _token_file: tempfile::NamedTempFile,
    serving: JoinHandle<()>,
}

impl Drop for AdminTestServer {
    fn drop(&mut self) {
        self.serving.abort();
    }
}

impl AdminTestServer {
    /// Serves AdminService behind a static token and a JWT authenticator, or none when `is_open`.
    async fn start(admin_audiences: &[&str], is_open: bool) -> Self {
        let signing_key = TestSigningKey::generate(JwsAlgorithm::EdDsa);
        let key_set_source =
            ScriptedKeySetSource::serving(key_set_document(&[signing_key.public_jwk(KEY_ID)]));
        let token_file = tempfile::NamedTempFile::new().expect("a temporary token file");
        std::fs::write(
            token_file.path(),
            format!(
                "tokens:\n  - sha256: {}\n",
                hex(&super::sha256(STATIC_TOKEN))
            ),
        )
        .expect("the token file is written");
        let authenticator = PublicApiAuthenticator {
            authenticators: vec![
                CredentialAuthenticator::StaticTokens(
                    WatchedFile::<StaticTokenHashes>::open_with_check_interval(
                        token_file.path(),
                        Duration::ZERO,
                    )
                    .expect("the token file loads"),
                ),
                CredentialAuthenticator::Jwt(public_jwt_verifier(Arc::clone(&key_set_source))),
            ],
        };
        let step_down = ScriptedActiveNodeStepDown::returning(Ok(outcome_with_takeover()));
        let admin_service = AuthenticatedService::for_admin_service(
            DexAdminService::new(Arc::clone(&step_down) as Arc<dyn ActiveNodeStepDown>)
                .into_server(),
            (!is_open).then(|| Arc::new(authenticator)),
            &admin_audiences
                .iter()
                .map(|audience| (*audience).to_owned())
                .collect::<Vec<_>>(),
        );
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0")
            .await
            .expect("a loopback port is available");
        let address = listener.local_addr().expect("the listener has an address");
        let serving = tokio::spawn(async move {
            tonic::transport::Server::builder()
                .add_service(admin_service)
                .serve_with_incoming(TcpListenerStream::new(listener))
                .await
                .expect("the test server serves");
        });
        let channel = tonic::transport::Endpoint::from_shared(format!("http://{address}"))
            .expect("the endpoint URL is valid")
            .connect()
            .await
            .expect("the test server accepts connections");
        Self {
            channel,
            signing_key,
            key_set_source,
            step_down,
            _token_file: token_file,
            serving,
        }
    }

    fn jwt(&self, audience: Value, lifetime_seconds: i64) -> String {
        self.signing_key.token(
            KEY_ID,
            &json!({
                "iss": TEST_ISSUER,
                "aud": audience,
                "exp": TEST_START_UNIX_SECONDS + lifetime_seconds,
            }),
        )
    }

    async fn step_down(&self, credential: Option<&str>) -> Result<(), Status> {
        let mut request = Request::new(StepDownActiveNodeRequest {});
        if let Some(credential) = credential {
            request.metadata_mut().insert(
                "authorization",
                format!("Bearer {credential}")
                    .parse()
                    .expect("the credential is a valid header value"),
            );
        }
        AdminServiceClient::new(self.channel.clone())
            .step_down_active_node(request)
            .await
            .map(|_| ())
    }
}

fn public_jwt_verifier(key_set_source: Arc<ScriptedKeySetSource>) -> JwtVerifier {
    JwtVerifier::new(
        JwtAuthenticatorConfig {
            issuer: TEST_ISSUER.to_owned(),
            audiences: vec![TEST_AUDIENCE.to_owned()],
            jwks_url: None,
            allow_insecure_jwks_url: false,
            ca_file: None,
        },
        JsonWebKeySetCache::new(
            TEST_ISSUER.to_owned(),
            key_set_source,
            KeySetRefreshPolicy::STANDARD,
        ),
        ManualWallClock::starting_at_test_start(),
    )
}

fn hex(bytes: &[u8]) -> String {
    bytes.iter().map(|byte| format!("{byte:02x}")).collect()
}

fn assert_refused(result: Result<(), Status>, code: Code, detail: &str) {
    let status = result.expect_err("the call must be refused");
    assert_eq!(status.code(), code, "{status:?}");
    assert_eq!(status.message(), detail);
    let details =
        GoogleRpcStatus::decode(status.details()).expect("the details are a google.rpc.Status");
    assert_eq!(details.code, code as i32);
    let response = ServiceErrorResponse::decode(details.details[0].value.as_slice())
        .expect("the detail is a ServiceErrorResponse");
    assert_eq!(response.sub_status, ErrorSubStatus::Uncategorized as i32);
    assert_eq!(response.detail, detail);
}

#[tokio::test]
async fn only_a_jwt_with_an_admin_audience_steps_down() {
    let server = AdminTestServer::start(&[ADMIN_AUDIENCE], false).await;
    let public_jwt = server.jwt(json!(TEST_AUDIENCE), 3600);
    let expired_admin_jwt = server.jwt(json!(ADMIN_AUDIENCE), -3600);
    let other_issuer_admin_jwt = server.signing_key.token(
        KEY_ID,
        &json!({
            "iss": "https://other-issuer.example.com",
            "aud": ADMIN_AUDIENCE,
            "exp": TEST_START_UNIX_SECONDS + 3600,
        }),
    );
    assert_refused(
        server.step_down(None).await,
        Code::Unauthenticated,
        MISSING_CREDENTIAL,
    );
    for credential in [
        "not-a-known-token",
        expired_admin_jwt.as_str(),
        other_issuer_admin_jwt.as_str(),
    ] {
        assert_refused(
            server.step_down(Some(credential)).await,
            Code::Unauthenticated,
            INVALID_CREDENTIAL,
        );
    }
    for credential in [STATIC_TOKEN, public_jwt.as_str()] {
        assert_refused(
            server.step_down(Some(credential)).await,
            Code::PermissionDenied,
            NOT_ADMIN_CREDENTIAL,
        );
    }
    assert_eq!(server.step_down.step_down_count(), 0);

    for credential in [
        server.jwt(json!(ADMIN_AUDIENCE), 3600),
        server.jwt(json!([TEST_AUDIENCE, ADMIN_AUDIENCE]), 3600),
    ] {
        server
            .step_down(Some(&credential))
            .await
            .expect("an admin JWT authorizes the step down");
    }
    assert_eq!(server.step_down.step_down_count(), 2);
}

#[tokio::test]
async fn empty_admin_audiences_refuse_every_credential() {
    let server = AdminTestServer::start(&[], false).await;
    assert_refused(
        server
            .step_down(Some(&server.jwt(json!(ADMIN_AUDIENCE), 3600)))
            .await,
        Code::Unauthenticated,
        INVALID_CREDENTIAL,
    );
    for credential in [
        STATIC_TOKEN.to_owned(),
        server.jwt(json!(TEST_AUDIENCE), 3600),
        server.jwt(json!([TEST_AUDIENCE, ADMIN_AUDIENCE]), 3600),
    ] {
        assert_refused(
            server.step_down(Some(&credential)).await,
            Code::PermissionDenied,
            NOT_ADMIN_CREDENTIAL,
        );
    }
    assert_eq!(server.step_down.step_down_count(), 0);
}

#[tokio::test]
async fn a_server_without_authentication_allows_admin_calls() {
    let server = AdminTestServer::start(&[], true).await;
    server
        .step_down(None)
        .await
        .expect("an unauthenticated server allows admin calls");
    assert_eq!(server.step_down.step_down_count(), 1);
}

#[tokio::test]
async fn admin_and_public_verification_share_one_key_set_cache() {
    let server = AdminTestServer::start(&[ADMIN_AUDIENCE], false).await;
    server
        .step_down(Some(&server.jwt(json!(ADMIN_AUDIENCE), 3600)))
        .await
        .expect("an admin JWT authorizes the step down");
    assert_refused(
        server
            .step_down(Some(&server.jwt(json!(TEST_AUDIENCE), 3600)))
            .await,
        Code::PermissionDenied,
        NOT_ADMIN_CREDENTIAL,
    );
    assert_eq!(server.key_set_source.fetch_count(), 1);
}
