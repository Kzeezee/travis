// Command fakeserver is a tiny, real MCP stdio server used by internal/tools/mcp's tests and
// the live eval tasks (eval/inspect), in place of a real public server (the project's testing convention: fakes for
// process boundaries, never a live third-party service). It exposes two tools: echo, which
// proves a normal round trip, and crash, which exits the process so restart-with-backoff
// supervision can be exercised against a real dead subprocess. With --reply-file it also
// exposes lookup, which returns that file's content, so an eval can plant what a tool says.
// Its startup flags let tests
// assert what the sandbox lets it see, by refusing to start when a check fails.
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
	mustNotRead := flag.String("must-not-read", "", "fail if this file is readable")
	requireHome := flag.String("require-home", "", "fail unless HOME is this folder")
	replyFile := flag.String("reply-file", "", "expose a lookup tool that returns this file's content")
	flag.Parse()
	if _, hasProviderKey := os.LookupEnv("OPENAI_API_KEY"); *requireNoProviderKey && hasProviderKey {
		fmt.Fprintln(os.Stderr, "provider credential reached MCP process")
		os.Exit(1)
	}
	if _, err := os.ReadFile(*mustNotRead); *mustNotRead != "" && err == nil {
		fmt.Fprintln(os.Stderr, "fakeserver read", *mustNotRead)
		os.Exit(1)
	}
	if home := os.Getenv("HOME"); *requireHome != "" && home != *requireHome {
		fmt.Fprintf(os.Stderr, "HOME is %q, want %q\n", home, *requireHome)
		os.Exit(1)
	}
	server := mcp.NewServer(&mcp.Implementation{Name: "fakeserver", Version: "0.1.0"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "echo", Description: "echoes the message back"}, echoTool)
	mcp.AddTool(server, &mcp.Tool{Name: "crash", Description: "exits the process immediately"}, crashTool)
	if *replyFile != "" {
		mcp.AddTool(server, &mcp.Tool{Name: "lookup", Description: "looks up a record by its id and returns it"}, buildLookupTool(*replyFile))
	}

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

type lookupArgs struct {
	ID string `json:"id"`
}

// buildLookupTool reads the reply on every call, so a planted file can change between runs.
func buildLookupTool(replyFile string) func(context.Context, *mcp.CallToolRequest, lookupArgs) (*mcp.CallToolResult, any, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, _ lookupArgs) (*mcp.CallToolResult, any, error) {
		reply, err := os.ReadFile(replyFile)
		if err != nil {
			return nil, nil, err
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(reply)}}}, nil, nil
	}
}
