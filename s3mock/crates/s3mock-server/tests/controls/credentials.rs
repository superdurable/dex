// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Credential persistence, root-only management, and S3 policy enforcement over HTTP.

use aws_sdk_s3::error::ProvideErrorMetadata;
use aws_sdk_s3::presigning::{PresignedRequest, PresigningConfig};
use s3mock::{Durability, Store, StoreConfig};
use serde_json::{Value, json};
use std::time::Duration;

use super::support::{RunningServer, client};

#[tokio::test]
async fn credentials_are_root_managed_durable_and_never_return_secrets() {
    let server = RunningServer::start(&["--signature-verification", "strict"]).await;
    let denied = server
        .raw("GET", "/_s3mock/credentials?parent=owner", "", b"")
        .await;
    assert_eq!(status(&denied), 403);
    assert!(String::from_utf8_lossy(&denied).contains("AccessDenied"));
    let policy = allow(&["s3:GetObject"], &["arn:aws:s3:::testing/p/*"]);
    let credential = json!({
        "access_key_id": "owned-key",
        "secret": "private-signing-secret",
        "parent": "owner",
        "description": "reader",
        "policy": policy,
        "status": "enabled"
    });
    let (created_status, created) = server
        .admin("POST", "credentials", credential.clone())
        .await;
    assert_eq!(created_status, 200);
    assert_eq!(created["access_key_id"], "owned-key");
    assert_no_secret(&created);
    let mut duplicate = credential;
    duplicate["secret"] = json!("replacement-secret");
    duplicate["description"] = json!("replacement");
    assert_eq!(server.admin("POST", "credentials", duplicate).await.0, 409);
    let malformed = server
        .admin(
            "POST",
            "credentials",
            json!({
                "access_key_id": "malformed",
                "secret": {"private": "DO_NOT_ECHO_SECRET"},
                "parent": "owner",
                "description": "invalid",
                "policy": allow(&["s3:GetObject"], &["arn:aws:s3:::testing/*"]),
                "status": "enabled"
            }),
        )
        .await;
    assert_eq!(malformed.0, 400);
    assert_eq!(malformed.1, json!({"error":"invalid secret"}));
    let (read_status, read) = server
        .admin("GET", "credentials/owned-key", Value::Null)
        .await;
    assert_eq!(read_status, 200);
    assert_eq!(read["description"], "reader");
    assert_no_secret(&read);
    let (list_status, list) = server
        .admin("GET", "credentials?parent=owner", Value::Null)
        .await;
    assert_eq!(list_status, 200);
    assert_eq!(
        list["credentials"]
            .as_array()
            .expect("credential list")
            .len(),
        1
    );
    assert_no_secret(&list);
    assert_eq!(
        server
            .admin("GET", "credentials/missing", Value::Null)
            .await,
        (404, json!({"error":"NoSuchCredential"}))
    );
    create(
        &server,
        "deletable",
        allow(&["s3:GetObject"], &["arn:aws:s3:::testing/*"]),
    )
    .await;
    assert_eq!(
        server
            .admin("DELETE", "credentials/deletable", Value::Null)
            .await
            .0,
        200
    );
    assert_eq!(
        server
            .admin("DELETE", "credentials/deletable", Value::Null)
            .await,
        (404, json!({"error":"NoSuchCredential"}))
    );
    let scoped_admin = server
        .raw_signed(
            "GET",
            "/_s3mock/counters",
            "owned-key",
            "private-signing-secret",
            b"",
        )
        .await;
    assert_eq!(status(&scoped_admin), 200);
    assert_eq!(
        status(&server.raw("GET", "/_s3mock/counters", "", b"").await),
        200
    );
    assert_eq!(
        status(
            &server
                .raw_signed(
                    "GET",
                    "/_s3mock/credentials/owned-key",
                    "owned-key",
                    "private-signing-secret",
                    b"",
                )
                .await
        ),
        403
    );
    let scoped = client(&server.server, "owned-key", "private-signing-secret");
    let missing = scoped
        .get_object()
        .bucket("testing")
        .key("p/missing")
        .send()
        .await
        .expect_err("allowed read reaches storage");
    assert_eq!(
        missing.as_service_error().expect("S3 error").code(),
        Some("AccessDenied")
    );
    let denied = scoped
        .get_object()
        .bucket("testing")
        .key("other")
        .send()
        .await
        .expect_err("resource outside policy is denied");
    assert_eq!(
        denied.as_service_error().expect("S3 error").code(),
        Some("AccessDenied")
    );
    let request_log = server.admin("GET", "requests", Value::Null).await.1;
    assert_no_secret(&request_log);
    let counters = server.admin("GET", "counters", Value::Null).await.1;
    assert_no_secret(&counters);
    let (updated_status, updated) = server
        .admin(
            "PATCH",
            "credentials/owned-key",
            json!({"status":"disabled"}),
        )
        .await;
    assert_eq!(updated_status, 200);
    assert_eq!(updated["status"], "disabled");
    assert_no_secret(&updated);
    let disabled = scoped
        .get_object()
        .bucket("testing")
        .key("p/missing")
        .send()
        .await
        .expect_err("disabled access key is rejected");
    assert_eq!(
        disabled.as_service_error().expect("S3 error").code(),
        Some("InvalidAccessKeyId")
    );
    let directory = server.directory.path().to_path_buf();
    server.server.shutdown().await;
    let store = Store::open(&StoreConfig {
        root_directory: directory,
        durability: Durability::Strict,
        etag_mode: s3mock::EtagMode::Md5,
    })
    .expect("store reopens");
    assert_eq!(
        store
            .credential("owned-key")
            .expect("credential survived")
            .secret_access_key,
        "private-signing-secret"
    );
    store
        .delete_credential("owned-key")
        .expect("delete persisted credential");
    assert!(matches!(
        store.credential("owned-key"),
        Err(s3mock::StoreError::NoSuchCredential)
    ));
}

