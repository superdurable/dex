// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::net::SocketAddr;
use std::path::PathBuf;

use clap::{Args, Parser, Subcommand};

use crate::config::DevDurabilityMode;

/// Command line of `dex-server-zd`.
#[derive(Debug, Parser)]
#[command(
    name = "dex-server-zd",
    version,
    about = "Dex Server with object storage as its only dependency"
)]
pub struct CommandLineOptions {
    /// The command to run.
    #[command(subcommand)]
    pub command: ServerCommand,
}

/// Commands of `dex-server-zd`.
#[derive(Debug, Subcommand)]
pub enum ServerCommand {
    /// Runs a single-node development Server with an embedded S3 emulator.
    Dev(DevCommandOptions),
    /// Runs a single-node production Server with an embedded local object store on --data-dir.
    ///
    /// It takes the flags and data directory of `dev`, with production defaults such as a
    /// 30-second Step attempt drain.
    Local(DevCommandOptions),
    /// Runs a Server node from a YAML configuration file against external object storage.
    Serve(ServeCommandOptions),
}

/// Options of `dex-server-zd dev`. `dexcli dev -engine zd` depends on these exact flags.
/// `dex-server-zd local` takes the same flags.
#[derive(Debug, Args)]
pub struct DevCommandOptions {
    /// Directory for all local state: the embedded S3 emulator data and the local state store.
    #[arg(long)]
    pub data_dir: PathBuf,
    /// Plaintext gRPC address that serves FlowService and WorkerTaskService.
    ///
    /// Overrides `api.listen` from `--config`. [default: 127.0.0.1:8801, or `api.listen` when
    /// `--config` sets it]
    #[arg(long)]
    pub listen: Option<SocketAddr>,
    /// Optional directory that also receives `dex-server-zd.log`.
    #[arg(long)]
    pub log_dir: Option<PathBuf>,
    /// Optional YAML file with configuration overrides.
    #[arg(long)]
    pub config: Option<PathBuf>,
    /// Optional Go Dex Server YAML file whose `attributeStore` section configures Attribute
    /// synchronization; its other sections are ignored. The section must define at least one store,
    /// and `--config` must not also set `attributeStore`.
    #[arg(long)]
    pub attribute_store_config: Option<PathBuf>,
    /// Optional embedded S3 durability; overrides `dev.durabilityMode` from `--config`. Defaults to strict.
    #[arg(long, value_enum)]
    pub durability: Option<DevDurabilityMode>,
    /// Optional loopback address of a second S3 emulator that serves `--application-s3-bucket`
    /// for applications, with its data in `<data-dir>/application-s3`.
    ///
    /// It is separate from the server's own object store. Defaults to none, which serves no
    /// application bucket.
    #[arg(long, requires = "application_s3_bucket")]
    pub application_s3_listen: Option<SocketAddr>,
    /// The bucket that `--application-s3-listen` serves; created when missing. Requires
    /// `--application-s3-listen`.
    #[arg(long, requires = "application_s3_listen")]
    pub application_s3_bucket: Option<String>,
}

/// Options of `dex-server-zd serve`.
#[derive(Debug, Args)]
pub struct ServeCommandOptions {
    /// YAML configuration file; `config/dex-server-zd.example.yaml` lists every key and default.
    #[arg(long)]
    pub config: PathBuf,
}

#[cfg(test)]
mod tests {
    use clap::Parser;

    use super::{CommandLineOptions, ServerCommand};
    use crate::config::DevDurabilityMode;

    #[test]
    fn dev_command_accepts_the_flags_dexcli_passes() {
        let options = CommandLineOptions::try_parse_from([
            "dex-server-zd",
            "dev",
            "--data-dir",
            "/tmp/zd",
            "--listen",
            "127.0.0.1:9901",
            "--log-dir",
            "/tmp/zd-logs",
            "--config",
            "/tmp/zd.yaml",
            "--attribute-store-config",
            "/tmp/dex.yaml",
            "--durability",
            "process-crash-safe",
            "--application-s3-listen",
            "127.0.0.1:8803",
            "--application-s3-bucket",
            "dex-dev-media",
        ])
        .unwrap();
        let ServerCommand::Dev(dev) = options.command else {
            panic!("dev parses as the dev command");
        };
        assert_eq!(dev.data_dir.to_str(), Some("/tmp/zd"));
        assert_eq!(
            dev.application_s3_listen
                .map(|listen| listen.to_string())
                .as_deref(),
            Some("127.0.0.1:8803")
        );
        assert_eq!(dev.application_s3_bucket.as_deref(), Some("dex-dev-media"));
        assert_eq!(
            dev.listen.map(|listen| listen.to_string()).as_deref(),
            Some("127.0.0.1:9901")
        );
        assert_eq!(dev.log_dir.unwrap().to_str(), Some("/tmp/zd-logs"));
        assert_eq!(dev.config.unwrap().to_str(), Some("/tmp/zd.yaml"));
        assert_eq!(
            dev.attribute_store_config.unwrap().to_str(),
            Some("/tmp/dex.yaml")
        );
        assert_eq!(dev.durability, Some(DevDurabilityMode::ProcessCrashSafe));
    }

