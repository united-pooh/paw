//go:build windows

package config

import (
	"errors"
	"syscall"
	"unsafe"
)

const (
	credTypeGeneric         = 1
	credPersistLocalMachine = 2
	errorNotFound           = syscall.Errno(1168)
)

type nativeCredential struct {
	Flags              uint32
	Type               uint32
	TargetName         *uint16
	Comment            *uint16
	LastWrittenLow     uint32
	LastWrittenHigh    uint32
	CredentialBlobSize uint32
	CredentialBlob     *byte
	Persist            uint32
	AttributeCount     uint32
	Attributes         uintptr
	TargetAlias        *uint16
	UserName           *uint16
}

var (
	advapi32        = syscall.NewLazyDLL("advapi32.dll")
	procCredReadW   = advapi32.NewProc("CredReadW")
	procCredWriteW  = advapi32.NewProc("CredWriteW")
	procCredDeleteW = advapi32.NewProc("CredDeleteW")
	procCredFree    = advapi32.NewProc("CredFree")
)

func credentialTarget(id string) string { return "Paw/" + id }

func platformCredentialGet(id string) (string, error) {
	if id == "" {
		return "", ErrCredentialNotFound
	}
	target, err := syscall.UTF16PtrFromString(credentialTarget(id))
	if err != nil {
		return "", err
	}
	var ptr *nativeCredential
	r1, _, callErr := procCredReadW.Call(uintptr(unsafe.Pointer(target)), credTypeGeneric, 0, uintptr(unsafe.Pointer(&ptr)))
	if r1 == 0 {
		if errors.Is(callErr, errorNotFound) {
			return "", ErrCredentialNotFound
		}
		return "", callErr
	}
	defer procCredFree.Call(uintptr(unsafe.Pointer(ptr)))
	if ptr == nil || ptr.CredentialBlob == nil || ptr.CredentialBlobSize == 0 {
		return "", ErrCredentialNotFound
	}
	data := unsafe.Slice(ptr.CredentialBlob, int(ptr.CredentialBlobSize))
	return string(append([]byte(nil), data...)), nil
}

func platformCredentialSet(id, secret string) error {
	if id == "" || secret == "" {
		return ErrCredentialNotFound
	}
	target, err := syscall.UTF16PtrFromString(credentialTarget(id))
	if err != nil {
		return err
	}
	user, err := syscall.UTF16PtrFromString("Paw")
	if err != nil {
		return err
	}
	data := []byte(secret)
	credential := nativeCredential{Type: credTypeGeneric, TargetName: target, CredentialBlobSize: uint32(len(data)), Persist: credPersistLocalMachine, UserName: user}
	credential.CredentialBlob = &data[0]
	r1, _, callErr := procCredWriteW.Call(uintptr(unsafe.Pointer(&credential)), 0)
	if r1 == 0 {
		return callErr
	}
	return nil
}

func platformCredentialDelete(id string) error {
	if id == "" {
		return ErrCredentialNotFound
	}
	target, err := syscall.UTF16PtrFromString(credentialTarget(id))
	if err != nil {
		return err
	}
	r1, _, callErr := procCredDeleteW.Call(uintptr(unsafe.Pointer(target)), credTypeGeneric, 0)
	if r1 == 0 {
		if errors.Is(callErr, errorNotFound) {
			return ErrCredentialNotFound
		}
		return callErr
	}
	return nil
}
