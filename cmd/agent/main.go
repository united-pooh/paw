package main

import (
	"context"
	"log"
	"os"

	"paw/internal/model"
)

func main() {
	// Load .env/.env.local before any mode so provider auth configured via
	// auth.env resolves from the project environment instead of prompting
	// for the macOS keychain on every startup.
	if _, err := model.LoadOptionalEnvFiles(); err != nil {
		log.Printf("warning: load .env/.env.local: %v", err)
	}

	if len(os.Args) > 1 && os.Args[1] == "serve" {
		serveOpts, err := parseServeOptions(os.Args[2:])
		if err != nil {
			log.Fatal(err)
		}
		if err := runServeMode(serveOpts); err != nil {
			log.Fatal(err)
		}
		return
	}

	opts := parseOptions()
	ctx := context.Background()
	sandboxLimits := parseSandboxLimits(opts.sandboxLimits)

	if opts.taskWorkerPool {
		if err := runTaskPoolWorkerMode(ctx, os.Stdin, os.Stdout, opts.allowOutsideRead, sandboxLimits); err != nil {
			log.Fatal(err)
		}
		return
	}

	if opts.taskWorker {
		if err := runTaskWorkerMode(ctx, os.Stdin, os.Stdout, opts.allowOutsideRead, sandboxLimits); err != nil {
			log.Fatal(err)
		}
		return
	}

	if opts.prompt != "" {
		if err := runSingleTurnMode(ctx, opts); err != nil {
			log.Fatal(err)
		}
		return
	}

	if err := runInteractiveMode(ctx, opts); err != nil {
		log.Fatal(err)
	}
}
