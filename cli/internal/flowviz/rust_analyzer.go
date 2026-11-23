// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package flowviz

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
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
	cache, err := resolveRustAnalyzerBinary(ctx, cargoPath, cacheDirectory, rustAnalyzerCrate)
	if err != nil {
		return rustAnalyzerFailure(sourcePath, err), nil
	}
	defer cache.lock.Close()
	request, err := json.Marshal(map[string]string{"path": sourcePath, "source": string(source), "flowType": flowType})
	if err != nil {
		return nil, fmt.Errorf("encode Rust analyzer input: %w", err)
	}
	command := exec.CommandContext(ctx, cache.binary)
	command.Stdin = bytes.NewReader(request)
	command.Env = slices.DeleteFunc(command.Environ(), func(setting string) bool {
		return strings.HasPrefix(setting, "RUST_BACKTRACE=")
	})
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

type cachedRustAnalyzer struct {
	binary string
	lock   *os.File
}

func resolveRustAnalyzerBinary(ctx context.Context, cargoPath string, cacheDirectory string, crateFiles fs.FS) (cachedRustAnalyzer, error) {
	cargo, err := exec.LookPath(cargoPath)
	if err != nil {
		return cachedRustAnalyzer{}, fmt.Errorf("find Cargo: %w", err)
	}
	cargo, err = filepath.Abs(cargo)
	if err != nil {
		return cachedRustAnalyzer{}, fmt.Errorf("resolve Cargo path: %w", err)
	}
	versionCommand := exec.CommandContext(ctx, cargo, "--version")
	versionCommand.Dir = cacheDirectory
	if err := os.MkdirAll(cacheDirectory, 0o700); err != nil {
		return cachedRustAnalyzer{}, fmt.Errorf("create user cache directory: %w", err)
	}
	version, err := versionCommand.Output()
	if err != nil {
		return cachedRustAnalyzer{}, rustAnalyzerProcessError("read Cargo version", err)
	}
	directory, err := rustAnalyzerCacheDirectory(cacheDirectory, version, crateFiles)
	if err != nil {
		return cachedRustAnalyzer{}, err
	}
	root := filepath.Dir(directory)
	if err := os.MkdirAll(root, 0o700); err != nil {
		return cachedRustAnalyzer{}, fmt.Errorf("create Rust analyzer cache: %w", err)
	}
	lock, err := os.OpenFile(filepath.Join(root, "build.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return cachedRustAnalyzer{}, fmt.Errorf("open Rust analyzer build lock: %w", err)
	}
	if err := lockRustAnalyzerBuild(ctx, lock); err != nil {
		lock.Close()
		return cachedRustAnalyzer{}, err
	}
	binary := filepath.Join(directory, rustAnalyzerExecutable)
	if !rustAnalyzerBinaryExists(binary) {
		fmt.Fprintln(os.Stderr, "building the Rust analyzer, first run only")
		if err := buildRustAnalyzer(ctx, cargo, directory, binary, crateFiles); err != nil {
			lock.Close()
			return cachedRustAnalyzer{}, err
		}
	}
	if err := pruneRustAnalyzerCache(directory); err != nil {
		lock.Close()
		return cachedRustAnalyzer{}, err
	}
	return cachedRustAnalyzer{binary: binary, lock: lock}, nil
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
	command := exec.CommandContext(ctx, cargo, "build", "--release", "--locked", "--target-dir", target, "--message-format=json")
	command.Dir = directory
	command.Stderr = log
	output, buildErr := command.Output()
	executable, err := rustAnalyzerCargoExecutable(output, log)
	if err != nil {
		return err
	}
	if buildErr != nil {
		tail := rustAnalyzerBuildLogTail(log)
		return fmt.Errorf("build Rust analyzer: %w (%s)%s%s", buildErr, log.Name(), tail, rustAnalyzerRegistryHint(tail))
	}
	if executable == "" {
		return errors.New("Cargo did not report a Rust analyzer executable")
	}
	if err := os.Rename(executable, binary); err != nil {
		return fmt.Errorf("install cached Rust analyzer: %w", err)
	}
	return nil
}

func rustAnalyzerCargoExecutable(output []byte, log io.Writer) (string, error) {
	var executable string
	scanner := bufio.NewScanner(bytes.NewReader(output))
	scanner.Buffer(make([]byte, 4096), 4<<20)
	for scanner.Scan() {
		var event struct {
			Reason     string
			Executable string
			Target     struct{ Name string }
			Message    struct{ Rendered string }
		}
		if json.Unmarshal(scanner.Bytes(), &event) != nil {
			continue
		}
		switch event.Reason {
		case "compiler-artifact":
			if event.Target.Name == rustAnalyzerExecutable && event.Executable != "" {
				executable = event.Executable
			}
		case "compiler-message":
			if _, err := io.WriteString(log, event.Message.Rendered); err != nil {
				return "", fmt.Errorf("write Rust analyzer build log: %w", err)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("read Cargo build messages: %w", err)
	}
	return executable, nil
}

func rustAnalyzerRegistryHint(tail string) string {
	for _, fragment := range []string{"failed to download", "failed to fetch", "download of config.json failed", "failed to query replaced source registry", "offline mode"} {
		if strings.Contains(tail, fragment) {
			return "\nBuild once online or configure a vendored Cargo registry."
		}
	}
	return ""
}

func pruneRustAnalyzerCache(directory string) error {
	for _, name := range []string{"target", "build.log"} {
		if err := os.RemoveAll(filepath.Join(directory, name)); err != nil {
			return fmt.Errorf("clean Rust analyzer build output: %w", err)
		}
	}
	root := filepath.Dir(directory)
	entries, err := os.ReadDir(root)
	if err != nil {
		return fmt.Errorf("list Rust analyzer cache: %w", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() || name == filepath.Base(directory) || len(name) != 2*sha256.Size {
			continue
		}
		if _, err := hex.DecodeString(name); err != nil {
			continue
		}
		if err := os.RemoveAll(filepath.Join(root, name)); err != nil {
			return fmt.Errorf("prune stale Rust analyzer cache: %w", err)
		}
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
