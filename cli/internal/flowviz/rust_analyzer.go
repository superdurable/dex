// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package flowviz

import (
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
)

//go:embed rust_analyzer/Cargo.toml rust_analyzer/Cargo.lock rust_analyzer/src/*.rs
var rustAnalyzerCrate embed.FS

const rustAnalyzerSourceDirectory = "rust_analyzer"
const rustAnalyzerExecutable = "dex-flowviz-rust"

func analyzeRust(ctx context.Context, sourcePath string, source []byte, cargoPath string, flowType string) (*Graph, error) {
	if strings.TrimSpace(cargoPath) == "" {
		cargoPath = "cargo"
	}
	cacheDirectory, err := os.UserCacheDir()
	if err != nil {
		return rustAnalyzerFailure(sourcePath, err), nil
	}
	binary, err := resolveRustAnalyzerBinary(ctx, cargoPath, cacheDirectory, rustAnalyzerCrate)
	if err != nil {
		return rustAnalyzerFailure(sourcePath, err), nil
	}
	request, err := json.Marshal(map[string]string{"path": sourcePath, "source": string(source), "flowType": flowType})
	if err != nil {
		return nil, fmt.Errorf("encode Rust analyzer input: %w", err)
	}
	command := exec.CommandContext(ctx, binary)
	command.Stdin = bytes.NewReader(request)
	output, err := command.Output()
	if err != nil {
		return rustAnalyzerFailure(sourcePath, rustAnalyzerProcessError("run Rust analyzer", err)), nil
	}
	var result scriptAnalysis
	if err := json.Unmarshal(output, &result); err != nil {
		return nil, fmt.Errorf("decode Rust analyzer output: %w", err)
	}
	return result.definition(), nil
}

func rustAnalyzerFailure(sourcePath string, err error) *Graph {
	graph := NewGraph("rust", sourcePath)
	graph.AddDiagnostic("error", "rust_analyzer_failed", err.Error(), nil)
	return graph
}

func resolveRustAnalyzerBinary(ctx context.Context, cargoPath string, cacheDirectory string, crateFiles fs.FS) (string, error) {
	cargo, err := exec.LookPath(cargoPath)
	if err != nil {
		return "", fmt.Errorf("find Cargo: %w", err)
	}
	cargo, err = filepath.Abs(cargo)
	if err != nil {
		return "", fmt.Errorf("resolve Cargo path: %w", err)
	}
	versionCommand := exec.CommandContext(ctx, cargo, "--version")
	versionCommand.Dir = cacheDirectory
	if err := os.MkdirAll(cacheDirectory, 0o700); err != nil {
		return "", fmt.Errorf("create user cache directory: %w", err)
	}
	version, err := versionCommand.Output()
	if err != nil {
		return "", rustAnalyzerProcessError("read Cargo version", err)
	}
	directory, err := rustAnalyzerCacheDirectory(cacheDirectory, version, crateFiles)
	if err != nil {
		return "", err
	}
	binary := filepath.Join(directory, rustAnalyzerExecutable)
	if rustAnalyzerBinaryExists(binary) {
		return binary, nil
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", fmt.Errorf("create Rust analyzer cache: %w", err)
	}
	lock, err := os.OpenFile(filepath.Join(directory, "build.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return "", fmt.Errorf("open Rust analyzer build lock: %w", err)
	}
	defer lock.Close()
	if err := lockRustAnalyzerBuild(ctx, lock); err != nil {
		return "", err
	}
	if rustAnalyzerBinaryExists(binary) {
		return binary, nil
	}
	if err := buildRustAnalyzer(ctx, cargo, directory, binary, crateFiles); err != nil {
		return "", err
	}
	return binary, nil
}

func rustAnalyzerCacheDirectory(cacheDirectory string, cargoVersion []byte, crateFiles fs.FS) (string, error) {
	digest := sha256.New()
	fmt.Fprintf(digest, "%s/%s:%d:", runtime.GOOS, runtime.GOARCH, len(cargoVersion))
	digest.Write(cargoVersion)
	err := fs.WalkDir(crateFiles, rustAnalyzerSourceDirectory, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		contents, err := fs.ReadFile(crateFiles, path)
		if err != nil {
			return err
		}
		fmt.Fprintf(digest, "%s:%d:", path, len(contents))
		digest.Write(contents)
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("hash embedded Rust analyzer crate: %w", err)
	}
	return filepath.Join(cacheDirectory, "dexcli", "flowviz-rust", fmt.Sprintf("%x", digest.Sum(nil))), nil
}

func rustAnalyzerBinaryExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o100 != 0
}

func lockRustAnalyzerBuild(ctx context.Context, lock *os.File) error {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EINTR) {
			return fmt.Errorf("lock Rust analyzer build: %w", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func buildRustAnalyzer(ctx context.Context, cargo string, directory string, binary string, crateFiles fs.FS) error {
	crate, err := fs.Sub(crateFiles, rustAnalyzerSourceDirectory)
	if err != nil {
		return fmt.Errorf("open embedded Rust analyzer crate: %w", err)
	}
	if err := writeRustAnalyzerSources(directory, crate); err != nil {
		return err
	}
	log, err := os.OpenFile(filepath.Join(directory, "build.log"), os.O_CREATE|os.O_TRUNC|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("open Rust analyzer build log: %w", err)
	}
	defer log.Close()
	target := filepath.Join(directory, "target")
	command := exec.CommandContext(ctx, cargo, "build", "--release", "--locked", "--target-dir", target)
	command.Dir = directory
	for _, setting := range command.Environ() {
		if !strings.HasPrefix(setting, "CARGO_BUILD_TARGET=") {
			command.Env = append(command.Env, setting)
		}
	}
	command.Stdout, command.Stderr = log, log
	if err := command.Run(); err != nil {
		return fmt.Errorf("build Rust analyzer: %w (%s)%s", err, log.Name(), rustAnalyzerBuildLogTail(log))
	}
	if err := os.Rename(filepath.Join(target, "release", rustAnalyzerExecutable), binary); err != nil {
		return fmt.Errorf("install cached Rust analyzer: %w", err)
	}
	return nil
}

func writeRustAnalyzerSources(directory string, crate fs.FS) error {
	err := fs.WalkDir(crate, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		destination := filepath.Join(directory, filepath.FromSlash(path))
		if entry.IsDir() {
			return os.MkdirAll(destination, 0o700)
		}
		contents, err := fs.ReadFile(crate, path)
		if err != nil {
			return err
		}
		return os.WriteFile(destination, contents, 0o600)
	})
	if err != nil {
		return fmt.Errorf("write Rust analyzer crate: %w", err)
	}
	return nil
}

func rustAnalyzerBuildLogTail(log *os.File) string {
	info, err := log.Stat()
	if err != nil {
		return ""
	}
	start := max(0, info.Size()-8192)
	contents, err := io.ReadAll(io.NewSectionReader(log, start, info.Size()-start))
	if err != nil || len(contents) == 0 {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(string(contents)), "\n")
	return "\n" + strings.Join(lines[max(0, len(lines)-12):], "\n")
}

func rustAnalyzerProcessError(operation string, err error) error {
	var exitError *exec.ExitError
	if errors.As(err, &exitError) && len(exitError.Stderr) > 0 {
		return fmt.Errorf("%s: %w: %s", operation, err, strings.TrimSpace(string(exitError.Stderr)))
	}
	return fmt.Errorf("%s: %w", operation, err)
}
