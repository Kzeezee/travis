// Command fakeserver is a tiny, real MCP stdio server used only by internal/tools/mcp's
// own tests, in place of a real public server (the project's testing convention: fakes for
// process boundaries, never a live third-party service). It exposes three tools: echo, which
// proves a normal round trip; crash, which exits the process so restart-with-backoff
// supervision can be exercised against a real dead subprocess; and env, which reports one
// environment variable so tests can prove what a server can see.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type echoArgs struct {
	Message string `json:"message"`
}

func main() {
	server := mcp.NewServer(&mcp.Implementation{Name: "fakeserver", Version: "0.1.0"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "echo", Description: "echoes the message back"}, echoTool)
	mcp.AddTool(server, &mcp.Tool{Name: "crash", Description: "exits the process immediately"}, crashTool)
	mcp.AddTool(server, &mcp.Tool{Name: "env", Description: "reports one environment variable"}, envTool)

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

type envArgs struct {
	Name string `json:"name"`
}

func envTool(_ context.Context, _ *mcp.CallToolRequest, args envArgs) (*mcp.CallToolResult, any, error) {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: args.Name + "=" + os.Getenv(args.Name)}}}, nil, nil
}