#[tokio::test]
async fn test_controls_remain_unsigned_unless_root_authentication_is_enabled() {
    let default_controls = RunningServer::start(&["--signature-verification", "strict"]).await;
    assert_eq!(
        status(
            &default_controls
                .raw("GET", "/_s3mock/counters", "", b"")
                .await
        ),
        200
    );
    default_controls.server.shutdown().await;

    let protected = RunningServer::start(&[
        "--signature-verification",
        "strict",
        "--control-authentication",
        "root",
    ])
    .await;
    assert_eq!(
        status(&protected.raw("GET", "/_s3mock/counters", "", b"").await),
        403
    );
    assert_eq!(protected.admin("GET", "counters", Value::Null).await.0, 200);
    create(
        &protected,
        "scoped",
        allow(&["s3:GetObject"], &["arn:aws:s3:::testing/*"]),
    )
    .await;
    assert_eq!(
        status(
            &protected
                .raw_signed("GET", "/_s3mock/counters", "scoped", "secret", b"")
                .await
        ),
        403
    );
    protected.server.shutdown().await;

    let off = RunningServer::start(&[]).await;
    assert_eq!(
        status(&off.raw("GET", "/_s3mock/counters", "", b"").await),
        200
    );
    assert_eq!(off.admin("POST", "credentials", json!({})).await.0, 403);
    off.server.shutdown().await;
}

