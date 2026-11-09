CREATE TABLE IF NOT EXISTS dex_object_metadata (
    bucket BYTEA NOT NULL,
    object_key BYTEA NOT NULL,
    etag TEXT NOT NULL,
    object_size BIGINT NOT NULL,
    modified_micros BIGINT NOT NULL DEFAULT (EXTRACT(EPOCH FROM CURRENT_TIMESTAMP) * 1000000)::BIGINT,
    PRIMARY KEY (bucket, object_key)
);
CREATE TABLE IF NOT EXISTS dex_object_chunks (
    bucket BYTEA NOT NULL,
    object_key BYTEA NOT NULL,
    chunk_index BIGINT NOT NULL,
    body BYTEA NOT NULL,
    PRIMARY KEY (bucket, object_key, chunk_index)
);
