// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

mod config;
mod pool;

use std::future::Future;
use std::path::Path;
use std::time::Duration;

use async_trait::async_trait;
use bytes::{Bytes, BytesMut};
use dexzd_postgres_connection::ConnectionSettings;
use tokio::io::{AsyncRead, AsyncReadExt};
use tokio_postgres::{Row, Transaction};
use uuid::Uuid;

use crate::file_download::TemporaryDownloadFile;
use crate::{
    ByteRange, ObjectContent, ObjectListing, ObjectMetadata, ObjectStore, ObjectStoreError,
    ObjectStoreErrorKind, ObjectStoreOperation, ObjectStoreResult, WriteCondition, WriteOutcome,
};
pub use config::Db9ObjectStoreConfig;
use pool::Db9Pool;

const CHUNK_BYTES: usize = 256 * 1024;

/// Transactional, chunked SQL storage for Dex logs, snapshots, leases, and values.
pub struct Db9ObjectStore {
    pool: Db9Pool,
    bucket: Vec<u8>,
    prefix: String,
    timeout: Duration,
}

struct Db9Request<'a> {
    operation: ObjectStoreOperation,
    key: &'a str,
    timeout: Duration,
}

impl Db9ObjectStore {
    /// Connects and creates provider tables; requires transactional SQL and row-level locks.
    pub async fn new(config: Db9ObjectStoreConfig) -> ObjectStoreResult<Self> {
        if config.bucket.trim().is_empty()
            || config.connection_string.trim().is_empty()
            || config.key_prefix.starts_with('/')
            || !(1..=64).contains(&config.pool_size)
            || config.request_timeout.is_zero()
        {
            return Err(ObjectStoreError::new(
                ObjectStoreOperation::Head,
                "",
                ObjectStoreErrorKind::Permanent,
                "db9 needs a connection string, bucket, relative prefix, 1-64 connections, and a positive timeout",
            ));
        }
        let settings = ConnectionSettings::parse(&config.connection_string).map_err(|_| {
            ObjectStoreError::new(
                ObjectStoreOperation::Head,
                "",
                ObjectStoreErrorKind::Permanent,
                "invalid db9 connection settings",
            )
        })?;
        let store = Self {
            pool: Db9Pool::new(settings, config.pool_size, config.request_timeout),
            bucket: config.bucket.into_bytes(),
            prefix: config.key_prefix,
            timeout: config.request_timeout,
        };
        let request = store.request(ObjectStoreOperation::Put, "");
        let mut slot = store.pool.acquire(request.operation, request.key).await?;
        let client = &mut slot.as_mut().expect("acquired session exists").client;
        request
            .sql(client.batch_execute(include_str!("schema.sql")))
            .await?;
        drop(slot);
        Ok(store)
    }