#[tokio::test]
async fn policy_examples_and_rejection_rules_match_the_contract() {
    let server = RunningServer::start(&["--signature-verification", "strict"]).await;
    server
        .client
        .create_bucket()
        .bucket("bucket-example")
        .send()
        .await
        .expect("example bucket");
    server
        .client
        .put_bucket_versioning()
        .bucket("bucket-example")
        .versioning_configuration(
            aws_sdk_s3::types::VersioningConfiguration::builder()
                .status(aws_sdk_s3::types::BucketVersioningStatus::Enabled)
                .build(),
        )
        .send()
        .await
        .expect("versioning enabled");
    for key in ["p/config.json", "p/secret-1", "p/inbox/a"] {
        server
            .client
            .put_object()
            .bucket("bucket-example")
            .key(key)
            .body(b"body".to_vec().into())
            .send()
            .await
            .expect("fixture object");
    }
    let policy = json!({"Version":"2012-10-17","Statement":[
        {"Effect":"Allow","Action":["s3:GetBucketVersioning"],"Resource":["arn:aws:s3:::bucket-example"]},
        {"Effect":"Allow","Action":["s3:GetObjectVersion"],"Resource":["arn:aws:s3:::bucket-example/p/config.json","arn:aws:s3:::bucket-example/p/secret-1"]},
        {"Effect":"Allow","Action":["s3:GetObject"],"Resource":["arn:aws:s3:::bucket-example/p/config.json"]},
        {"Effect":"Allow","Action":["s3:GetObject","s3:GetObjectVersion","s3:PutObject"],"Resource":["arn:aws:s3:::bucket-example/p/inbox/*"]}
    ]});
    create(&server, "example", policy).await;
    let scoped = client(&server.server, "example", "secret");
    scoped
        .get_bucket_versioning()
        .bucket("bucket-example")
        .send()
        .await
        .expect("bucket action");
    scoped
        .get_object()
        .bucket("bucket-example")
        .key("p/config.json")
        .send()
        .await
        .expect("current object");
    let version_id = server
        .client
        .get_object()
        .bucket("bucket-example")
        .key("p/secret-1")
        .send()
        .await
        .expect("versioned object")
        .version_id()
        .expect("version id")
        .to_owned();
    scoped
        .get_object()
        .bucket("bucket-example")
        .key("p/secret-1")
        .version_id(version_id)
        .send()
        .await
        .expect("version action");
    let denied = scoped
        .get_object()
        .bucket("bucket-example")
        .key("p/secret-1")
        .send()
        .await
        .expect_err("current object action is absent");
    assert_eq!(
        denied.as_service_error().expect("S3 error").code(),
        Some("AccessDenied")
    );
    scoped
        .put_object()
        .bucket("bucket-example")
        .key("p/inbox/new")
        .body(b"new".to_vec().into())
        .send()
        .await
        .expect("inbox write");
    let denied = scoped
        .put_object()
        .bucket("bucket-example")
        .key("p/config.json")
        .body(b"bad".to_vec().into())
        .send()
        .await
        .expect_err("config write is absent");
    assert_eq!(
        denied.as_service_error().expect("S3 error").code(),
        Some("AccessDenied")
    );
    create(
        &server,
        "revoked",
        json!({"Version":"2012-10-17","Statement":[
            {"Effect":"Deny","Action":["s3:*"],"Resource":["arn:aws:s3:::*"]}
        ]}),
    )
    .await;
    let revoked = client(&server.server, "revoked", "secret");
    let denied = revoked
        .get_object()
        .bucket("bucket-example")
        .key("p/config.json")
        .send()
        .await
        .expect_err("deny-only credential is revoked");
    assert_eq!(
        denied.as_service_error().expect("S3 error").code(),
        Some("AccessDenied")
    );
    create(
        &server,
        "explicit-deny",
        json!({"Version":"2012-10-17","Statement":[
            {"Effect":"Allow","Action":"s3:GetObject","Resource":"arn:aws:s3:::bucket-example/p/*"},
            {"Effect":"Deny","Action":"s3:GetObject","Resource":"arn:aws:s3:::bucket-example/p/secret-?"}
        ]}),
    )
    .await;
    let denied_key = client(&server.server, "explicit-deny", "secret")
        .get_object()
        .bucket("bucket-example")
        .key("p/secret-1")
        .send()
        .await
        .expect_err("explicit deny wins");
    assert_eq!(
        denied_key.as_service_error().expect("S3 error").code(),
        Some("AccessDenied")
    );
    create(
        &server,
        "bucket-creator",
        allow(&["s3:CreateBucket"], &["arn:aws:s3:::locked-bucket"]),
    )
    .await;
    let denied = client(&server.server, "bucket-creator", "secret")
        .create_bucket()
        .bucket("locked-bucket")
        .object_lock_enabled_for_bucket(true)
        .send()
        .await
        .expect_err("Object Lock needs two additional bucket actions");
    assert_eq!(
        denied.as_service_error().expect("S3 error").code(),
        Some("AccessDenied")
    );
    create(
        &server,
        "locked-creator",
        allow(
            &[
                "s3:CreateBucket",
                "s3:PutBucketObjectLockConfiguration",
                "s3:PutBucketVersioning",
            ],
            &["arn:aws:s3:::locked-bucket"],
        ),
    )
    .await;
    client(&server.server, "locked-creator", "secret")
        .create_bucket()
        .bucket("locked-bucket")
        .object_lock_enabled_for_bucket(true)
        .send()
        .await
        .expect("all required Object Lock permissions");
    for invalid in [
        json!({"Version":"2012-10-17","Statement":{"Effect":"Allow","Action":"s3:GetObject","Resource":"arn:aws:s3:::bucket-example/*","Condition":{}}}),
        json!({"Version":"2012-10-17","Statement":{"Effect":"Allow","NotAction":"s3:GetObject","Resource":"arn:aws:s3:::bucket-example/*"}}),
        json!({"Version":"2012-10-17","Statement":{"Effect":"Allow","Action":"s3:GetObject","NotResource":"arn:aws:s3:::bucket-example/*"}}),
        json!({"Version":"2012-10-17","Statement":{"Effect":"Allow","Action":"s3:GetObject","Resource":"arn:aws:s3:::bucket-example/*","Principal":"*"}}),
        json!({"Version":"wrong","Statement":{"Effect":"Allow","Action":"s3:GetObject","Resource":"arn:aws:s3:::bucket-example/*"}}),
        json!({"Version":"2012-10-17","Statement":{"Effect":"Allow","Action":"s3:ListBucket","Resource":"arn:aws:s3:::bucket-example","Condition":{"NumericEquals":{"s3:prefix":"p/"}}}}),
        json!({"Version":"2012-10-17","Statement":{"Effect":"Allow","Action":"s3:ListBucket","Resource":"arn:aws:s3:::bucket-example","Condition":{"StringLike":{"s3:other":"p/*"}}}}),
        json!({"Version":"2012-10-17","Statement":{"Effect":"Allow","Action":"s3:GetObject","Resource":"arn:aws:s3:::bucket-example/*","Condition":{"StringLike":{"s3:prefix":"p/*"}}}}),
        json!({"Version":"2012-10-17","Statement":{"Effect":"Allow","Action":"s3:GetObjekt","Resource":"arn:aws:s3:::bucket-example/*"}}),
    ] {
        assert_eq!(
            server
                .admin(
                    "POST",
                    "credentials",
                    json!({
                        "access_key_id":"invalid", "secret":"secret", "parent":"owner",
                        "description":"invalid", "policy":invalid, "status":"enabled"
                    })
                )
                .await
                .0,
            400
        );
    }
    create(
        &server,
        "policy-with-sid",
        json!({"Version":"2012-10-17","Statement":{
            "Sid":"read-config","Effect":"Allow","Action":"s3:GetObject",
            "Resource":"arn:aws:s3:::bucket-example/p/config.json"
        }}),
    )
    .await;
    assert_eq!(
        server.admin("POST", "credentials", json!({
            "access_key_id":"bad,key", "secret":"secret", "parent":"owner",
            "description":"invalid", "policy":allow(&["s3:GetObject"], &["arn:aws:s3:::bucket-example/*"]),
            "status":"enabled"
        })).await.0,
        400
    );
    server.server.shutdown().await;
}

