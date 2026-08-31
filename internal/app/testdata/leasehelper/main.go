package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"paw/internal/app"
)

type result struct {
	Code            string             `json:"code"`
	PID             int                `json:"pid,omitempty"`
	Mode            app.ControllerMode `json:"mode,omitempty"`
	InstanceID      string             `json:"instance_id,omitempty"`
	OwnerPID        int                `json:"owner_pid,omitempty"`
	OwnerMode       app.ControllerMode `json:"owner_mode,omitempty"`
	OwnerInstanceID string             `json:"owner_instance_id,omitempty"`
	Message         string             `json:"message,omitempty"`
}

func main() {
	if len(os.Args) < 3 {
		write(result{Code: "error", Message: "usage: leasehelper STORE_ROOT MODE [hold]"})
		os.Exit(2)
	}

	mode := app.ControllerMode(os.Args[2])
	lease, err := app.AcquireControllerLease(os.Args[1], mode)
	if err != nil {
		var locked *app.WorkspaceLockedError
		if errors.As(err, &locked) {
			write(result{
				Code:            "workspace_locked",
				OwnerPID:        locked.OwnerPID,
				OwnerMode:       locked.OwnerMode,
				OwnerInstanceID: locked.OwnerInstanceID,
				Message:         locked.Error(),
			})
			return
		}
		write(result{Code: "error", Message: err.Error()})
		os.Exit(1)
	}
	defer func() {
		if err := lease.Close(); err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
	}()

	write(result{Code: "acquired", PID: os.Getpid(), Mode: mode, InstanceID: lease.InstanceID()})
	if len(os.Args) < 4 || os.Args[3] != "hold" {
		return
	}

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	<-signals
}

func write(value result) {
	if err := json.NewEncoder(os.Stdout).Encode(value); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
