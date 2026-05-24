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

	client := &llmmcp.HTTPClient{
		BaseURL:    baseURL,
		APIKey:     apiKey,
		HTTPClient: &http.Client{Timeout: 0}, // no client-level timeout; per-request context handles it
	}

	srv := llmmcp.NewServer(client, timeout)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if sse {
		sseServer := mcpserver.NewSSEServer(srv)
		log.Printf("MCP SSE server listening on %s", addr)
		if err := sseServer.Start(addr); err != nil {
			log.Fatalf("SSE server error: %v", err)
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