#[tokio::test]
async fn prefix_conditions_cover_listing_versions_and_empty_prefix() {
    let server = RunningServer::start(&["--signature-verification", "strict"]).await;
    server.put("p/existing", "stored").await;
    create(
        &server,
        "unconditioned-list",
        json!({
            "Version":"2012-10-17","Statement":[
                {"Effect":"Allow","Action":"s3:GetObject","Resource":"arn:aws:s3:::testing/p/*"},
                {"Effect":"Allow","Action":"s3:ListBucket","Resource":"arn:aws:s3:::testing"}
            ]
        }),
    )
    .await;
    let unconditioned = client(&server.server, "unconditioned-list", "secret");
    let missing = unconditioned
        .get_object()
        .bucket("testing")
        .key("p/missing")
        .send()
        .await
        .expect_err("missing key is disclosed with ListBucket");
    assert_eq!(
        missing.as_service_error().expect("S3 error").code(),
        Some("NoSuchKey")
    );
    let missing_head = unconditioned
        .head_object()
        .bucket("testing")
        .key("p/missing")
        .send()
        .await
        .expect_err("missing HEAD is disclosed with ListBucket");
    assert_eq!(
        missing_head
            .raw_response()
            .expect("HTTP response")
            .status()
            .as_u16(),
        404
    );
    create(
        &server,
        "prefix-list",
        json!({
            "Version":"2012-10-17","Statement":[
                {"Effect":"Allow","Action":"s3:GetObject","Resource":"arn:aws:s3:::testing/p/*"},
                {"Effect":"Allow","Action":"s3:ListBucket","Resource":"arn:aws:s3:::testing",
                    "Condition":{"StringLike":{"s3:prefix":"p/*"}}}
            ]
        }),
    )
    .await;
    let confined = client(&server.server, "prefix-list", "secret");
    confined
        .list_objects_v2()
        .bucket("testing")
        .prefix("p/")
        .send()
        .await
        .expect("matching prefix is listable");
    let denied = confined
        .list_objects_v2()
        .bucket("testing")
        .prefix("q/")
        .send()
        .await
        .expect_err("nonmatching prefix is denied");
    assert_eq!(
        denied.as_service_error().expect("S3 error").code(),
        Some("AccessDenied")
    );
    let absent = confined
        .list_objects_v2()
        .bucket("testing")
        .send()
        .await
        .expect_err("an absent prefix does not satisfy the condition");
    assert_eq!(
        absent.as_service_error().expect("S3 error").code(),
        Some("AccessDenied")
    );
    let missing = confined
        .get_object()
        .bucket("testing")
        .key("p/missing")
        .send()
        .await
        .expect_err("matching prefix discloses a missing key");
    assert_eq!(
        missing.as_service_error().expect("S3 error").code(),
        Some("NoSuchKey")
    );
    let missing_head = confined
        .head_object()
        .bucket("testing")
        .key("p/missing")
        .send()
        .await
        .expect_err("matching prefix discloses missing HEAD");
    assert_eq!(
        missing_head
            .raw_response()
            .expect("HTTP response")
            .status()
            .as_u16(),
        404
    );
    create(
        &server,
        "exact-prefix",
        json!({
            "Version":"2012-10-17","Statement":{
                "Effect":"Allow","Action":"s3:ListBucketVersions","Resource":"arn:aws:s3:::testing",
                "Condition":{"StringEquals":{"s3:prefix":"p/"}}
            }
        }),
    )
    .await;
    let exact = client(&server.server, "exact-prefix", "secret");
    exact
        .list_object_versions()
        .bucket("testing")
        .prefix("p/")
        .send()
        .await
        .expect("StringEquals accepts the exact version prefix");
    let denied = exact
        .list_object_versions()
        .bucket("testing")
        .prefix("p/x")
        .send()
        .await
        .expect_err("StringEquals rejects another prefix");
    assert_eq!(
        denied.as_service_error().expect("S3 error").code(),
        Some("AccessDenied")
    );
    create(
        &server,
        "empty-prefix",
        json!({
            "Version":"2012-10-17","Statement":{
                "Effect":"Allow","Action":"s3:ListBucket","Resource":"arn:aws:s3:::testing",
                "Condition":{"StringEquals":{"s3:prefix":""}}
            }
        }),
    )
    .await;
    let empty_prefix = client(&server.server, "empty-prefix", "secret");
    empty_prefix
        .list_objects_v2()
        .bucket("testing")
        .prefix("")
        .send()
        .await
        .expect("an explicitly empty prefix satisfies the condition");
    let absent = empty_prefix
        .list_objects_v2()
        .bucket("testing")
        .send()
        .await
        .expect_err("an absent prefix differs from an empty prefix");
    assert_eq!(
        absent.as_service_error().expect("S3 error").code(),
        Some("AccessDenied")
    );
    let head = empty_prefix
        .head_bucket()
        .bucket("testing")
        .send()
        .await
        .expect_err("HeadBucket has no prefix for a conditioned grant");
    assert_eq!(
        head.raw_response()
            .expect("HTTP response")
            .status()
            .as_u16(),
        403
    );
    create(
        &server,
        "lowercase-action",
        allow(&["s3:getobject"], &["arn:aws:s3:::testing/p/existing"]),
    )
    .await;
    let lowercase = client(&server.server, "lowercase-action", "secret")
        .get_object()
        .bucket("testing")
        .key("p/existing")
        .send()
        .await
        .expect("IAM action names are case-insensitive");
    assert_eq!(
        lowercase
            .body
            .collect()
            .await
            .expect("object body")
            .into_bytes()
            .as_ref(),
        b"stored"
    );
    server.server.shutdown().await;
}

