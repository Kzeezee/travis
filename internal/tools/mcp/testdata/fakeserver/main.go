// Command fakeserver is a tiny, real MCP stdio server used only by internal/tools/mcp's
// own tests, in place of a real public server (the project's testing convention: fakes for
// process boundaries, never a live third-party service). It exposes two tools: echo, which
// proves a normal round trip, and crash, which exits the process so restart-with-backoff
// supervision can be exercised against a real dead subprocess.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type echoArgs struct {
	Message string `json:"message"`
}

func main() {
	requireNoProviderKey := flag.Bool("require-no-provider-key", false, "fail if a provider credential was inherited")
	flag.Parse()
	if _, hasProviderKey := os.LookupEnv("OPENAI_API_KEY"); *requireNoProviderKey && hasProviderKey {
		fmt.Fprintln(os.Stderr, "provider credential reached MCP process")
		os.Exit(1)
	}
	server := mcp.NewServer(&mcp.Implementation{Name: "fakeserver", Version: "0.1.0"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "echo", Description: "echoes the message back"}, echoTool)
	mcp.AddTool(server, &mcp.Tool{Name: "crash", Description: "exits the process immediately"}, crashTool)

	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		fmt.Fprintln(os.Stderr, "fakeserver:", err)
		os.Exit(1)
	}
}

func echoTool(_ context.Context, _ *mcp.CallToolRequest, args echoArgs) (*mcp.CallToolResult, any, error) {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: args.Message}}}, nil, nil
}

func crashTool(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
	os.Exit(1)
	return nil, nil, nil
}
