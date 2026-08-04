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
	"bytes"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/superdurable/dex/service"
)

type temporalProcess struct {
	command *exec.Cmd
	done    chan struct{}
	mu      sync.Mutex
	waitErr error
}

func startTemporalProcess(cfg *Config, stdout io.Writer, stderr io.Writer) (*temporalProcess, error) {
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
		"--search-attribute", service.SearchAttributeDexWorkflowType + "=Keyword",
		"--search-attribute", service.SearchAttributeActiveStepTypes + "=KeywordList",
	}
	if cfg.TemporalNamespace != defaultTemporalNamespace {
		arguments = append(arguments, "--namespace", cfg.TemporalNamespace)
	}
	if cfg.TemporalDBFilename != "" {
		arguments = append(arguments, "--db-filename", cfg.TemporalDBFilename)
	}
	command := exec.Command(temporalPath, arguments...)
	command.Stdout = newLinePrefixWriter(stdout, "[temporal] ")
	command.Stderr = newLinePrefixWriter(stderr, "[temporal] ")
	if err := command.Start(); err != nil {
		return nil, fmt.Errorf("start Temporal: %w", err)
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
			return fmt.Errorf("signal Temporal: %w", err)
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
				return fmt.Errorf("kill Temporal: %w", err)
			}
		}
		<-p.done
		return nil
	}
}

type linePrefixWriter struct {
	target      io.Writer
	prefix      []byte
	mu          sync.Mutex
	atLineStart bool
}

func newLinePrefixWriter(target io.Writer, prefix string) *linePrefixWriter {
	return &linePrefixWriter{
		target:      target,
		prefix:      []byte(prefix),
		atLineStart: true,
	}
}

func (w *linePrefixWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	written := 0
	for len(data) > 0 {
		if w.atLineStart {
			if _, err := w.target.Write(w.prefix); err != nil {
				return written, err
			}
			w.atLineStart = false
		}
		newline := bytes.IndexByte(data, '\n')
		chunkLength := len(data)
		if newline >= 0 {
			chunkLength = newline + 1
		}
		count, err := w.target.Write(data[:chunkLength])
		written += count
		if err != nil {
			return written, err
		}
		if count != chunkLength {
			return written, io.ErrShortWrite
		}
		w.atLineStart = data[chunkLength-1] == '\n'
		data = data[chunkLength:]
	}
	return written, nil
}
