// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

// Command mcp runs the LLMSafeSpace MCP server.
// It supports stdio transport (default) and SSE transport (--sse flag).
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"time"

	mcpserver "github.com/mark3labs/mcp-go/server"

	llmmcp "github.com/lenaxia/llmsafespace/pkg/mcp"
)

func main() {
	var (
		baseURL string
		apiKey  string
		sse     bool
		addr    string
		timeout time.Duration
	)

	flag.StringVar(&baseURL, "base-url", envOr("LLMSAFESPACE_URL", "http://localhost:8080"), "LLMSafeSpace API base URL")
	flag.StringVar(&apiKey, "api-key", os.Getenv("LLMSAFESPACE_API_KEY"), "API key for authentication")
	flag.BoolVar(&sse, "sse", false, "Use SSE transport instead of stdio")
	flag.StringVar(&addr, "addr", envOr("MCP_ADDR", ":3001"), "SSE listen address")
	flag.DurationVar(&timeout, "timeout", 300*time.Second, "Default timeout for session_message")
	flag.Parse()

	if apiKey == "" {
		fmt.Fprintf(os.Stderr, "WARNING: no API key configured (set LLMSAFESPACE_API_KEY or --api-key). All API calls will fail with 401.\n")
	}

	client := &llmmcp.HTTPClient{
		BaseURL:    baseURL,
		APIKey:     apiKey,
		HTTPClient: &http.Client{Timeout: 0}, // per-request context handles timeouts
	}

	srv := llmmcp.NewServer(client, timeout)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if sse {
		sseServer := mcpserver.NewSSEServer(srv)
		log.Printf("MCP SSE server listening on %s", addr)

		// Run in goroutine so we can listen for shutdown signal
		errCh := make(chan error, 1)
		go func() { errCh <- sseServer.Start(addr) }()

		select {
		case err := <-errCh:
			log.Fatalf("SSE server error: %v", err)
		case <-ctx.Done():
			log.Println("Shutting down SSE server...")
		}
	} else {
		stdioServer := mcpserver.NewStdioServer(srv)
		if err := stdioServer.Listen(ctx, os.Stdin, os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "stdio server error: %v\n", err)
			os.Exit(1)
		}
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
