// Package update implements signed in-app updates from GitHub Releases.
//
// A release carries, per OS/arch, a binary named ptz-<os>-<arch>[.exe],
// plus SHA256SUMS and SHA256SUMS.sig (an ed25519 signature over the
// SHA256SUMS bytes, base64). The public key is compiled into the binary
// (or set with PTZ_UPDATE_PUBKEY). Nothing is offered until the
// signature AND the binary's hash verify; unsigned updates are refused.
package update

import (
	"bufio"
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

var ErrInvalid = errors.New("update failed verification")

// ParsePublicKey decodes a base64 ed25519 public key.
func ParsePublicKey(s string) (ed25519.PublicKey, error) {
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil || len(b) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("update public key must be %d bytes of base64", ed25519.PublicKeySize)
	}
	return ed25519.PublicKey(b), nil
}

// VerifySums checks the signature over SHA256SUMS and returns the
// expected hash for asset.
func VerifySums(pub ed25519.PublicKey, sums, sig []byte, asset string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sig)))
	if err != nil {
		return "", fmt.Errorf("%w: signature is not base64", ErrInvalid)
	}
	if !ed25519.Verify(pub, sums, raw) {
		return "", fmt.Errorf("%w: SHA256SUMS signature does not match the release key", ErrInvalid)
	}
	sc := bufio.NewScanner(bytes.NewReader(sums))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == asset {
			if len(f[0]) != 64 {
				break
			}
			return strings.ToLower(f[0]), nil
		}
	}
	return "", fmt.Errorf("%w: %s is not listed in SHA256SUMS", ErrInvalid, asset)
}

// VerifyBinary checks data against the expected hex SHA-256.
func VerifyBinary(data []byte, want string) error {
	got := sha256.Sum256(data)
	if hex.EncodeToString(got[:]) != want {
		return fmt.Errorf("%w: binary hash does not match SHA256SUMS", ErrInvalid)
	}
	return nil
}

// Newer reports whether version a is newer than b (vMAJOR.MINOR.PATCH,
// optional -suffix sorts before the plain release). "dev" is oldest.
func Newer(a, b string) bool {
	pa, oka := parse(a)
	pb, okb := parse(b)
	if !oka {
		return false
	}
	if !okb {
		return true
	}
	for i := 0; i < 3; i++ {
		if pa.n[i] != pb.n[i] {
			return pa.n[i] > pb.n[i]
		}
	}
	if pa.pre == pb.pre {
		return false
	}
	if pa.pre == "" {
		return true
	}
	if pb.pre == "" {
		return false
	}
	return pa.pre > pb.pre
}

type ver struct {
	n   [3]int
	pre string
}

func parse(s string) (ver, bool) {
	var v ver
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	if i := strings.IndexAny(s, "-+"); i >= 0 {
		v.pre, s = s[i+1:], s[:i]
	}
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return v, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return v, false
		}
		v.n[i] = n
	}
	return v, true
}