    fn request<'a>(&self, operation: ObjectStoreOperation, key: &'a str) -> Db9Request<'a> {
        Db9Request {
            operation,
            key,
            timeout: self.timeout,
        }
    }

    fn object_key(&self, key: &str) -> Vec<u8> {
        format!("{}{key}", self.prefix).into_bytes()
    }

    async fn write_reader<R: AsyncRead + Unpin + Send>(
        &self,
        key: &str,
        mut reader: R,
        condition: WriteCondition,
    ) -> ObjectStoreResult<WriteOutcome> {
        let request = self.request(ObjectStoreOperation::Put, key);
        let object_key = self.object_key(key);
        let etag = Uuid::new_v4().to_string();
        let mut slot = self.pool.acquire(request.operation, key).await?;
        let client = &mut slot.as_mut().expect("acquired session exists").client;
        let transaction = request.sql(client.transaction()).await?;
        let row = match condition {
            WriteCondition::Unconditional => request.sql(transaction.query_opt(
                "INSERT INTO dex_object_metadata (bucket,object_key,etag,object_size) VALUES ($1,$2,$3,0) \
                 ON CONFLICT (bucket,object_key) DO UPDATE SET etag=$3,object_size=0 RETURNING etag",
                &[&self.bucket, &object_key, &etag])).await?,
            WriteCondition::CreateOnly => request.sql(transaction.query_opt(
                "INSERT INTO dex_object_metadata (bucket,object_key,etag,object_size) VALUES ($1,$2,$3,0) \
                 ON CONFLICT (bucket,object_key) DO NOTHING RETURNING etag",
                &[&self.bucket, &object_key, &etag])).await?,
            WriteCondition::ReplaceIfEtagMatches { etag: previous } => request.sql(transaction.query_opt(
                "UPDATE dex_object_metadata SET etag=$3,object_size=0 \
                 WHERE bucket=$1 AND object_key=$2 AND etag=$4 RETURNING etag",
                &[&self.bucket, &object_key, &etag, &previous])).await?,
        };
        if row.is_none() {
            request.sql(transaction.rollback()).await?;
            return Ok(WriteOutcome::PreconditionFailed);
        }
        request
            .sql(transaction.execute(
                "DELETE FROM dex_object_chunks WHERE bucket=$1 AND object_key=$2",
                &[&self.bucket, &object_key],
            ))
            .await?;
        let mut buffer = vec![0; CHUNK_BYTES];
        let mut size = 0_i64;
        let mut index = 0_i64;
        loop {
            let mut filled = 0;
            while filled < buffer.len() {
                let count = reader
                    .read(&mut buffer[filled..])
                    .await
                    .map_err(|error| request.io(error))?;
                if count == 0 {
                    break;
                }
                filled += count;
            }
            if filled == 0 {
                break;
            }
            let body = &buffer[..filled];
            request.sql(transaction.execute(
                "INSERT INTO dex_object_chunks (bucket,object_key,chunk_index,body) VALUES ($1,$2,$3,$4)",
                &[&self.bucket, &object_key, &index, &body])).await?;
            size = size
                .checked_add(filled as i64)
                .ok_or_else(|| request.invalid("object exceeds SQL size limit"))?;
            index += 1;
        }
        request
            .sql(transaction.execute(
                "UPDATE dex_object_metadata SET object_size=$3,modified_micros=(EXTRACT(EPOCH FROM CURRENT_TIMESTAMP)*1000000)::BIGINT WHERE bucket=$1 AND object_key=$2",
                &[&self.bucket, &object_key, &size],
            ))
            .await?;
        request.sql(transaction.commit()).await?;
        Ok(WriteOutcome::Written { etag })
    }

    async fn read_object(
        &self,
        key: &str,
        range: Option<ByteRange>,
        path: Option<&Path>,
    ) -> ObjectStoreResult<Option<ObjectContent>> {
        let request = self.request(ObjectStoreOperation::Get, key);
        if range.is_some_and(|range| range.start > range.end_inclusive) {
            return Err(request.invalid("range start exceeds end"));
        }
        let object_key = self.object_key(key);
        let mut slot = self.pool.acquire(request.operation, key).await?;
        let client = &mut slot.as_mut().expect("acquired session exists").client;
        let transaction = request.sql(client.transaction()).await?;
        // Hold the metadata lock until every chunk has been read from this object version.
        let row = request.sql(transaction.query_opt(
            "SELECT etag,object_size FROM dex_object_metadata WHERE bucket=$1 AND object_key=$2 FOR SHARE",
            &[&self.bucket, &object_key])).await?;
        let Some(row) = row else {
            request.sql(transaction.rollback()).await?;
            return Ok(None);
        };
        let size: i64 = row.try_get(1).map_err(|error| request.database(error))?;
        let size = u64::try_from(size).map_err(|_| request.corrupt("negative object size"))?;
        let etag = row.try_get(0).map_err(|error| request.database(error))?;
        let start = range.map_or(0, |range| range.start.min(size));
        let end = range.map_or(size, |range| {
            range.end_inclusive.saturating_add(1).min(size)
        });
        let mut output = BytesMut::new();
        let mut download = match path {
            Some(path) => Some(TemporaryDownloadFile::create(key, path).await?),
            None => None,
        };
        let mut position = start;
        while position < end {
            let index = (position / CHUNK_BYTES as u64) as i64;
            let row = request.sql(transaction.query_opt(
                "SELECT body FROM dex_object_chunks WHERE bucket=$1 AND object_key=$2 AND chunk_index=$3",
                &[&self.bucket, &object_key, &index])).await?
                .ok_or_else(|| request.corrupt("missing object chunk"))?;
            let body: Vec<u8> = row.try_get(0).map_err(|error| request.database(error))?;
            let chunk_start = index as u64 * CHUNK_BYTES as u64;
            let expected = (size - chunk_start).min(CHUNK_BYTES as u64) as usize;
            if body.len() != expected {
                return Err(request.corrupt("invalid object chunk length"));
            }
            let from = (position - chunk_start) as usize;
            let to = (end - chunk_start).min(body.len() as u64) as usize;
            let selected = &body[from..to];
            match &mut download {
                Some(download) => download.write_all(selected).await?,
                None => output.extend_from_slice(selected),
            }
            position += selected.len() as u64;
        }
        request.sql(transaction.commit()).await?;
        if let Some(download) = download {
            download.commit().await?;
        }
        Ok(Some(ObjectContent {
            bytes: output.freeze(),
            etag,
            object_size: size,
        }))
    }

    async fn delete_in_transaction(
        &self,
        transaction: &Transaction<'_>,
        request: &Db9Request<'_>,
        key: &str,
    ) -> ObjectStoreResult<()> {
        let object_key = self.object_key(key);
        let removed = request
            .sql(transaction.execute(
                "DELETE FROM dex_object_metadata WHERE bucket=$1 AND object_key=$2",
                &[&self.bucket, &object_key],
            ))
            .await?;
        if removed == 0 {
            return Ok(());
        }
        request
            .sql(transaction.execute(
                "DELETE FROM dex_object_chunks WHERE bucket=$1 AND object_key=$2",
                &[&self.bucket, &object_key],
            ))
            .await?;
        Ok(())
    }

    fn metadata(&self, request: &Db9Request<'_>, row: Row) -> ObjectStoreResult<ObjectMetadata> {
        let full_key: Vec<u8> = row.try_get(0).map_err(|error| request.database(error))?;
        let full_key =
            String::from_utf8(full_key).map_err(|_| request.corrupt("object key is not UTF-8"))?;
        let key = full_key
            .strip_prefix(&self.prefix)
            .ok_or_else(|| request.corrupt("object escaped configured prefix"))?;
        let size: i64 = row.try_get(1).map_err(|error| request.database(error))?;
        Ok(ObjectMetadata {
            key: key.to_owned(),
            size: u64::try_from(size).map_err(|_| request.corrupt("negative object size"))?,
            etag: row.try_get(2).map_err(|error| request.database(error))?,
            last_modified_unix_micros: Some(
                row.try_get(3).map_err(|error| request.database(error))?,
            ),
        })
    }
}

