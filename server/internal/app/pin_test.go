package app

import (
	"strings"
	"testing"
)

const (
	devicePINTestPepper = "test-secret-not-a-real-pepper-000000000000000"
	// Generated independently with history-keep/backend/tidb/pin-hash.ts and
	// argon2id@1.0.1 under Node, fixing crypto.getRandomValues to bytes 0..15.
	// The original createPinHasher.verify also accepted both golden hashes.
	devicePINGolden        = devicePINHashPrefix + "AAECAwQFBgcICQoLDA0ODw$rwh/IydOey0jIHKBQQ7prQQ6s60plk91BZ5JPlxVUm4"
	devicePINUnicodeGolden = devicePINHashPrefix + "AAECAwQFBgcICQoLDA0ODw$QOnVxzkIH3YOh1v2td1cGbStwNlSX1b7P69WnLjaCkM"
)

func TestVerifyDevicePINGolden(t *testing.T) {
	for _, tc := range []struct {
		name, pin, encoded, pepper string
		want                       bool
	}{
		{"leading zero PIN", "0042", devicePINGolden, devicePINTestPepper, true},
		{"wrong PIN", "0043", devicePINGolden, devicePINTestPepper, false},
		{"leading zeros removed", "42", devicePINGolden, devicePINTestPepper, false},
		{"empty PIN", "", devicePINGolden, devicePINTestPepper, false},
		{"long PIN", "00042", devicePINGolden, devicePINTestPepper, false},
		{"PIN whitespace", "0042\n", devicePINGolden, devicePINTestPepper, false},
		{"PIN non digits", "00a2", devicePINGolden, devicePINTestPepper, false},
		{"PIN Unicode digits", "００４２", devicePINGolden, devicePINTestPepper, false},
		{"wrong pepper", "0042", devicePINGolden, "different-test-secret-000000000000000000000", false},
		{"empty pepper", "0042", devicePINGolden, "", false},
		{"short pepper", "0042", devicePINGolden, strings.Repeat("x", 31), false},
		{"UTF8 byte length is not JS length", "0042", devicePINGolden, strings.Repeat("é", 16), false},
		{"UTF16 pepper length and raw UTF8 HMAC", "0042", devicePINUnicodeGolden, strings.Repeat("🔑", 16), true},
		{"short UTF16 pepper", "0042", devicePINUnicodeGolden, strings.Repeat("🔑", 15), false},
		{"invalid UTF8 pepper", "0042", devicePINGolden, devicePINTestPepper + "\xff", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := VerifyDevicePIN(tc.pin, tc.encoded, tc.pepper); got != tc.want {
				t.Fatalf("VerifyDevicePIN() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestVerifyDevicePINRejectsMalformedEncodings(t *testing.T) {
	salt := "AAECAwQFBgcICQoLDA0ODw"
	digest := "rwh/IydOey0jIHKBQQ7prQQ6s60plk91BZ5JPlxVUm4"
	for name, encoded := range map[string]string{
		"empty":                    "",
		"oversized":                strings.Repeat("x", 1<<20),
		"different transformation": strings.Replace(devicePINGolden, "hmac-sha256-v1:", "hmac-sha256-v2:", 1),
		"missing transformation":   strings.TrimPrefix(devicePINGolden, "hmac-sha256-v1:"),
		"different algorithm":      strings.Replace(devicePINGolden, "argon2id", "argon2i", 1),
		"different version":        strings.Replace(devicePINGolden, "v=19", "v=16", 1),
		"lower memory":             strings.Replace(devicePINGolden, "m=19456", "m=1", 1),
		"higher memory":            strings.Replace(devicePINGolden, "m=19456", "m=9999999999999", 1),
		"different passes":         strings.Replace(devicePINGolden, "t=2", "t=9", 1),
		"different parallelism":    strings.Replace(devicePINGolden, "p=1", "p=2", 1),
		"reordered costs":          strings.Replace(devicePINGolden, "m=19456,t=2,p=1", "t=2,m=19456,p=1", 1),
		"short salt":               devicePINHashPrefix + salt[:21] + "$" + digest,
		"padded salt":              devicePINHashPrefix + salt + "==$" + digest,
		"invalid salt alphabet":    devicePINHashPrefix + "_" + salt[1:] + "$" + digest,
		"noncanonical salt bits":   devicePINHashPrefix + salt[:21] + "x$" + digest,
		"short hash":               devicePINHashPrefix + salt + "$" + digest[:42],
		"padded hash":              devicePINHashPrefix + salt + "$" + digest + "=",
		"invalid hash alphabet":    devicePINHashPrefix + salt + "$_" + digest[1:],
		"noncanonical hash bits":   devicePINHashPrefix + salt + "$" + digest[:42] + "5",
		"extra field":              devicePINGolden + "$ignored",
		"wrong separator":          devicePINHashPrefix + salt + ":" + digest,
		"newline":                  devicePINGolden + "\n",
	} {
		t.Run(name, func(t *testing.T) {
			if VerifyDevicePIN("0042", encoded, devicePINTestPepper) {
				t.Fatal("malformed encoding was accepted")
			}
		})
	}
}