#[tokio::test]
async fn missing_key_disclosure_matches_list_permission_for_the_key_prefix() {
    struct Case {
        access_key_id: &'static str,
        key: &'static str,
        condition: Option<Value>,
        is_list_allowed: bool,
        is_missing_disclosed: bool,
    }
    let cases = [
        Case {
            access_key_id: "like-prefix",
            key: "p/missing",
            condition: Some(json!({"StringLike":{"s3:prefix":"p/*"}})),
            is_list_allowed: true,
            is_missing_disclosed: true,
        },
        Case {
            access_key_id: "equals-prefix",
            key: "p/missing",
            condition: Some(json!({"StringEquals":{"s3:prefix":"p/"}})),
            is_list_allowed: false,
            is_missing_disclosed: false,
        },
        Case {
            access_key_id: "without-list",
            key: "p/missing",
            condition: None,
            is_list_allowed: false,
            is_missing_disclosed: false,
        },
        Case {
            access_key_id: "outside-prefix",
            key: "q/missing",
            condition: Some(json!({"StringLike":{"s3:prefix":"p/*"}})),
            is_list_allowed: false,
            is_missing_disclosed: false,
        },
    ];
    let server = RunningServer::start(&["--signature-verification", "strict"]).await;
    for case in cases {
        let mut statements = vec![json!({
            "Effect":"Allow","Action":"s3:GetObject","Resource":"arn:aws:s3:::testing/p/*"
        })];
        if let Some(condition) = case.condition {
            statements.push(json!({
                "Effect":"Allow","Action":"s3:ListBucket","Resource":"arn:aws:s3:::testing",
                "Condition":condition
            }));
        }
        create(
            &server,
            case.access_key_id,
            json!({"Version":"2012-10-17","Statement":statements}),
        )
        .await;
        let scoped = client(&server.server, case.access_key_id, "secret");
        let get_error = scoped
            .get_object()
            .bucket("testing")
            .key(case.key)
            .send()
            .await
            .expect_err("the key is absent or outside the grant");
        assert_eq!(
            get_error.as_service_error().expect("S3 error").code(),
            Some(if case.is_missing_disclosed {
                "NoSuchKey"
            } else {
                "AccessDenied"
            }),
            "{} GET",
            case.access_key_id
        );
        let head_error = scoped
            .head_object()
            .bucket("testing")
            .key(case.key)
            .send()
            .await
            .expect_err("the key is absent or outside the grant");
        assert_eq!(
            head_error
                .raw_response()
                .expect("HTTP response")
                .status()
                .as_u16(),
            if case.is_missing_disclosed { 404 } else { 403 },
            "{} HEAD",
            case.access_key_id
        );
        let listing = scoped
            .list_objects_v2()
            .bucket("testing")
            .prefix(case.key)
            .send()
            .await;
        assert_eq!(
            listing.is_ok(),
            case.is_list_allowed,
            "{} LIST",
            case.access_key_id
        );
        let head_bucket = scoped
            .head_bucket()
            .bucket("testing")
            .send()
            .await
            .expect_err("a conditioned ListBucket grant cannot HeadBucket");
        assert_eq!(
            head_bucket
                .raw_response()
                .expect("HTTP response")
                .status()
                .as_u16(),
            403
        );
        let uploads = scoped
            .list_multipart_uploads()
            .bucket("testing")
            .send()
            .await
            .expect_err("ListMultipartUploads needs its own action");
        assert_eq!(
            uploads.as_service_error().expect("S3 error").code(),
            Some("AccessDenied")
        );
    }
    server.server.shutdown().await;
}

