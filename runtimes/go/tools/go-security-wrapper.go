package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type RestrictedPackages struct {
	Blocked []string `json:"blocked"`
	Warning []string `json:"warning"`
}

func main() {
	// Load restricted packages configuration
	configFile, err := os.Open("/etc/llmsafespace/go/restricted_packages.json")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading restricted packages: %v\n", err)
		os.Exit(1)
	}
	defer configFile.Close()

	var restricted RestrictedPackages
	if err := json.NewDecoder(configFile).Decode(&restricted); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing restricted packages: %v\n", err)
		os.Exit(1)
	}

	// Check if a source file is provided
	if len(os.Args) < 2 {
		fmt.Println("LLMSafeSpace Go Environment")
		fmt.Println("Usage: go-security-wrapper <source-file>")
		os.Exit(0)
	}

	sourceFile := os.Args[1]

	// Read and analyze the source code
	source, err := ioutil.ReadFile(sourceFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading source file: %v\n", err)
		os.Exit(1)
	}

	// Check for restricted packages
	for _, pkg := range restricted.Blocked {
		if strings.Contains(string(source), fmt.Sprintf("import \"%s\"", pkg)) ||
			strings.Contains(string(source), fmt.Sprintf("import (\n\t\"%s\"", pkg)) {
			fmt.Fprintf(os.Stderr, "Error: Use of restricted package '%s' is not allowed\n", pkg)
			os.Exit(1)
		}
	}

	// Check for warning packages
	for _, pkg := range restricted.Warning {
		if strings.Contains(string(source), fmt.Sprintf("import \"%s\"", pkg)) ||
			strings.Contains(string(source), fmt.Sprintf("import (\n\t\"%s\"", pkg)) {
			fmt.Fprintf(os.Stderr, "Warning: Use of package '%s' may pose security risks\n", pkg)
		}
	}

	// Compile and run the code
	tempDir, err := ioutil.TempDir("", "llmsafespace-go-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating temp directory: %v\n", err)
		os.Exit(1)
	}
	defer os.RemoveAll(tempDir)

	outputBinary := filepath.Join(tempDir, "program")
	buildCmd := exec.Command("go", "build", "-o", outputBinary, sourceFile)
	buildCmd.Env = append(os.Environ(),
		"GOGC=50",
		"GOMAXPROCS=2",
		"GOMEMLIMIT=512MiB",
	)

	if output, err := buildCmd.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "Build error: %v\n%s\n", err, output)
		os.Exit(1)
	}

	// Execute the compiled program
	cmd := exec.Command(outputBinary)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			os.Exit(exitErr.ExitCode())
		}
		fmt.Fprintf(os.Stderr, "Error running program: %v\n", err)
		os.Exit(1)
	}
}
