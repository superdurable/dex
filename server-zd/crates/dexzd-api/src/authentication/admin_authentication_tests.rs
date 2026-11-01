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
use dexzd_protocol::dex::{
    DescribeActiveNodeRequest, ErrorSubStatus, ServiceErrorResponse, StepDownActiveNodeRequest,
};
use prost::Message;
use serde_json::{Value, json};
use tokio::task::JoinHandle;
use tokio_stream::wrappers::TcpListenerStream;
use tonic::transport::Channel;
use tonic::{Code, Request, Status};

use super::json_web_key_set::{JsonWebKeySetCache, KeySetRefreshPolicy};
use super::json_web_token::{JwsAlgorithm, JwtVerifier};
use super::key_set_fetcher_tests::CapturedLogs;
use super::static_token_file::StaticTokenHashes;
use super::token_signing_tests::{
    ManualWallClock, ScriptedKeySetSource, TEST_AUDIENCE, TEST_ISSUER, TEST_START_UNIX_SECONDS,
    TestSigningKey, key_set_document,
};
use super::watched_file::WatchedFile;
use super::{CredentialAuthenticator, JwtAuthenticatorConfig, PublicApiAuthenticator};
use crate::admin_service::dex_admin_service_tests::{
    ScriptedActiveNodeAdministration, outcome_with_takeover,
};
use crate::flow_operation_error::GoogleRpcStatus;
use crate::{ActiveNodeAdministration, DexAdminService};

const KEY_ID: &str = "admin-test-key";
const ADMIN_AUDIENCE: &str = "dex-operators";
/// An issuer configured without admin audiences, such as one that lets callers choose `aud`.
const PUBLIC_ONLY_ISSUER: &str = "https://public-only-issuer.example.com";
const STATIC_TOKEN: &str = "static-token-for-admin-authentication-tests";
const MISSING_CREDENTIAL: &str = "a bearer credential is required";
const INVALID_CREDENTIAL: &str = "the credential is not valid";
const NOT_ADMIN_CREDENTIAL: &str =
    "admin RPCs require a JWT whose audience is listed in its jwt authenticator's adminAudiences";

/// Which authentication the test server puts in front of AdminService.
enum TestAuthentication<'audiences> {
    /// No authenticator, as on a development server.
    Off,
    /// Static tokens, a JWT issuer with these admin audiences, and an issuer without any.
    AdminAudiences(&'audiences [&'audiences str]),
}

struct AdminTestServer {
    channel: Channel,
    admin_issuer_key: TestSigningKey,
    public_only_issuer_key: TestSigningKey,
    admin_issuer_key_set: Arc<ScriptedKeySetSource>,
    administration: Arc<ScriptedActiveNodeAdministration>,
    _token_file: tempfile::NamedTempFile,
    serving: JoinHandle<()>,
}

impl Drop for AdminTestServer {
    fn drop(&mut self) {
        self.serving.abort();
    }
}

impl AdminTestServer {
    async fn start(authentication: TestAuthentication<'_>) -> Self {
        let admin_issuer_key = TestSigningKey::generate(JwsAlgorithm::EdDsa);
        let admin_issuer_key_set =
            ScriptedKeySetSource::serving(key_set_document(&[admin_issuer_key.public_jwk(KEY_ID)]));
        let public_only_issuer_key = TestSigningKey::generate(JwsAlgorithm::EdDsa);
        let public_only_issuer_key_set = ScriptedKeySetSource::serving(key_set_document(&[
            public_only_issuer_key.public_jwk(KEY_ID),
        ]));
        let token_file = tempfile::NamedTempFile::new().expect("a temporary token file");
        std::fs::write(
            token_file.path(),
            format!(
                "tokens:\n  - sha256: {}\n",
                hex(&super::sha256(STATIC_TOKEN))
            ),
        )
        .expect("the token file is written");
        let authenticator = match authentication {
            TestAuthentication::Off => None,
            TestAuthentication::AdminAudiences(admin_audiences) => {
                Some(Arc::new(PublicApiAuthenticator {
                    authenticators: vec![
                        CredentialAuthenticator::StaticTokens(
                            WatchedFile::<StaticTokenHashes>::open_with_check_interval(
                                token_file.path(),
                                Duration::ZERO,
                            )
                            .expect("the token file loads"),
                        ),
                        CredentialAuthenticator::Jwt(jwt_verifier(
                            TEST_ISSUER,
                            admin_audiences,
                            Arc::clone(&admin_issuer_key_set),
                        )),
                        CredentialAuthenticator::Jwt(jwt_verifier(
                            PUBLIC_ONLY_ISSUER,
                            &[],
                            public_only_issuer_key_set,
                        )),
                    ],
                }))
            }
        };
        let administration =
            ScriptedActiveNodeAdministration::returning(Ok(outcome_with_takeover()));
        let admin_service = DexAdminService::authenticated_server(
            Arc::clone(&administration) as Arc<dyn ActiveNodeAdministration>,
            authenticator,
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
            admin_issuer_key,
            public_only_issuer_key,
            admin_issuer_key_set,
            administration,
            _token_file: token_file,
            serving,
        }
    }

