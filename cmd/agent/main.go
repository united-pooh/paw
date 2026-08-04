package main

import (
	"context"
	"log"
	"os"
)

func main() {
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
