// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Static token file reload, revocation, and malformed updates on paused time.

use std::path::Path;
use std::time::{Duration, SystemTime};

use super::static_token_file::StaticTokenHashes;
use super::watched_file::{WATCHED_FILE_CHECK_INTERVAL, WatchedFile};
use super::{CredentialAuthenticator, PublicApiAuthenticator, sha256};

const FIRST_TOKEN: &str = "first-static-token";
const SECOND_TOKEN: &str = "second-static-token";

fn token_file_for(tokens: &[&str]) -> String {
    let mut contents = String::from("tokens:\n");
    for token in tokens {
        let hash: String = sha256(token)
            .iter()
            .map(|byte| format!("{byte:02x}"))
            .collect();
        contents.push_str(&format!("  - sha256: {hash}\n"));
    }
    contents
}

/// Writes `contents` with a distinct modification time, so same-size rewrites are still detected.
fn write_version(path: &Path, contents: &str, version: u64) {
    std::fs::write(path, contents).expect("the token file is written");
    let modified = SystemTime::UNIX_EPOCH + Duration::from_secs(1_800_000_000 + version);
    std::fs::File::options()
        .write(true)
        .open(path)
        .expect("the token file opens")
        .set_modified(modified)
        .expect("the modification time is set");
}

async fn accepts(authenticator: &PublicApiAuthenticator, token: &str) -> bool {
    authenticator.authenticate(token).await.is_ok()
}

async fn next_check() {
    tokio::time::advance(WATCHED_FILE_CHECK_INTERVAL).await;
}

#[tokio::test(start_paused = true)]
async fn changes_apply_within_a_second_and_malformed_updates_keep_the_last_valid_tokens() {
    let directory = tempfile::TempDir::new().expect("a temporary directory");
    let path = directory.path().join("tokens.yaml");
    write_version(&path, &token_file_for(&[FIRST_TOKEN]), 1);
    let authenticator = PublicApiAuthenticator {
        authenticators: vec![CredentialAuthenticator::StaticTokens(
            WatchedFile::<StaticTokenHashes>::open(&path).expect("the token file loads"),
        )],
    };
    assert!(accepts(&authenticator, FIRST_TOKEN).await);
    assert!(!accepts(&authenticator, SECOND_TOKEN).await);

    write_version(&path, &token_file_for(&[SECOND_TOKEN]), 2);
    assert!(
        accepts(&authenticator, FIRST_TOKEN).await,
        "the file is not checked again within a second"
    );
    next_check().await;
    assert!(!accepts(&authenticator, FIRST_TOKEN).await);
    assert!(accepts(&authenticator, SECOND_TOKEN).await);

    write_version(&path, "tokens:\n  - sha256: not-hexadecimal\n", 3);
    next_check().await;
    assert!(accepts(&authenticator, SECOND_TOKEN).await);
    write_version(&path, "tokens: [", 4);
    next_check().await;
    assert!(accepts(&authenticator, SECOND_TOKEN).await);

    write_version(&path, &token_file_for(&[FIRST_TOKEN, SECOND_TOKEN]), 5);
    next_check().await;
    assert!(accepts(&authenticator, FIRST_TOKEN).await);
    assert!(accepts(&authenticator, SECOND_TOKEN).await);
}

#[tokio::test(start_paused = true)]
async fn an_empty_or_missing_file_revokes_every_token_until_it_returns() {
    let directory = tempfile::TempDir::new().expect("a temporary directory");
    let path = directory.path().join("tokens.yaml");
    write_version(&path, &token_file_for(&[FIRST_TOKEN]), 1);
    let authenticator = PublicApiAuthenticator {
        authenticators: vec![CredentialAuthenticator::StaticTokens(
            WatchedFile::<StaticTokenHashes>::open(&path).expect("the token file loads"),
        )],
    };
    assert!(accepts(&authenticator, FIRST_TOKEN).await);

    write_version(&path, "", 2);
    next_check().await;
    assert!(!accepts(&authenticator, FIRST_TOKEN).await);

    write_version(&path, &token_file_for(&[FIRST_TOKEN]), 3);
    next_check().await;
    assert!(accepts(&authenticator, FIRST_TOKEN).await);

    std::fs::remove_file(&path).expect("the token file is removed");
    next_check().await;
    assert!(!accepts(&authenticator, FIRST_TOKEN).await);
    next_check().await;
    assert!(!accepts(&authenticator, FIRST_TOKEN).await);

    write_version(&path, &token_file_for(&[FIRST_TOKEN]), 4);
    next_check().await;
    assert!(accepts(&authenticator, FIRST_TOKEN).await);
}

#[tokio::test(start_paused = true)]
async fn an_in_place_repair_with_unchanged_metadata_applies_after_a_malformed_update() {
    let directory = tempfile::TempDir::new().expect("a temporary directory");
    let path = directory.path().join("tokens.yaml");
    write_version(&path, &token_file_for(&[FIRST_TOKEN]), 1);
    let authenticator = PublicApiAuthenticator {
        authenticators: vec![CredentialAuthenticator::StaticTokens(
            WatchedFile::<StaticTokenHashes>::open(&path).expect("the token file loads"),
        )],
    };
    assert!(accepts(&authenticator, FIRST_TOKEN).await);

    let repaired = token_file_for(&[SECOND_TOKEN]);
    let hash_start = repaired.find("sha256: ").expect("the file names a hash") + "sha256: ".len();
    let mut malformed = repaired.clone();
    malformed.replace_range(hash_start..hash_start + 64, &"z".repeat(64));
    write_version(&path, &malformed, 2);
    let malformed_metadata = std::fs::metadata(&path).expect("the token file has metadata");
    next_check().await;
    assert!(accepts(&authenticator, FIRST_TOKEN).await);
    assert!(!accepts(&authenticator, SECOND_TOKEN).await);

    write_version(&path, &repaired, 2);
    let repaired_metadata = std::fs::metadata(&path).expect("the token file has metadata");
    assert_eq!(repaired_metadata.len(), malformed_metadata.len());
    assert_eq!(
        repaired_metadata.modified().expect("a modification time"),
        malformed_metadata.modified().expect("a modification time")
    );
    next_check().await;
    assert!(!accepts(&authenticator, FIRST_TOKEN).await);
    assert!(accepts(&authenticator, SECOND_TOKEN).await);
}
