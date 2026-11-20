// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package payloadcodec

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
)

const defaultLocalCodecServerPort = 8805

// DefaultLocalWebOrigins are the Dex Web origins of dexcli dev and dexcli web on their default port.
var DefaultLocalWebOrigins = []string{"http://127.0.0.1:8802", "http://localhost:8802"}

// LocalWebOrigins returns the browser origins of a Dex Web served on a loopback port.
func LocalWebOrigins(webPort int) []string {
	port := strconv.Itoa(webPort)
	return []string{"http://127.0.0.1:" + port, "http://localhost:" + port}
}

type stringListFlag struct{ values *[]string }

func (list stringListFlag) String() string {
	if list.values == nil {
		return ""
	}
	return strings.Join(*list.values, ",")
}

func (list stringListFlag) Set(value string) error {
	*list.values = append(*list.values, value)
	return nil
}

// ExecuteCodecServerCommand runs dexcli codec-server and dexcli codec-server keygen.
func ExecuteCodecServerCommand(ctx context.Context, args []string, stdout io.Writer, stderr io.Writer) error {
	if len(args) > 0 && args[0] == "keygen" {
		return executeKeygen(args[1:], stdout, stderr)
	}
	flags := flag.NewFlagSet("dexcli codec-server", flag.ContinueOnError)
	flags.SetOutput(stderr)
	keyFile := flags.String("key-file", "", "payload key file (create one with dexcli codec-server keygen)")
	port := flags.Int("port", defaultLocalCodecServerPort, "port on 127.0.0.1; 0 picks a free port")
	tokenFile := flags.String("token-file", "", "also write the per-start token to this file with mode 0600")
	var webOrigins []string
	flags.Var(stringListFlag{values: &webOrigins}, "web-origin", "Dex Web origin allowed by CORS (repeatable; default http://127.0.0.1:8802 and http://localhost:8802)")
	flags.Usage = func() { printCodecServerUsage(stderr, flags) }
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("dexcli codec-server: unexpected arguments: %v", flags.Args())
	}
	if *keyFile == "" {
		return errors.New("dexcli codec-server: -key-file is required; create one with dexcli codec-server keygen -o key.json")
	}
	if *port < 0 || *port > 65535 {
		return errors.New("dexcli codec-server: -port must be between 0 and 65535")
	}
	if len(webOrigins) == 0 {
		webOrigins = DefaultLocalWebOrigins
	}
	codec, err := NewLocalCodecFromKeyFile(*keyFile)
	if err != nil {
		return fmt.Errorf("dexcli codec-server: %w", err)
	}
	token, err := NewLocalCodecServerToken()
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", net.JoinHostPort(LocalCodecServerHost, strconv.Itoa(*port)))
	if err != nil {
		return fmt.Errorf("dexcli codec-server: listen: %w", err)
	}
	server, err := NewLocalCodecServer(codec, token, webOrigins, listener)
	if err != nil {
		return errors.Join(err, listener.Close())
	}
	if *tokenFile != "" {
		if err := writePrivateFile(*tokenFile, []byte(token+"\n"), true); err != nil {
			return errors.Join(fmt.Errorf("dexcli codec-server: write token file: %w", err), listener.Close())
		}
	}
	endpoint := "http://" + listener.Addr().String()
	fmt.Fprintln(stdout, "Dex local codec server: LOCAL DEVELOPMENT ONLY.")
	fmt.Fprintln(stdout, "It decrypts every value encrypted with this key file for anyone who has the token.")
	fmt.Fprintln(stdout)
	fmt.Fprintf(stdout, "Endpoint:     %s\n", endpoint)
	fmt.Fprintf(stdout, "Token:        %s\n", token)
	if *tokenFile != "" {
		fmt.Fprintf(stdout, "Token file:   %s\n", *tokenFile)
	}
	fmt.Fprintf(stdout, "Web origins:  %s\n", strings.Join(webOrigins, ", "))
	fmt.Fprintln(stdout)
	fmt.Fprintln(stdout, "Clients send the token as \"Authorization: Bearer <token>\". A new token is generated at each start.")
	fmt.Fprintln(stdout, "Press Ctrl+C to stop.")
	serveErrors := make(chan error, 1)
	go func() { serveErrors <- server.Serve(listener) }()
	select {
	case <-ctx.Done():
		return server.Close()
	case err := <-serveErrors:
		return err
	}
}

func executeKeygen(args []string, stdout io.Writer, stderr io.Writer) error {
	flags := flag.NewFlagSet("dexcli codec-server keygen", flag.ContinueOnError)
	flags.SetOutput(stderr)
	output := flags.String("o", "", "key file to write")
	keyID := flags.String("key-id", defaultGeneratedKeyID, "key ID; visible in clear in every envelope")
	shouldOverwrite := flags.Bool("force", false, "replace an existing file")
	flags.Usage = func() {
		fmt.Fprintln(stderr, "Usage: dexcli codec-server keygen -o key.json [-key-id k1] [-force]")
		fmt.Fprintln(stderr)
		fmt.Fprintln(stderr, "Writes a payload key file with one random 32-byte AES-256 key and mode 0600.")
		fmt.Fprintln(stderr, "Give the same file to your Workers and Clients, and to dexcli dev -codec-key-file.")
		fmt.Fprintln(stderr)
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("dexcli codec-server keygen: unexpected arguments: %v", flags.Args())
	}
	if *output == "" {
		return errors.New("dexcli codec-server keygen: -o is required")
	}
	if err := GenerateKeyFile(*output, *keyID, *shouldOverwrite); err != nil {
		return fmt.Errorf("dexcli codec-server keygen: %w", err)
	}
	fmt.Fprintf(stdout, "Wrote payload key file %s (key id %s, mode 0600). Keep it secret and back it up; values encrypted with it cannot be read without it.\n", *output, *keyID)
	return nil
}

func printCodecServerUsage(output io.Writer, flags *flag.FlagSet) {
	fmt.Fprintln(output, "Usage: dexcli codec-server -key-file key.json [-port 8805] [-web-origin URL]... [-token-file PATH]")
	fmt.Fprintln(output, "       dexcli codec-server keygen -o key.json [-key-id k1] [-force]")
	fmt.Fprintln(output)
	fmt.Fprintln(output, "Serves the Dex codec server protocol (POST /encode, POST /decode) on 127.0.0.1 for local development,")
	fmt.Fprintln(output, "so Dex Web and dexcli can show and write encrypted payloads. It accepts only localhost Host headers,")
	fmt.Fprintln(output, "requires the bearer token it prints at each start, and allows CORS only from the given Dex Web origins.")
	fmt.Fprintln(output, "Do not expose it; deploy a codec server with authentication for shared environments.")
	fmt.Fprintln(output)
	flags.PrintDefaults()
}