    /// Returns a JWT of the issuer configured with admin audiences.
    fn jwt(&self, audience: Value, lifetime_seconds: i64) -> String {
        self.admin_issuer_key.token(
            KEY_ID,
            &json!({
                "iss": TEST_ISSUER,
                "sub": "operator@example.com",
                "aud": audience,
                "exp": TEST_START_UNIX_SECONDS + lifetime_seconds,
            }),
        )
    }

    /// Returns a valid JWT of the issuer configured without admin audiences.
    fn public_only_issuer_jwt(&self, audience: Value) -> String {
        self.public_only_issuer_key.token(
            KEY_ID,
            &json!({
                "iss": PUBLIC_ONLY_ISSUER,
                "aud": audience,
                "exp": TEST_START_UNIX_SECONDS + 3600,
            }),
        )
    }

    async fn step_down(&self, credential: Option<&str>) -> Result<(), Status> {
        AdminServiceClient::new(self.channel.clone())
            .step_down_active_node(with_credential(
                StepDownActiveNodeRequest {
                    expected_node_epoch: None,
                },
                credential,
            ))
            .await
            .map(|_| ())
    }

    async fn describe(&self, credential: Option<&str>) -> Result<(), Status> {
        AdminServiceClient::new(self.channel.clone())
            .describe_active_node(with_credential(DescribeActiveNodeRequest {}, credential))
            .await
            .map(|_| ())
    }
}

fn with_credential<T>(message: T, credential: Option<&str>) -> Request<T> {
    let mut request = Request::new(message);
    if let Some(credential) = credential {
        request.metadata_mut().insert(
            "authorization",
            format!("Bearer {credential}")
                .parse()
                .expect("the credential is a valid header value"),
        );
    }
    request
}

