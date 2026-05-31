package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/lenaxia/llmsafespace/pkg/redact"
)

func main() {
	configPath := flag.String("config", "/sandbox-cfg/redact-patterns.json", "path to extra patterns JSON file")
	flag.Parse()

	r, err := redact.NewRedactorFromFile(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "redact: %v\n", err)
		os.Exit(1)
	}

	input, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "redact: failed to read stdin: %v\n", err)
		os.Exit(1)
	}

	result, err := r.Redact(string(input))
	if err != nil {
		fmt.Fprintf(os.Stderr, "redact: %v\n", err)
		os.Exit(1)
	}

	_, _ = fmt.Fprint(os.Stdout, result)
}
