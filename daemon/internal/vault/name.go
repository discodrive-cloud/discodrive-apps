// Package vault — name.go: file name encryption and DirIdHash (Cryptomator format 8).
//
// Cryptomator uses AES-SIV-CMAC (RFC 5297) for name encryption:
//   - sivKey = macKey || encKey (64B); macKey = S2V/CMAC, encKey = AES-CTR
//   - S2V always includes CMAC(AAD) even for an empty AAD string
//   - siv-go skips CMAC(AAD) when len(AAD)==0 — this deviates from RFC 5297,
//     so we implement SIV manually.
package vault

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha1"
	"encoding/base32"
	"encoding/base64"
	"errors"
	"fmt"
	"runtime"
	"strings"

	cmaclib "github.com/aead/cmac/aes"
)

// sivDecrypt decrypts AES-SIV-CMAC.
// RFC 5297: S2V(K1, S1, ..., Sn) → always includes all components, including empty ones.
// CT layout: tag(16) || AES-CTR(K2, IV=tag_with_zeroed_bits, plaintext).
func sivDecrypt(macKey, encKey, ciphertext, aad []byte) ([]byte, error) {
	if len(ciphertext) < 16 {
		return nil, errors.New("siv: ciphertext too short")
	}
	tag := ciphertext[:16]
	enc := ciphertext[16:]

	// IV: tag with bits 31 and 63 zeroed (RFC 5297)
	iv := make([]byte, 16)
	copy(iv, tag)
	iv[8] &= 0x7f
	iv[12] &= 0x7f

	// Decrypt CTR
	block, err := aes.NewCipher(encKey)
	if err != nil {
		return nil, err
	}
	pt := make([]byte, len(enc))
	cipher.NewCTR(block, iv).XORKeyStream(pt, enc)

	// Verify tag via S2V
	computedTag, err := s2vCMAC(macKey, aad, pt)
	if err != nil {
		return nil, err
	}

	// Constant-time compare
	ok := subtleEqual(computedTag, tag)
	if !ok {
		return nil, errors.New("siv: message authentication failed")
	}
	return pt, nil
}

// sivEncrypt encrypts AES-SIV-CMAC.
func sivEncrypt(macKey, encKey, plaintext, aad []byte) ([]byte, error) {
	// Compute the SIV tag
	tag, err := s2vCMAC(macKey, aad, plaintext)
	if err != nil {
		return nil, err
	}

	// IV: tag with bits 31 and 63 zeroed
	iv := make([]byte, 16)
	copy(iv, tag)
	iv[8] &= 0x7f
	iv[12] &= 0x7f

	block, err := aes.NewCipher(encKey)
	if err != nil {
		return nil, err
	}
	ct := make([]byte, len(tag)+len(plaintext))
	copy(ct, tag)
	cipher.NewCTR(block, iv).XORKeyStream(ct[16:], plaintext)
	return ct, nil
}

// s2vCMAC computes S2V per RFC 5297.
// If aad != nil — includes CMAC(aad) as the first S2V component (even if len(aad)==0).
// If aad == nil — S2V is computed only over plaintext (one component).
// This matches Cryptomator behaviour:
//   - DecryptName: aad = []byte(parentDirID) → CMAC(parentDirID) always included
//   - DirIdHash: aad = nil → plaintext only (dirID)
func s2vCMAC(key, aad, plaintext []byte) ([]byte, error) {
	mac, err := cmaclib.New(key)
	if err != nil {
		return nil, err
	}

	// Step 1: D = CMAC(K, 0^128)
	zeros := make([]byte, 16)
	mac.Write(zeros)
	D := mac.Sum(nil)
	mac.Reset()

	// Step 2: Process AAD if provided (non-nil)
	if aad != nil {
		mac.Write(aad)
		aadMAC := mac.Sum(nil)
		mac.Reset()

		D = dbl16(D)
		D = xor16(D, aadMAC)
	}

	// Step 3: Final element — plaintext
	if len(plaintext) >= 16 {
		// xorend: XOR the last 16 bytes of plaintext with D
		padded := make([]byte, len(plaintext))
		copy(padded, plaintext)
		off := len(plaintext) - 16
		for i := 0; i < 16; i++ {
			padded[off+i] ^= D[i]
		}
		mac.Write(padded)
	} else {
		// pad: append 0x80… to 16 bytes, XOR with D
		padded := make([]byte, 16)
		copy(padded, plaintext)
		padded[len(plaintext)] = 0x80
		D = dbl16(D)
		padded = xor16(padded, D)
		mac.Write(padded)
	}
	tag := mac.Sum(nil)
	return tag, nil
}

// dbl16 doubles a 16-byte GF(2^128) element (polynomial x^128 + x^7 + x^2 + x + 1).
func dbl16(b []byte) []byte {
	out := make([]byte, 16)
	var carry byte
	for i := 15; i >= 0; i-- {
		newCarry := (b[i] >> 7) & 1
		out[i] = (b[i] << 1) | carry
		carry = newCarry
	}
	if carry != 0 {
		out[15] ^= 0x87
	}
	return out
}