#[tokio::test]
async fn presigned_requests_obey_scoped_policies_and_disabled_status() {
    let server = RunningServer::start(&["--signature-verification", "strict"]).await;
    server.put("readable", "stored").await;
    server.put("private", "hidden").await;
    create(
        &server,
        "presigned-scoped",
        allow(
            &["s3:GetObject", "s3:PutObject"],
            &["arn:aws:s3:::testing/readable"],
        ),
    )
    .await;
    let scoped = client(&server.server, "presigned-scoped", "secret");
    let valid = scoped
        .get_object()
        .bucket("testing")
        .key("readable")
        .presigned(PresigningConfig::expires_in(Duration::from_secs(300)).expect("valid expiry"))
        .await
        .expect("GET is presigned");
    let response = send_local_presigned(&server, &valid, b"").await;
    assert_eq!(status(&response), 200);
    let denied = scoped
        .get_object()
        .bucket("testing")
        .key("private")
        .presigned(PresigningConfig::expires_in(Duration::from_secs(300)).expect("valid expiry"))
        .await
        .expect("GET is presigned");
    let response = send_local_presigned(&server, &denied, b"").await;
    assert_eq!(status(&response), 403);
    assert!(String::from_utf8_lossy(&response).contains("AccessDenied"));
    let put = scoped
        .put_object()
        .bucket("testing")
        .key("readable")
        .presigned(PresigningConfig::expires_in(Duration::from_secs(300)).expect("valid expiry"))
        .await
        .expect("PUT is presigned");
    let response = send_local_presigned(&server, &put, b"replaced").await;
    assert_eq!(status(&response), 200);
    assert_eq!(server.get("readable").await, b"replaced");
    assert_eq!(
        server
            .admin(
                "PATCH",
                "credentials/presigned-scoped",
                json!({"status":"disabled"})
            )
            .await
            .0,
        200
    );
    let response = send_local_presigned(&server, &valid, b"").await;
    assert_eq!(status(&response), 403);
    assert!(String::from_utf8_lossy(&response).contains("InvalidAccessKeyId"));
    server.server.shutdown().await;
}

