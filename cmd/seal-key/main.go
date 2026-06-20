package main

import (
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"os"

	"github.com/lenaxia/llmsafespaces/pkg/secrets"
)

func main() {
	outPath := flag.String("out", "", "output path for sealed key file (required)")
	passphrase := flag.String("passphrase", "", "passphrase to seal the key (required, or use -passphrase-file)")
	passFile := flag.String("passphrase-file", "", "read passphrase from this file")
	keyHex := flag.String("key", "", "hex-encoded 32-byte root key (optional; random if omitted)")
	printKey := flag.Bool("print-key", false, "print the root key to stdout (WARNING: sensitive \u2014 never emitted by default)")
	flag.Parse()

	if *outPath == "" {
		fmt.Fprintln(os.Stderr, "seal-key: -out is required")
		flag.Usage()
		os.Exit(1)
	}

	pass := *passphrase
	if pass == "" && *passFile != "" {
		data, err := os.ReadFile(*passFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "seal-key: reading passphrase file: %v\n", err)
			os.Exit(1)
		}
		pass = string(data)
	}
	if pass == "" {
		fmt.Fprintln(os.Stderr, "seal-key: -passphrase or -passphrase-file is required")
		flag.Usage()
		os.Exit(1)
	}

	var rootKey []byte
	if *keyHex != "" {
		decoded, err := hex.DecodeString(*keyHex)
		if err != nil {
			fmt.Fprintf(os.Stderr, "seal-key: invalid hex key: %v\n", err)
			os.Exit(1)
		}
		if len(decoded) != 32 {
			fmt.Fprintf(os.Stderr, "seal-key: key must be 32 bytes (64 hex chars), got %d bytes\n", len(decoded))
			os.Exit(1)
		}
		rootKey = decoded
	} else {
		rootKey = make([]byte, 32)
		if _, err := rand.Read(rootKey); err != nil {
			fmt.Fprintf(os.Stderr, "seal-key: generating key: %v\n", err)
			os.Exit(1)
		}
	}

	// The root key is never emitted unless the operator explicitly opts in with
	// -print-key. By default a freshly generated key lives only inside the
	// sealed output file (recoverable only via the passphrase), so it does not
	// leak through stderr/stdout/logs/process listings.
	if *printKey {
		_, _ = fmt.Fprintln(os.Stdout, "WARNING: the root key below can decrypt every at-rest credential. Store it securely and never commit it.")
		_, _ = fmt.Fprintln(os.Stdout, hex.EncodeToString(rootKey))
	}

	if err := secrets.SealRootKey(*outPath, []byte(pass), rootKey); err != nil {
		fmt.Fprintf(os.Stderr, "seal-key: %v\n", err)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "Sealed key written to %s\n", *outPath)
}