#[async_trait]
impl ObjectStore for Db9ObjectStore {
    async fn put(
        &self,
        key: &str,
        body: Bytes,
        condition: WriteCondition,
    ) -> ObjectStoreResult<WriteOutcome> {
        self.write_reader(key, body.as_ref(), condition).await
    }

    async fn put_file(
        &self,
        key: &str,
        path: &Path,
        condition: WriteCondition,
    ) -> ObjectStoreResult<WriteOutcome> {
        let file = tokio::fs::File::open(path)
            .await
            .map_err(|error| self.request(ObjectStoreOperation::Put, key).io(error))?;
        self.write_reader(key, file, condition).await
    }

    async fn get(&self, key: &str) -> ObjectStoreResult<Option<ObjectContent>> {
        self.read_object(key, None, None).await
    }

    async fn get_range(
        &self,
        key: &str,
        range: ByteRange,
    ) -> ObjectStoreResult<Option<ObjectContent>> {
        self.read_object(key, Some(range), None).await
    }

    async fn get_to_file(&self, key: &str, path: &Path) -> ObjectStoreResult<Option<u64>> {
        self.read_object(key, None, Some(path))
            .await
            .map(|content| content.map(|content| content.object_size))
    }

    async fn head(&self, key: &str) -> ObjectStoreResult<Option<ObjectMetadata>> {
        let request = self.request(ObjectStoreOperation::Head, key);
        let object_key = self.object_key(key);
        let slot = self.pool.acquire(request.operation, key).await?;
        let client = &slot.as_ref().expect("acquired session exists").client;
        let row = request.sql(client.query_opt(
            "SELECT object_key,object_size,etag,modified_micros FROM dex_object_metadata WHERE bucket=$1 AND object_key=$2",
            &[&self.bucket, &object_key])).await?;
        row.map(|row| self.metadata(&request, row)).transpose()
    }

    async fn delete(&self, key: &str) -> ObjectStoreResult<()> {
        self.delete_many(&[key.to_owned()]).await
    }

    async fn delete_many(&self, keys: &[String]) -> ObjectStoreResult<()> {
        let request = self.request(ObjectStoreOperation::Delete, "");
        if keys.len() > 1000 {
            return Err(request.invalid("at most 1000 keys may be deleted"));
        }
        // Lock in key order so overlapping batch deletions cannot deadlock with each other.
        let mut keys = keys.iter().collect::<Vec<_>>();
        keys.sort_unstable();
        keys.dedup();
        let mut slot = self.pool.acquire(request.operation, "").await?;
        let client = &mut slot.as_mut().expect("acquired session exists").client;
        let transaction = request.sql(client.transaction()).await?;
        for key in keys {
            self.delete_in_transaction(&transaction, &request, key)
                .await?;
        }
        request.sql(transaction.commit()).await?;
        Ok(())
    }

