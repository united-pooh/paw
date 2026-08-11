//go:build darwin && cgo

package config

/*
#cgo LDFLAGS: -framework CoreFoundation -framework Security

#include <CoreFoundation/CoreFoundation.h>
#include <Security/Security.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

static CFMutableDictionaryRef paw_credential_query(const void *account_bytes, size_t account_len) {
	if (account_bytes == NULL || account_len == 0) {
		return NULL;
	}
	CFStringRef account = CFStringCreateWithBytes(
		kCFAllocatorDefault,
		(const UInt8 *)account_bytes,
		(CFIndex)account_len,
		kCFStringEncodingUTF8,
		false
	);
	if (account == NULL) {
		return NULL;
	}
	CFMutableDictionaryRef query = CFDictionaryCreateMutable(
		kCFAllocatorDefault,
		0,
		&kCFTypeDictionaryKeyCallBacks,
		&kCFTypeDictionaryValueCallBacks
	);
	if (query == NULL) {
		CFRelease(account);
		return NULL;
	}
	CFDictionarySetValue(query, kSecClass, kSecClassGenericPassword);
	CFDictionarySetValue(query, kSecAttrService, CFSTR("Paw"));
	CFDictionarySetValue(query, kSecAttrAccount, account);
	CFRelease(account);
	return query;
}

static int32_t paw_credential_copy(
	const void *account_bytes,
	size_t account_len,
	void **secret_bytes,
	size_t *secret_len
) {
	if (secret_bytes == NULL || secret_len == NULL) {
		return errSecParam;
	}
	*secret_bytes = NULL;
	*secret_len = 0;

	CFMutableDictionaryRef query = paw_credential_query(account_bytes, account_len);
	if (query == NULL) {
		return errSecAllocate;
	}
	CFDictionarySetValue(query, kSecReturnData, kCFBooleanTrue);
	CFDictionarySetValue(query, kSecMatchLimit, kSecMatchLimitOne);

	CFTypeRef result = NULL;
	OSStatus status = SecItemCopyMatching(query, &result);
	CFRelease(query);
	if (status != errSecSuccess) {
		if (result != NULL) {
			CFRelease(result);
		}
		return status;
	}
	if (result == NULL || CFGetTypeID(result) != CFDataGetTypeID()) {
		if (result != NULL) {
			CFRelease(result);
		}
		return errSecDecode;
	}

	CFDataRef data = (CFDataRef)result;
	CFIndex length = CFDataGetLength(data);
	if (length <= 0) {
		CFRelease(result);
		return errSecItemNotFound;
	}
	void *copy = malloc((size_t)length);
	if (copy == NULL) {
		CFRelease(result);
		return errSecAllocate;
	}
	memcpy(copy, CFDataGetBytePtr(data), (size_t)length);
	CFRelease(result);
	*secret_bytes = copy;
	*secret_len = (size_t)length;
	return errSecSuccess;
}

static int32_t paw_credential_set(
	const void *account_bytes,
	size_t account_len,
	const void *secret_bytes,
	size_t secret_len
) {
	if (secret_bytes == NULL || secret_len == 0) {
		return errSecParam;
	}
	CFMutableDictionaryRef query = paw_credential_query(account_bytes, account_len);
	if (query == NULL) {
		return errSecAllocate;
	}
	CFDataRef secret = CFDataCreate(
		kCFAllocatorDefault,
		(const UInt8 *)secret_bytes,
		(CFIndex)secret_len
	);
	if (secret == NULL) {
		CFRelease(query);
		return errSecAllocate;
	}
	CFMutableDictionaryRef update = CFDictionaryCreateMutable(
		kCFAllocatorDefault,
		0,
		&kCFTypeDictionaryKeyCallBacks,
		&kCFTypeDictionaryValueCallBacks
	);
	if (update == NULL) {
		CFRelease(secret);
		CFRelease(query);
		return errSecAllocate;
	}
	CFDictionarySetValue(update, kSecValueData, secret);

	OSStatus status = SecItemUpdate(query, update);
	CFRelease(update);
	if (status == errSecItemNotFound) {
		CFDictionarySetValue(query, kSecValueData, secret);
		status = SecItemAdd(query, NULL);
	}
	CFRelease(secret);
	CFRelease(query);
	return status;
}

static int32_t paw_credential_delete(const void *account_bytes, size_t account_len) {
	CFMutableDictionaryRef query = paw_credential_query(account_bytes, account_len);
	if (query == NULL) {
		return errSecAllocate;
	}
	OSStatus status = SecItemDelete(query);
	CFRelease(query);
	return status;
}

static char *paw_security_error_message(int32_t raw_status) {
	CFStringRef message = SecCopyErrorMessageString((OSStatus)raw_status, NULL);
	if (message == NULL) {
		return NULL;
	}
	CFIndex maximum = CFStringGetMaximumSizeForEncoding(
		CFStringGetLength(message),
		kCFStringEncodingUTF8
	);
	if (maximum < 0) {
		CFRelease(message);
		return NULL;
	}
	char *buffer = (char *)malloc((size_t)maximum + 1);
	if (buffer == NULL) {
		CFRelease(message);
		return NULL;
	}
	Boolean converted = CFStringGetCString(
		message,
		buffer,
		maximum + 1,
		kCFStringEncodingUTF8
	);
	CFRelease(message);
	if (!converted) {
		free(buffer);
		return NULL;
	}
	return buffer;
}
*/
import "C"