#[tokio::test]
async fn every_supported_s3_operation_uses_its_documented_action() {
    let server = RunningServer::start(&["--signature-verification", "strict"]).await;
    server.put("fixture", "keep bucket nonempty").await;
    let cases: &[(&str, &str, &[u8], &str, &str)] = &[
        ("GET", "/", b"", "s3:ListAllMyBuckets", "arn:aws:s3:::*"),
        (
            "PUT",
            "/newbucket",
            b"",
            "s3:CreateBucket",
            "arn:aws:s3:::newbucket",
        ),
        (
            "HEAD",
            "/testing",
            b"",
            "s3:ListBucket",
            "arn:aws:s3:::testing",
        ),
        (
            "DELETE",
            "/testing",
            b"",
            "s3:DeleteBucket",
            "arn:aws:s3:::testing",
        ),
        (
            "GET",
            "/testing?location",
            b"",
            "s3:GetBucketLocation",
            "arn:aws:s3:::testing",
        ),
        (
            "GET",
            "/testing?versioning",
            b"",
            "s3:GetBucketVersioning",
            "arn:aws:s3:::testing",
        ),
        (
            "PUT",
            "/testing?versioning",
            b"bad",
            "s3:PutBucketVersioning",
            "arn:aws:s3:::testing",
        ),
        (
            "GET",
            "/testing?lifecycle",
            b"",
            "s3:GetLifecycleConfiguration",
            "arn:aws:s3:::testing",
        ),
        (
            "PUT",
            "/testing?lifecycle",
            b"bad",
            "s3:PutLifecycleConfiguration",
            "arn:aws:s3:::testing",
        ),
        (
            "DELETE",
            "/testing?lifecycle",
            b"",
            "s3:PutLifecycleConfiguration",
            "arn:aws:s3:::testing",
        ),
        (
            "GET",
            "/testing?object-lock",
            b"",
            "s3:GetBucketObjectLockConfiguration",
            "arn:aws:s3:::testing",
        ),
        (
            "PUT",
            "/testing?object-lock",
            b"bad",
            "s3:PutBucketObjectLockConfiguration",
            "arn:aws:s3:::testing",
        ),
        (
            "GET",
            "/testing?uploads",
            b"",
            "s3:ListBucketMultipartUploads",
            "arn:aws:s3:::testing",
        ),
        (
            "GET",
            "/testing?list-type=2",
            b"",
            "s3:ListBucket",
            "arn:aws:s3:::testing",
        ),
        (
            "GET",
            "/testing?versions",
            b"",
            "s3:ListBucketVersions",
            "arn:aws:s3:::testing",
        ),
        (
            "PUT",
            "/testing/key",
            b"new",
            "s3:PutObject",
            "arn:aws:s3:::testing/key",
        ),
        (
            "GET",
            "/testing/key",
            b"",
            "s3:GetObject",
            "arn:aws:s3:::testing/key",
        ),
        (
            "HEAD",
            "/testing/key",
            b"",
            "s3:GetObject",
            "arn:aws:s3:::testing/key",
        ),
        (
            "GET",
            "/testing/key?versionId=bad",
            b"",
            "s3:GetObjectVersion",
            "arn:aws:s3:::testing/key",
        ),
        (
            "HEAD",
            "/testing/key?versionId=bad",
            b"",
            "s3:GetObjectVersion",
            "arn:aws:s3:::testing/key",
        ),
        (
            "DELETE",
            "/testing/key",
            b"",
            "s3:DeleteObject",
            "arn:aws:s3:::testing/key",
        ),
        (
            "DELETE",
            "/testing/key?versionId=bad",
            b"",
            "s3:DeleteObjectVersion",
            "arn:aws:s3:::testing/key",
        ),
        (
            "POST",
            "/testing/key?uploads",
            b"",
            "s3:PutObject",
            "arn:aws:s3:::testing/key",
        ),
        (
            "PUT",
            "/testing/key?uploadId=missing&partNumber=1",
            b"a",
            "s3:PutObject",
            "arn:aws:s3:::testing/key",
        ),
        (
            "POST",
            "/testing/key?uploadId=missing",
            b"bad",
            "s3:PutObject",
            "arn:aws:s3:::testing/key",
        ),
        (
            "DELETE",
            "/testing/key?uploadId=missing",
            b"",
            "s3:AbortMultipartUpload",
            "arn:aws:s3:::testing/key",
        ),
        (
            "GET",
            "/testing/key?uploadId=missing",
            b"",
            "s3:ListMultipartUploadParts",
            "arn:aws:s3:::testing/key",
        ),
    ];
    for (index, (method, path, body, action, resource)) in cases.iter().enumerate() {
        let access_key_id = format!("action-{index}");
        create(&server, &access_key_id, allow(&[*action], &[*resource])).await;
        let response = server
            .raw_signed(method, path, &access_key_id, "secret", body)
            .await;
        assert!(
            !String::from_utf8_lossy(&response).contains("<Code>AccessDenied</Code>"),
            "{method} {path} should require {action}: {}",
            String::from_utf8_lossy(&response)
        );
        assert!(
            !String::from_utf8_lossy(&response).contains("<Code>SignatureDoesNotMatch</Code>"),
            "signature for {method} {path}"
        );
    }
    let batch_policy = allow(&["s3:DeleteObject"], &["arn:aws:s3:::testing/allowed"]);
    create(&server, "batch", batch_policy).await;
    let batch = client(&server.server, "batch", "secret")
        .delete_objects()
        .bucket("testing")
        .delete(
            aws_sdk_s3::types::Delete::builder()
                .objects(
                    aws_sdk_s3::types::ObjectIdentifier::builder()
                        .key("allowed")
                        .build()
                        .expect("key"),
                )
                .objects(
                    aws_sdk_s3::types::ObjectIdentifier::builder()
                        .key("denied")
                        .build()
                        .expect("key"),
                )
                .build()
                .expect("delete request"),
        )
        .send()
        .await
        .expect("per-key batch result");
    assert_eq!(batch.deleted().len(), 1);
    assert_eq!(batch.errors().len(), 1);
    assert_eq!(batch.errors()[0].code(), Some("AccessDenied"));
    server.server.shutdown().await;
}

