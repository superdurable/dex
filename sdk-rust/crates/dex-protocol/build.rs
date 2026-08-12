// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

fn main() -> Result<(), Box<dyn std::error::Error>> {
    let mut prost_config = tonic_prost_build::Config::new();
    prost_config.protoc_executable(protoc_bin_vendored::protoc_bin_path()?);
    let bundled_proto = std::path::PathBuf::from("proto/dex.proto");
    let repository_proto = std::path::PathBuf::from("../../../protos/dex.proto");
    let proto = if bundled_proto.exists() {
        bundled_proto
    } else {
        repository_proto
    };
    let proto_include = proto
        .parent()
        .ok_or("Dex proto must have a parent directory")?
        .to_path_buf();
    let protos = [proto.clone()];
    let includes = [proto_include, protoc_bin_vendored::include_path()?];

    tonic_prost_build::configure().compile_with_config(prost_config, &protos, &includes)?;
    println!("cargo:rerun-if-changed={}", proto.display());
    Ok(())
}
