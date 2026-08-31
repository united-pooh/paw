package main

import (
	"flag"
	"fmt"
	"io"
	"net"
	"strings"
)

const defaultServeListen = "127.0.0.1:0"

type serveOptions struct {
	listen string
	open   bool
}

func parseServeOptions(args []string) (serveOptions, error) {
	options := serveOptions{listen: defaultServeListen}
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&options.listen, "listen", defaultServeListen, "loopback address to listen on")
	flags.BoolVar(&options.open, "open", false, "open the workbench in the default browser")
	if err := flags.Parse(args); err != nil {
		return serveOptions{}, err
	}
	if flags.NArg() != 0 {
		return serveOptions{}, fmt.Errorf("serve does not accept positional arguments: %s", strings.Join(flags.Args(), " "))
	}
	address, err := validateServeListen(options.listen)
	if err != nil {
		return serveOptions{}, err
	}
	options.listen = address
	return options, nil
}

func validateServeListen(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		value = defaultServeListen
	}
	host, port, err := net.SplitHostPort(value)
	if err != nil {
		return "", fmt.Errorf("invalid serve listen address %q: %w", value, err)
	}
	if port == "" {
		return "", fmt.Errorf("serve listen port is required")
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	if !strings.EqualFold(host, "localhost") && (ip == nil || !ip.IsLoopback()) {
		return "", fmt.Errorf("serve listen address must be loopback: %s", value)
	}
	return net.JoinHostPort(host, port), nil
}

func runServeMode(_ serveOptions) error {
	return fmt.Errorf("serve runtime is not implemented yet")
}