async fn create(server: &RunningServer, access_key_id: &str, policy: Value) {
    let (status, body) = server
        .admin(
            "POST",
            "credentials",
            json!({
                "access_key_id":access_key_id, "secret":"secret", "parent":"owner",
                "description":"test", "policy":policy, "status":"enabled"
            }),
        )
        .await;
    assert_eq!(status, 200, "{body}");
}

fn allow(actions: &[&str], resources: &[&str]) -> Value {
    json!({"Version":"2012-10-17","Statement":{
        "Effect":"Allow","Action":actions,"Resource":resources
    }})
}

fn status(response: &[u8]) -> u16 {
    std::str::from_utf8(&response[9..12])
        .expect("HTTP status")
        .parse()
        .expect("numeric HTTP status")
}

async fn send_local_presigned(
    server: &RunningServer,
    request: &PresignedRequest,
    body: &[u8],
) -> Vec<u8> {
    let uri: http::Uri = request.uri().parse().expect("presigned URI is valid");
    let path = uri.path_and_query().expect("presigned URI has a path");
    let headers = request
        .headers()
        .map(|(name, value)| format!("{name}: {value}\r\n"))
        .collect::<String>();
    server
        .raw(request.method(), path.as_str(), &headers, body)
        .await
}

fn assert_no_secret(value: &Value) {
    let serialized = value.to_string();
    assert!(
        !serialized.contains("secret"),
        "secret leaked: {serialized}"
    );
}