import (
	"fmt"
	"strings"
	"unsafe"
)

const (
	darwinErrSecSuccess      int32 = 0
	darwinErrSecNotAvailable int32 = -25291
	darwinErrSecAuthFailed   int32 = -25293
	darwinErrSecItemNotFound int32 = -25300
)

func platformCredentialGet(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", ErrCredentialNotFound
	}
	account := C.CBytes([]byte(id))
	if account == nil {
		return "", fmt.Errorf("allocate macOS Keychain credential ID")
	}
	defer C.free(account)

	var secret unsafe.Pointer
	var secretLength C.size_t
	status := int32(C.paw_credential_copy(account, C.size_t(len(id)), &secret, &secretLength))
	if err := darwinCredentialError("read", status); err != nil {
		return "", err
	}
	if secret == nil || secretLength == 0 {
		if secret != nil {
			C.free(secret)
		}
		return "", ErrCredentialNotFound
	}
	defer C.free(secret)
	return string(C.GoBytes(secret, C.int(secretLength))), nil
}

func platformCredentialSet(id, secret string) error {
	id = strings.TrimSpace(id)
	if id == "" || secret == "" {
		return ErrCredentialNotFound
	}
	account := C.CBytes([]byte(id))
	if account == nil {
		return fmt.Errorf("allocate macOS Keychain credential ID")
	}
	defer C.free(account)
	secretBytes := C.CBytes([]byte(secret))
	if secretBytes == nil {
		return fmt.Errorf("allocate macOS Keychain credential data")
	}
	defer C.free(secretBytes)

	status := int32(C.paw_credential_set(
		account,
		C.size_t(len(id)),
		secretBytes,
		C.size_t(len(secret)),
	))
	return darwinCredentialError("write", status)
}

func platformCredentialDelete(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return ErrCredentialNotFound
	}
	account := C.CBytes([]byte(id))
	if account == nil {
		return fmt.Errorf("allocate macOS Keychain credential ID")
	}
	defer C.free(account)

	status := int32(C.paw_credential_delete(account, C.size_t(len(id))))
	return darwinCredentialError("delete", status)
}

func darwinCredentialError(operation string, status int32) error {
	switch status {
	case darwinErrSecSuccess:
		return nil
	case darwinErrSecItemNotFound:
		return ErrCredentialNotFound
	case darwinErrSecNotAvailable:
		return ErrCredentialStoreUnavailable
	}

	message := ""
	if raw := C.paw_security_error_message(C.int32_t(status)); raw != nil {
		message = C.GoString(raw)
		C.free(unsafe.Pointer(raw))
	}
	if message == "" {
		return fmt.Errorf("%s credential in macOS Keychain failed (OSStatus %d)", operation, status)
	}
	return fmt.Errorf("%s credential in macOS Keychain: %s (OSStatus %d)", operation, message, status)
}
