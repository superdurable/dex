// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Write conditions and integrity rules that differ between the emulated S3 dialects.

use http::{HeaderMap, StatusCode};
use s3local::WriteCondition;

use crate::config::S3ApiDialect;
use crate::request_headers::{header_text, write_condition};
use crate::s3_error::S3Error;

const FORBID_OVERWRITE_HEADER: &str = "x-oss-forbid-overwrite";

/// The write condition of a PutObject or CompleteMultipartUpload, and how its rejection is reported.
pub(super) struct DialectWriteCondition {
    pub(super) condition: WriteCondition,
    is_forbid_overwrite: bool,
}

impl DialectWriteCondition {
    /// Parses the conditional-write headers that `dialect` understands.
    pub(super) fn parse(
        headers: &HeaderMap,
        operation: &str,
        dialect: S3ApiDialect,
    ) -> Result<Self, S3Error> {
        if dialect == S3ApiDialect::AmazonS3 {
            return Ok(Self {
                condition: write_condition(headers, operation)?,
                is_forbid_overwrite: false,
            });
        }
        if headers.contains_key("if-none-match") || headers.contains_key("if-match") {
            // Aliyun OSS answers 400, not S3's 501, for conditional writes.
            return Err(S3Error::new(
                StatusCode::BAD_REQUEST,
                "NotImplemented",
                format!("{operation} with If-None-Match or If-Match is not implemented"),
            ));
        }
        let is_forbid_overwrite = header_text(headers, FORBID_OVERWRITE_HEADER)
            .is_some_and(|value| value.trim().eq_ignore_ascii_case("true"));
        Ok(Self {
            condition: if is_forbid_overwrite {
                WriteCondition::ForbidOverwriteUnlessVersioned
            } else {
                WriteCondition::Unconditional
            },
            is_forbid_overwrite,
        })
    }

    /// Reports a forbid-overwrite rejection as Aliyun OSS does; other errors pass through.
    pub(super) fn translate_error(&self, error: S3Error) -> S3Error {
        if self.is_forbid_overwrite && error.code == "PreconditionFailed" {
            return S3Error::new(
                StatusCode::CONFLICT,
                "FileAlreadyExists",
                "The object you specified already exists and can not be overwritten.",
            );
        }
        error
    }
}

/// Aliyun OSS requires `Content-MD5` on DeleteObjects; its value is verified with the body.
pub(super) fn require_delete_objects_content_md5(
    headers: &HeaderMap,
    dialect: S3ApiDialect,
) -> Result<(), S3Error> {
    if dialect == S3ApiDialect::AliyunOss && header_text(headers, "content-md5").is_none() {
        return Err(S3Error::new(
            StatusCode::BAD_REQUEST,
            "MissingArgument",
            "Missing Some Required Arguments: Content-MD5",
        ));
    }
    Ok(())
}

/// Aliyun OSS UploadPart responses carry only the ETag.
pub(super) fn echoes_part_checksum(dialect: S3ApiDialect) -> bool {
    dialect == S3ApiDialect::AmazonS3
}
