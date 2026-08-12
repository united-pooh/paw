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

	opts := parseOptions()
	ctx := context.Background()

	if opts.subagentWorker {
		if err := runSubagentWorkerMode(ctx, os.Stdin, os.Stdout, opts.allowOutsideRead); err != nil {
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
