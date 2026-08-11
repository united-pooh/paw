//go:build !windows && (!darwin || !cgo)

package config

func platformCredentialGet(string) (string, error) { return "", ErrCredentialStoreUnavailable }
func platformCredentialSet(string, string) error   { return ErrCredentialStoreUnavailable }
func platformCredentialDelete(string) error        { return ErrCredentialStoreUnavailable }
