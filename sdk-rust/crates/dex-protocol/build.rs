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
    let protos = [std::path::PathBuf::from("../../../protos/dex.proto")];
    let includes = [
        std::path::PathBuf::from("../../../protos"),
        protoc_bin_vendored::include_path()?,
    ];

    tonic_prost_build::configure().compile_with_config(prost_config, &protos, &includes)?;
    println!("cargo:rerun-if-changed=../../../protos/dex.proto");
    Ok(())
}