    #[test]
    fn dev_command_requires_the_application_s3_flags_together() {
        for flag in [
            ["--application-s3-listen", "127.0.0.1:8803"],
            ["--application-s3-bucket", "dex-dev-media"],
        ] {
            assert!(
                CommandLineOptions::try_parse_from(
                    ["dex-server-zd", "dev", "--data-dir", "/tmp/zd"]
                        .into_iter()
                        .chain(flag)
                )
                .is_err(),
                "{} alone must be rejected",
                flag[0]
            );
        }
    }

    #[test]
    fn dev_command_accepts_every_durability_and_rejects_others() {
        for (value, expected) in [
            ("strict", DevDurabilityMode::Strict),
            ("process-crash-safe", DevDurabilityMode::ProcessCrashSafe),
            ("relaxed", DevDurabilityMode::Relaxed),
        ] {
            let options = CommandLineOptions::try_parse_from([
                "dex-server-zd",
                "dev",
                "--data-dir",
                "/tmp/zd",
                "--durability",
                value,
            ])
            .unwrap();
            let ServerCommand::Dev(dev) = options.command else {
                panic!("dev parses as the dev command");
            };
            assert_eq!(dev.durability, Some(expected));
        }
        for value in ["processCrashSafe", "fsync", ""] {
            assert!(
                CommandLineOptions::try_parse_from([
                    "dex-server-zd",
                    "dev",
                    "--data-dir",
                    "/tmp/zd",
                    "--durability",
                    value,
                ])
                .is_err(),
                "--durability {value:?} must be rejected"
            );
        }
    }

    #[test]
    fn dev_command_leaves_an_absent_listen_address_to_the_configuration() {
        let options =
            CommandLineOptions::try_parse_from(["dex-server-zd", "dev", "--data-dir", "/tmp/zd"])
                .unwrap();
        let ServerCommand::Dev(dev) = options.command else {
            panic!("dev parses as the dev command");
        };
        assert!(
            dev.listen.is_none(),
            "the configuration supplies the default listen address"
        );
        assert!(dev.log_dir.is_none());
        assert!(dev.durability.is_none());
    }

    #[test]
    fn dev_command_rejects_missing_data_dir_and_bad_listen_address() {
        assert!(CommandLineOptions::try_parse_from(["dex-server-zd", "dev"]).is_err());
        assert!(
            CommandLineOptions::try_parse_from([
                "dex-server-zd",
                "dev",
                "--data-dir",
                "/tmp/zd",
                "--listen",
                "localhost",
            ])
            .is_err()
        );
    }

    #[test]
    fn local_command_accepts_the_flags_and_defaults_of_dev() {
        let options = CommandLineOptions::try_parse_from([
            "dex-server-zd",
            "local",
            "--data-dir",
            "/var/lib/dex",
            "--listen",
            "127.0.0.1:9901",
            "--log-dir",
            "/var/log/dex",
            "--config",
            "/etc/dex/local.yaml",
            "--attribute-store-config",
            "/etc/dex/dex.yaml",
            "--durability",
            "relaxed",
        ])
        .unwrap();
        let ServerCommand::Local(local) = options.command else {
            panic!("local parses as the local command");
        };
        assert_eq!(local.data_dir.to_str(), Some("/var/lib/dex"));
        assert_eq!(
            local.listen.map(|listen| listen.to_string()).as_deref(),
            Some("127.0.0.1:9901")
        );
        assert_eq!(local.log_dir.unwrap().to_str(), Some("/var/log/dex"));
        assert_eq!(local.config.unwrap().to_str(), Some("/etc/dex/local.yaml"));
        assert_eq!(
            local.attribute_store_config.unwrap().to_str(),
            Some("/etc/dex/dex.yaml")
        );
        assert_eq!(local.durability, Some(DevDurabilityMode::Relaxed));

        let defaults = CommandLineOptions::try_parse_from([
            "dex-server-zd",
            "local",
            "--data-dir",
            "/var/lib/dex",
        ])
        .unwrap();
        let ServerCommand::Local(local) = defaults.command else {
            panic!("local parses as the local command");
        };
        assert!(
            local.listen.is_none(),
            "the configuration supplies the default listen address"
        );
        assert!(local.log_dir.is_none());
        assert!(local.config.is_none());
        assert!(local.durability.is_none());
        assert!(CommandLineOptions::try_parse_from(["dex-server-zd", "local"]).is_err());
        assert!(
            CommandLineOptions::try_parse_from([
                "dex-server-zd",
                "local",
                "--data-dir",
                "/var/lib/dex",
                "--durability",
                "fsync",
            ])
            .is_err()
        );
    }

    #[test]
    fn serve_command_requires_a_config_file() {
        let options = CommandLineOptions::try_parse_from([
            "dex-server-zd",
            "serve",
            "--config",
            "/etc/dex/dex-server-zd.yaml",
        ])
        .unwrap();
        let ServerCommand::Serve(serve) = options.command else {
            panic!("serve parses as the serve command");
        };
        assert_eq!(serve.config.to_str(), Some("/etc/dex/dex-server-zd.yaml"));
        assert!(CommandLineOptions::try_parse_from(["dex-server-zd", "serve"]).is_err());
        assert!(
            CommandLineOptions::try_parse_from([
                "dex-server-zd",
                "serve",
                "--config",
                "/etc/dex/dex-server-zd.yaml",
                "--listen",
                "127.0.0.1:8801",
            ])
            .is_err()
        );
    }
}
