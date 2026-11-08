//go:build darwin && cgo

package theme

/*
#cgo LDFLAGS: -framework CoreFoundation
#include <CoreFoundation/CoreFoundation.h>

static int paw_read_system_appearance(void) {
	if (!CFPreferencesSynchronize(kCFPreferencesAnyApplication, kCFPreferencesCurrentUser, kCFPreferencesAnyHost)) {
		return -2;
	}
	CFPropertyListRef value = CFPreferencesCopyValue(
		CFSTR("AppleInterfaceStyle"), kCFPreferencesAnyApplication,
		kCFPreferencesCurrentUser, kCFPreferencesAnyHost);
	if (value == NULL) {
		return 0;
	}
	int mode = -1;
	if (CFGetTypeID(value) == CFStringGetTypeID() &&
		CFStringCompare((CFStringRef)value, CFSTR("Dark"), 0) == kCFCompareEqualTo) {
		mode = 1;
	}
	CFRelease(value);
	return mode;
}
*/
import "C"

import (
	"context"
	"errors"
)

func DetectSystemMode(ctx context.Context) (ThemeMode, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	mode := C.paw_read_system_appearance()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	switch mode {
	case 0:
		return ModeLight, nil
	case 1:
		return ModeDark, nil
	case -2:
		return "", errors.New("system appearance preferences could not be refreshed")
	default:
		return "", errors.New("system appearance preference has an unsupported value")
	}
}
