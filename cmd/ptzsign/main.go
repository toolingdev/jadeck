// Command ptzsign creates the release signing key and signs SHA256SUMS.
//
//	ptzsign keygen release.key      writes the private key, prints the public key
//	ptzsign sign release.key SHA256SUMS > SHA256SUMS.sig
//	ptzsign verify <public key> SHA256SUMS SHA256SUMS.sig
//	ptzsign pub -                   reads the private key on stdin, prints the public key
//
// The private key is the only thing that can publish an update the
// controllers will accept. Keep it in your password manager and as the
// PTZ_SIGNING_KEY GitHub Actions secret, nowhere else.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"strings"
)

func die(f string, a ...any) {
	fmt.Fprintf(os.Stderr, "ptzsign: "+f+"\n", a...)
	os.Exit(1)
}

func readKey(arg string) ed25519.PrivateKey {
	s := arg
	if b, err := os.ReadFile(arg); err == nil {
		s = string(b)
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil || len(raw) != ed25519.SeedSize {
		die("private key must be a %d-byte base64 seed (file or literal)", ed25519.SeedSize)
	}
	return ed25519.NewKeyFromSeed(raw)
}

func main() {
	if len(os.Args) < 2 {
		die("usage: keygen <file> | sign <key> <file> | verify <pubkey> <file> <sig>")
	}
	switch os.Args[1] {
	case "keygen":
		if len(os.Args) != 3 {
			die("usage: keygen <file>")
		}
		if _, err := os.Stat(os.Args[2]); err == nil {
			die("%s exists; refusing to overwrite a signing key", os.Args[2])
		}
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			die("%v", err)
		}
		if err := os.WriteFile(os.Args[2], []byte(base64.StdEncoding.EncodeToString(priv.Seed())+"\n"), 0o600); err != nil {
			die("%v", err)
		}
		fmt.Println(base64.StdEncoding.EncodeToString(pub))
	case "pub":
		if len(os.Args) != 3 {
			die("usage: pub <key> (\"-\" reads it from stdin)")
		}
		arg := os.Args[2]
		if arg == "-" {
			b, err := io.ReadAll(os.Stdin)
			if err != nil {
				die("%v", err)
			}
			arg = string(b)
		}
		fmt.Println(base64.StdEncoding.EncodeToString(readKey(arg).Public().(ed25519.PublicKey)))
	case "sign":
		if len(os.Args) != 4 {
			die("usage: sign <key> <file>")
		}
		data, err := os.ReadFile(os.Args[3])
		if err != nil {
			die("%v", err)
		}
		fmt.Println(base64.StdEncoding.EncodeToString(ed25519.Sign(readKey(os.Args[2]), data)))
	case "verify":
		if len(os.Args) != 5 {
			die("usage: verify <pubkey> <file> <sig>")
		}
		pub, err := base64.StdEncoding.DecodeString(strings.TrimSpace(os.Args[2]))
		if err != nil || len(pub) != ed25519.PublicKeySize {
			die("bad public key")
		}
		data, _ := os.ReadFile(os.Args[3])
		sigb, _ := os.ReadFile(os.Args[4])
		sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sigb)))
		if err != nil || !ed25519.Verify(pub, data, sig) {
			die("signature does NOT verify")
		}
		fmt.Println("signature OK")
	default:
		die("unknown command %q", os.Args[1])
	}
}
