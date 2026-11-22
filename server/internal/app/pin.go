package app

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

const devicePINHashPrefix = "hmac-sha256-v1:$argon2id$v=19$m=19456,t=2,p=1$"

// VerifyDevicePIN verifies the versioned hashes produced by the Android app's
// registration Worker. Only that exact encoding and its fixed costs are accepted.
func VerifyDevicePIN(pin, encoded, pepper string) bool {
	if len(pin) != 4 || !utf8.ValidString(pepper) {
		return false
	}
	for i := range pin {
		if pin[i] < '0' || pin[i] > '9' {
			return false
		}
	}
	// The Worker checks JavaScript string.length (UTF-16 code units), while HMAC
	// uses the original UTF-8 bytes. Do not trim, normalize, or base64-decode it.
	pepperUnits := 0
	for _, r := range pepper {
		pepperUnits++
		if r > 0xffff {
			pepperUnits++
		}
	}
	if pepperUnits < 32 || len(encoded) != len(devicePINHashPrefix)+22+1+43 ||
		!strings.HasPrefix(encoded, devicePINHashPrefix) {
		return false
	}
	parts := encoded[len(devicePINHashPrefix):]
	if parts[22] != '$' {
		return false
	}
	salt, err := base64.RawStdEncoding.Strict().DecodeString(parts[:22])
	if err != nil || len(salt) != 16 {
		return false
	}
	expected, err := base64.RawStdEncoding.Strict().DecodeString(parts[23:])
	if err != nil || len(expected) != 32 {
		return false
	}
	mac := hmac.New(sha256.New, []byte(pepper))
	mac.Write([]byte(pin))
	password := mac.Sum(nil)
	defer clear(password)
	actual := argon2.IDKey(password, salt, 2, 19456, 1, 32)
	defer clear(actual)
	return subtle.ConstantTimeCompare(actual, expected) == 1
}