// xor16 returns the XOR of two 16-byte slices.
func xor16(a, b []byte) []byte {
	out := make([]byte, 16)
	for i := 0; i < 16; i++ {
		out[i] = a[i] ^ b[i]
	}
	return out
}

// subtleEqual performs a constant-time comparison of two slices.
func subtleEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var v byte
	for i := range a {
		v |= a[i] ^ b[i]
	}
	return v == 0
}

// ErrInvalidName marks a plaintext name that cannot be a single path component:
// empty, "." or "..", or containing a separator or NUL. Names are authenticated, but
// anyone holding the vault password can write such an entry, so every decrypted name
// must pass this check before it is joined into a local path.
var ErrInvalidName = errors.New("vault: invalid entry name")

// CheckPlainName reports ErrInvalidName for names that would not stay a single path
// component. The backslash is a separator only on Windows; elsewhere it is a legal
// file-name character.
func CheckPlainName(name string) error {
	bad := name == "" || name == "." || name == ".." ||
		strings.ContainsAny(name, "/\x00") ||
		(runtime.GOOS == "windows" && strings.Contains(name, `\`))
	if bad {
		return fmt.Errorf("%w: %q", ErrInvalidName, name)
	}
	return nil
}

// EncryptName encrypts a file or directory name bound to parentDirID.
// Result: base64url(ciphertext) + ".c9r" — always the full name.
// If the result length exceeds 220 characters, the caller must apply .c9s shortening.
// AES-SIV-CMAC: sivKey = macKey||encKey, AAD = parentDirID.
func (v *Vault) EncryptName(name, parentDirID string) (string, error) {
	if err := CheckPlainName(name); err != nil {
		return "", err
	}
	ct, err := sivEncrypt(v.macKey, v.encKey, []byte(name), []byte(parentDirID))
	if err != nil {
		return "", fmt.Errorf("vault: SIV encrypt name %q: %w", name, err)
	}
	return base64.URLEncoding.EncodeToString(ct) + ".c9r", nil
}

// shortenedName computes the .c9s directory name for a long encrypted name.
// shortened = base64url(sha1([]byte(fullEncName))) + ".c9s"
// fullEncName — the full string of the form "<base64url>.c9r".
func shortenedName(fullEncName string) string {
	h := sha1.Sum([]byte(fullEncName))
	return base64.RawURLEncoding.EncodeToString(h[:]) + ".c9s"
}

// DecryptName decrypts an encrypted .c9r file name.
// encNameC9r — name with .c9r suffix (e.g. "GkzZc7GkvMnFWszdko4WGQ16MkoAK9uX.c9r").
// parentDirID — parent directory ID (string; empty string for root).
func (v *Vault) DecryptName(encNameC9r, parentDirID string) (string, error) {
	// Strip the .c9r suffix
	encName := strings.TrimSuffix(encNameC9r, ".c9r")
	if encName == encNameC9r {
		return "", fmt.Errorf("vault: name %q does not end with .c9r", encNameC9r)
	}

	// base64url-decode. Cryptomator may use padding or no padding.
	ct, err := decodeBase64URLFlex(encName)
	if err != nil {
		return "", fmt.Errorf("vault: base64url decode %q: %w", encName, err)
	}

	pt, err := sivDecrypt(v.macKey, v.encKey, ct, []byte(parentDirID))
	if err != nil {
		return "", fmt.Errorf("vault: SIV decrypt name %q: %w", encNameC9r, err)
	}
	if err := CheckPlainName(string(pt)); err != nil {
		return "", err
	}
	return string(pt), nil
}

// DirIdHash computes the storage path for a directory by its ID.
// Algorithm: AES-SIV(dirID, AAD=[]) → SHA1 → Base32(NoPadding) → "d/XX/YYY..."
func (v *Vault) DirIdHash(dirID string) (string, error) {
	// Deterministic encryption with no AAD (DirIdHash does not use AAD)
	ct, err := sivEncrypt(v.macKey, v.encKey, []byte(dirID), nil)
	if err != nil {
		return "", fmt.Errorf("vault: DirIdHash SIV encrypt: %w", err)
	}

	// SHA1 of the ciphertext
	h := sha1.Sum(ct)

	// Base32 RFC4648 uppercase without padding
	b32 := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(h[:])

	// Path: "d/" + first 2 chars + "/" + remaining 30 chars
	return "d/" + b32[:2] + "/" + b32[2:], nil
}

// decodeBase64URLFlex decodes a base64url string with or without padding.
func decodeBase64URLFlex(s string) ([]byte, error) {
	// Try with padding first
	b, err := base64.URLEncoding.DecodeString(s)
	if err == nil {
		return b, nil
	}
	// Try without padding (raw)
	return base64.RawURLEncoding.DecodeString(s)
}