fn jwt_verifier(
    issuer: &str,
    admin_audiences: &[&str],
    key_set_source: Arc<ScriptedKeySetSource>,
) -> JwtVerifier {
    JwtVerifier::new(
        JwtAuthenticatorConfig {
            issuer: issuer.to_owned(),
            audiences: vec![TEST_AUDIENCE.to_owned()],
            admin_audiences: admin_audiences
                .iter()
                .map(|audience| (*audience).to_owned())
                .collect(),
            jwks_url: None,
            allow_insecure_jwks_url: false,
            ca_file: None,
        },
        JsonWebKeySetCache::new(
            issuer.to_owned(),
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
async fn only_a_jwt_naming_an_admin_audience_of_its_issuer_authorizes_admin_calls() {
    let server =
        AdminTestServer::start(TestAuthentication::AdminAudiences(&[ADMIN_AUDIENCE])).await;
    let public_jwt = server.jwt(json!(TEST_AUDIENCE), 3600);
    let expired_admin_jwt = server.jwt(json!(ADMIN_AUDIENCE), -3600);
    let unknown_issuer_admin_jwt = server.admin_issuer_key.token(
        KEY_ID,
        &json!({
            "iss": "https://unknown-issuer.example.com",
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
        unknown_issuer_admin_jwt.as_str(),
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
        assert_refused(
            server.describe(Some(credential)).await,
            Code::PermissionDenied,
            NOT_ADMIN_CREDENTIAL,
        );
    }
    assert_eq!(server.administration.step_down_count(), 0);
    assert_eq!(server.administration.describe_count(), 0);

    for credential in [
        server.jwt(json!(ADMIN_AUDIENCE), 3600),
        server.jwt(json!([TEST_AUDIENCE, ADMIN_AUDIENCE]), 3600),
    ] {
        server
            .describe(Some(&credential))
            .await
            .expect("an admin JWT authorizes describe");
        server
            .step_down(Some(&credential))
            .await
            .expect("an admin JWT authorizes the step down");
    }
    assert_eq!(server.administration.describe_count(), 2);
    assert_eq!(server.administration.step_down_count(), 2);
}

#[tokio::test]
async fn an_issuer_without_admin_audiences_never_authorizes_admin_calls() {
    let server =
        AdminTestServer::start(TestAuthentication::AdminAudiences(&[ADMIN_AUDIENCE])).await;
    assert_refused(
        server
            .step_down(Some(
                &server.public_only_issuer_jwt(json!([TEST_AUDIENCE, ADMIN_AUDIENCE])),
            ))
            .await,
        Code::PermissionDenied,
        NOT_ADMIN_CREDENTIAL,
    );
    assert_refused(
        server
            .step_down(Some(&server.public_only_issuer_jwt(json!(ADMIN_AUDIENCE))))
            .await,
        Code::Unauthenticated,
        INVALID_CREDENTIAL,
    );
    assert_eq!(server.administration.step_down_count(), 0);
    server
        .step_down(Some(&server.jwt(json!(ADMIN_AUDIENCE), 3600)))
        .await
        .expect("the admin issuer still authorizes the step down");
    assert_eq!(server.administration.step_down_count(), 1);
}

#[tokio::test]
async fn empty_admin_audiences_refuse_every_credential() {
    for admin_audiences in [&[][..], &[""][..]] {
        let server =
            AdminTestServer::start(TestAuthentication::AdminAudiences(admin_audiences)).await;
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
            server.jwt(json!([TEST_AUDIENCE, ADMIN_AUDIENCE, ""]), 3600),
        ] {
            assert_refused(
                server.step_down(Some(&credential)).await,
                Code::PermissionDenied,
                NOT_ADMIN_CREDENTIAL,
            );
        }
        assert_eq!(server.administration.step_down_count(), 0);
    }
}

#[tokio::test]
async fn a_server_without_authentication_allows_admin_calls() {
    let server = AdminTestServer::start(TestAuthentication::Off).await;
    server
        .describe(None)
        .await
        .expect("an unauthenticated server allows describe");
    server
        .step_down(None)
        .await
        .expect("an unauthenticated server allows the step down");
    assert_eq!(server.administration.describe_count(), 1);
    assert_eq!(server.administration.step_down_count(), 1);
}

#[tokio::test]
async fn admin_and_public_verification_share_one_key_set_cache() {
    let server =
        AdminTestServer::start(TestAuthentication::AdminAudiences(&[ADMIN_AUDIENCE])).await;
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
    assert_eq!(server.admin_issuer_key_set.fetch_count(), 1);
}

#[tokio::test]
async fn admin_calls_log_the_verified_caller_and_denials_without_the_credential() {
    let logs = CapturedLogs::installed_globally();
    let server =
        AdminTestServer::start(TestAuthentication::AdminAudiences(&[ADMIN_AUDIENCE])).await;
    let admin_jwt = server.admin_issuer_key.token(
        KEY_ID,
        &json!({
            "iss": TEST_ISSUER,
            "sub": "audited-operator",
            "aud": ADMIN_AUDIENCE,
            "exp": TEST_START_UNIX_SECONDS + 3600,
        }),
    );
    server
        .step_down(Some(&admin_jwt))
        .await
        .expect("an admin JWT authorizes the step down");
    let public_jwt = server.admin_issuer_key.token(
        KEY_ID,
        &json!({
            "iss": TEST_ISSUER,
            "sub": "denied-application",
            "aud": TEST_AUDIENCE,
            "exp": TEST_START_UNIX_SECONDS + 3600,
        }),
    );
    assert_refused(
        server.step_down(Some(&public_jwt)).await,
        Code::PermissionDenied,
        NOT_ADMIN_CREDENTIAL,
    );
    let contents = logs.contents();
    let stepped_down = contents
        .lines()
        .find(|line| {
            line.contains("the active server process stepped down")
                && line.contains("audited-operator")
        })
        .unwrap_or_else(|| panic!("the step down is logged with its caller: {contents}"));
    assert!(stepped_down.contains("INFO"), "{stepped_down}");
    assert!(
        stepped_down.contains(&format!("caller_issuer=\"{TEST_ISSUER}\"")),
        "{stepped_down}"
    );
    let denied = contents
        .lines()
        .find(|line| line.contains("denied an admin call") && line.contains("denied-application"))
        .unwrap_or_else(|| panic!("the denial is logged with its caller: {contents}"));
    assert!(denied.contains("WARN"), "{denied}");
    assert!(
        denied.contains("/dex.AdminService/StepDownActiveNode"),
        "{denied}"
    );
    assert!(
        !contents.contains(&admin_jwt),
        "a credential reached the log"
    );
    assert!(
        !contents.contains(&public_jwt),
        "a credential reached the log"
    );
}