    async fn list(
        &self,
        prefix: &str,
        start_after: Option<&str>,
        max_keys: u32,
    ) -> ObjectStoreResult<ObjectListing> {
        let request = self.request(ObjectStoreOperation::List, prefix);
        if !(1..=1000).contains(&max_keys) {
            return Err(request.invalid("max_keys must be 1-1000"));
        }
        let lower = self.object_key(prefix);
        let after = start_after.map(|key| self.object_key(key));
        let mut upper = lower.clone();
        while upper.last() == Some(&255) {
            upper.pop();
        }
        let upper = if let Some(last) = upper.last_mut() {
            *last += 1;
            Some(upper)
        } else {
            None
        };
        let limit = i64::from(max_keys) + 1;
        let slot = self.pool.acquire(request.operation, prefix).await?;
        let client = &slot.as_ref().expect("acquired session exists").client;
        let rows = request
            .sql(client.query(
                "SELECT object_key,object_size,etag,modified_micros FROM dex_object_metadata WHERE bucket=$1 \
             AND object_key >= $2 AND ($3::bytea IS NULL OR object_key < $3) \
             AND ($4::bytea IS NULL OR object_key > $4) ORDER BY object_key LIMIT $5",
                &[&self.bucket, &lower, &upper, &after, &limit],
            ))
            .await?;
        let has_more = rows.len() > max_keys as usize;
        let objects = rows
            .into_iter()
            .take(max_keys as usize)
            .map(|row| self.metadata(&request, row))
            .collect::<ObjectStoreResult<Vec<_>>>()?;
        let next_start_after = if has_more {
            objects.last().map(|object| object.key.clone())
        } else {
            None
        };
        Ok(ObjectListing {
            objects,
            next_start_after,
        })
    }
}

impl Db9Request<'_> {
    async fn sql<T>(
        &self,
        future: impl Future<Output = Result<T, tokio_postgres::Error>>,
    ) -> ObjectStoreResult<T> {
        tokio::time::timeout(self.timeout, future)
            .await
            .map_err(|_| {
                ObjectStoreError::new(
                    self.operation,
                    self.key,
                    self.uncertain_kind(),
                    "db9 SQL deadline exceeded",
                )
            })?
            .map_err(|error| self.database(error))
    }

    fn database(&self, error: tokio_postgres::Error) -> ObjectStoreError {
        let code = error.code().map(|code| code.code());
        let kind = match code {
            Some("40001" | "40P01" | "55P03" | "57014") => ObjectStoreErrorKind::Transient,
            Some(code)
                if !["08", "57", "58", "XX"]
                    .iter()
                    .any(|prefix| code.starts_with(prefix)) =>
            {
                ObjectStoreErrorKind::Permanent
            }
            _ => self.uncertain_kind(),
        };
        // Server messages may contain credentials or payloads; expose SQLSTATE only.
        ObjectStoreError::new(
            self.operation,
            self.key,
            kind,
            format!(
                "db9 SQL failure (SQLSTATE {})",
                code.unwrap_or("unavailable")
            ),
        )
    }

    fn uncertain_kind(&self) -> ObjectStoreErrorKind {
        match self.operation {
            ObjectStoreOperation::Put | ObjectStoreOperation::Delete => {
                ObjectStoreErrorKind::OutcomeUnknown
            }
            ObjectStoreOperation::Get | ObjectStoreOperation::Head | ObjectStoreOperation::List => {
                ObjectStoreErrorKind::Transient
            }
        }
    }

    fn invalid(&self, message: &str) -> ObjectStoreError {
        ObjectStoreError::new(
            self.operation,
            self.key,
            ObjectStoreErrorKind::Permanent,
            message,
        )
    }

    fn corrupt(&self, message: &str) -> ObjectStoreError {
        ObjectStoreError::new(
            self.operation,
            self.key,
            ObjectStoreErrorKind::Corrupt,
            message,
        )
    }

    fn io(&self, error: std::io::Error) -> ObjectStoreError {
        self.invalid(&format!("local object file I/O failed: {error}"))
    }
}
