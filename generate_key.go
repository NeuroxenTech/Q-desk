// generate_keys.go
//
// Generates an ML-DSA-65 (Dilithium3) keypair for Q-DESK's PQC signing.
// Run this locally, once, on a trusted machine. Do NOT run this via the
// coding agent (OpenCode) or any AI tool — generate signing keys yourself.
//
// Usage:
//   go mod init keygen
//   go get github.com/cloudflare/circl/sign/mldsa/mldsa65
//   go run generate_keys.go
//
// Output:
//   Prints base64-encoded public and private keys to stdout, and writes
//   them to mldsa_public.key / mldsa_private.key in the current directory.
//
// After running:
//   - Put the PUBLIC key in .env as MLDSA_SERVER_PUBLIC_KEY
//   - Put the PRIVATE key in .env as MLDSA_SERVER_PRIVATE_KEY (local dev only)
//   - For anything beyond local dev, move the private key into a secrets
//     manager (e.g. Supabase Vault, AWS Secrets Manager) instead of a
//     plaintext .env file, and delete the local .key file after import.
//   - Never commit mldsa_private.key or paste its contents anywhere shared.

package main

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"

	"github.com/cloudflare/circl/sign/mldsa/mldsa65"
)

func main() {
	pub, priv, err := mldsa65.GenerateKey(rand.Reader)
	if err != nil {
		fmt.Fprintf(os.Stderr, "key generation failed: %v\n", err)
		os.Exit(1)
	}

	pubBytes, err := pub.MarshalBinary()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to marshal public key: %v\n", err)
		os.Exit(1)
	}
	privBytes, err := priv.MarshalBinary()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to marshal private key: %v\n", err)
		os.Exit(1)
	}

	pubB64 := base64.StdEncoding.EncodeToString(pubBytes)
	privB64 := base64.StdEncoding.EncodeToString(privBytes)

	if err := os.WriteFile("mldsa_public.key", []byte(pubB64), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "failed to write public key file: %v\n", err)
		os.Exit(1)
	}
	// 0600: owner read/write only — private key file should not be group/world readable
	if err := os.WriteFile("mldsa_private.key", []byte(privB64), 0600); err != nil {
		fmt.Fprintf(os.Stderr, "failed to write private key file: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("ML-DSA-65 keypair generated.")
	fmt.Println()
	fmt.Println("MLDSA_SERVER_PUBLIC_KEY=" + pubB64)
	fmt.Println()
	fmt.Println("MLDSA_SERVER_PRIVATE_KEY=" + privB64)
	fmt.Println()
	fmt.Println("Also written to: mldsa_public.key, mldsa_private.key")
	fmt.Println("Keep mldsa_private.key out of git and out of any shared channel.")
}