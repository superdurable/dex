// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package dev

import (
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"sync"
	"syscall"
	"time"
)

type temporalProcess struct {
	command *exec.Cmd
	done    chan struct{}
	mu      sync.Mutex
	waitErr error
}

func startTemporalProcess(cfg *Config) (*temporalProcess, error) {
	temporalPath, err := exec.LookPath("temporal")
	if err != nil {
		return nil, fmt.Errorf("Temporal CLI was not found; reinstall dexcli with Homebrew: %w", err)
	}
	arguments := []string{
		"server", "start-dev",
		"--ip", cfg.BindAddress,
		"--port", strconv.Itoa(cfg.TemporalPort),
		"--ui-ip", cfg.BindAddress,
		"--ui-port", strconv.Itoa(cfg.TemporalUIPort),
	}
	if cfg.TemporalNamespace != defaultTemporalNamespace {
		arguments = append(arguments, "--namespace", cfg.TemporalNamespace)
	}
	if cfg.TemporalDBFilename != "" {
		arguments = append(arguments, "--db-filename", cfg.TemporalDBFilename)
	}
	command := exec.Command(temporalPath, arguments...)
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	if err := command.Start(); err != nil {
		return nil, fmt.Errorf("start workflow backend: %w", err)
	}
	process := &temporalProcess{
		command: command,
		done:    make(chan struct{}),
	}
	go process.wait()
	return process, nil
}

func (p *temporalProcess) wait() {
	err := p.command.Wait()
	p.mu.Lock()
	p.waitErr = err
	p.mu.Unlock()
	close(p.done)
}

func (p *temporalProcess) Done() <-chan struct{} {
	return p.done
}

func (p *temporalProcess) Err() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.waitErr
}

func (p *temporalProcess) Stop(timeout time.Duration) error {
	select {
	case <-p.done:
		return nil
	default:
	}
	if err := p.command.Process.Signal(syscall.SIGTERM); err != nil {
		select {
		case <-p.done:
			return nil
		default:
			return fmt.Errorf("signal workflow backend: %w", err)
		}
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-p.done:
		return nil
	case <-timer.C:
		if err := p.command.Process.Kill(); err != nil {
			select {
			case <-p.done:
				return nil
			default:
				return fmt.Errorf("kill workflow backend: %w", err)
			}
		}
		<-p.done
		return nil
	}
}
