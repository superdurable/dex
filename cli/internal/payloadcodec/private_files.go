// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package payloadcodec

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
)

const defaultGeneratedKeyID = "k1"

// GenerateKeyFile writes a payload key file with one random 32-byte key, readable only by its owner.
// It refuses to replace an existing file unless shouldOverwrite is true.
func GenerateKeyFile(path string, keyID string, shouldOverwrite bool) error {
	if keyID == "" {
		keyID = defaultGeneratedKeyID
	}
	if err := validateKeyID(keyID); err != nil {
		return err
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return fmt.Errorf("generate key: %w", err)
	}
	content, err := json.MarshalIndent(map[string]any{
		"current_key_id": keyID,
		"keys":           map[string]string{keyID: base64.StdEncoding.EncodeToString(key)},
	}, "", "  ")
	if err != nil {
		return err
	}
	return writePrivateFile(path, append(content, '\n'), shouldOverwrite)
}

// writePrivateFile writes data with mode 0600.
// Overwrites rename a temporary file, because truncating keeps the old, possibly wider, mode.
func writePrivateFile(path string, data []byte, shouldOverwrite bool) error {
	if !shouldOverwrite {
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("%s already exists; pass -force to replace it", path)
		}
		if err != nil {
			return err
		}
		_, writeErr := file.Write(data)
		return errors.Join(writeErr, file.Close())
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	_, writeErr := temporary.Write(data)
	chmodErr := temporary.Chmod(0o600)
	closeErr := temporary.Close()
	if err := errors.Join(writeErr, chmodErr, closeErr); err != nil {
		_ = os.Remove(temporaryPath)
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		_ = os.Remove(temporaryPath)
		return err
	}
	return nil
}

// DevCodecServerDiscovery tells dexcli Flow commands how to reach the codec server of a running dexcli dev.
type DevCodecServerDiscovery struct {
	Endpoint string `json:"endpoint"`
	Token    string `json:"token"`
}

// DevCodecServerDiscoveryPath returns the discovery file of the dexcli dev that serves dexPort.
func DevCodecServerDiscoveryPath(stateDirectory string, dexPort int) string {
	return filepath.Join(stateDirectory, "dev-codec-server-"+strconv.Itoa(dexPort)+".json")
}

// WriteDevCodecServerDiscovery writes the discovery file with mode 0600, replacing a stale one.
// It returns the written content, which RemoveDevCodecServerDiscovery compares before deleting.
func WriteDevCodecServerDiscovery(path string, discovery DevCodecServerDiscovery) ([]byte, error) {
	if err := checkLoopbackEndpoint(discovery.Endpoint); err != nil {
		return nil, err
	}
	content, err := json.Marshal(discovery)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if err := checkPrivateDirectory(filepath.Dir(path)); err != nil {
		return nil, err
	}
	return content, writePrivateFile(path, content, true)
}

// ReadDevCodecServerDiscovery returns the discovery file at path, or found false when it does not exist.
// It returns an error when the file or its directory could have been written by another user, or when the
// endpoint is not a loopback http URL.
func ReadDevCodecServerDiscovery(path string) (DevCodecServerDiscovery, bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return DevCodecServerDiscovery{}, false, nil
	}
	if err != nil {
		return DevCodecServerDiscovery{}, false, err
	}
	if err := checkPrivateDirectory(filepath.Dir(path)); err != nil {
		return DevCodecServerDiscovery{}, false, err
	}
	if err := checkOwnedEntry(path, info, false); err != nil {
		return DevCodecServerDiscovery{}, false, err
	}
	// The file holds the token, so any group or other permission means another user may have read it.
	if info.Mode().Perm()&0o077 != 0 {
		return DevCodecServerDiscovery{}, false, fmt.Errorf("%s is accessible by other users; refusing to use it for the local codec server", path)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return DevCodecServerDiscovery{}, false, err
	}
	var discovery DevCodecServerDiscovery
	if err := json.Unmarshal(content, &discovery); err != nil || discovery.Endpoint == "" || discovery.Token == "" {
		return DevCodecServerDiscovery{}, false, fmt.Errorf("dexcli dev codec server file %s is invalid", path)
	}
	if err := checkLoopbackEndpoint(discovery.Endpoint); err != nil {
		return DevCodecServerDiscovery{}, false, fmt.Errorf("dexcli dev codec server file %s: %w", path, err)
	}
	return discovery, true, nil
}

// RemoveDevCodecServerDiscovery deletes the file at path only while it still holds writtenContent.
func RemoveDevCodecServerDiscovery(path string, writtenContent []byte) error {
	content, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) || (err == nil && !bytes.Equal(content, writtenContent)) {
		return nil
	}
	if err != nil {
		return err
	}
	return os.Remove(path)
}

func checkLoopbackEndpoint(endpoint string) error {
	parsedEndpoint, err := url.Parse(endpoint)
	if err != nil || parsedEndpoint.Scheme != "http" || !isLoopbackHostName(parsedEndpoint.Hostname()) {
		return errors.New("local codec server endpoint must be an http URL on 127.0.0.1, ::1, or localhost")
	}
	return nil
}

// checkPrivateDirectory rejects a directory that is a symlink, owned by another user, or writable by others.
func checkPrivateDirectory(directory string) error {
	info, err := os.Lstat(directory)
	if err != nil {
		return err
	}
	return checkOwnedEntry(directory, info, true)
}

func checkOwnedEntry(path string, info os.FileInfo, isDirectory bool) error {
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s is a symbolic link; refusing to use it for the local codec server", path)
	}
	if isDirectory != info.IsDir() || (!isDirectory && !info.Mode().IsRegular()) {
		return fmt.Errorf("%s has an unexpected file type; refusing to use it for the local codec server", path)
	}
	status, hasStatus := info.Sys().(*syscall.Stat_t)
	if !hasStatus || int(status.Uid) != os.Getuid() {
		return fmt.Errorf("%s is not owned by the current user; refusing to use it for the local codec server", path)
	}
	if info.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("%s is writable by other users; restrict it with chmod go-w", path)
	}
	return nil
}
