//go:build !windows

package config

func platformCredentialGet(string) (string, error) { return "", ErrCredentialStoreUnavailable }
func platformCredentialSet(string, string) error   { return ErrCredentialStoreUnavailable }
func platformCredentialDelete(string) error        { return ErrCredentialStoreUnavailable }
