package app

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"paw/internal/session"
)

const (
	CommandKindCreateSession = "session.create"
	CommandKindForkSession   = "session.fork"
	CommandStatusAccepted    = "accepted"
)

type CommandReceipt = session.CommandReceipt

func deterministicCommandResourceID(kind, commandID, requested string) (string, error) {
	kind = strings.TrimSpace(kind)
	commandID = strings.TrimSpace(commandID)
	requested = strings.TrimSpace(requested)
	if commandID == "" {
		return "", fmt.Errorf("command ID is required")
	}
	if requested != "" {
		return requested, nil
	}
	sum := sha256.Sum256([]byte(kind + "\x00" + commandID))
	return hex.EncodeToString(sum[:16]), nil
}

func mutationResultFromReceipt(receipt CommandReceipt) SessionMutationResult {
	return SessionMutationResult{SessionID: receipt.ResourceID, SessionVersion: receipt.SessionVersion}
}
