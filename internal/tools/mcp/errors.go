package mcp

import "github.com/Kzeezee/travis/internal/core"

// sourceName is how one server's errors and untrusted content are both labelled, so a
// human reading a transcript or a trace sees the same name either way.
func sourceName(server string) string {
	return "mcp:" + server
}

// translateConnectError reports a server that failed to start or complete its MCP
// handshake; retrying later may succeed once the supervisor's backoff elapses.
func translateConnectError(server string, cause error) error {
	return &core.ProviderError{Provider: sourceName(server), Kind: core.ErrTransient, Cause: cause}
}

// translateCallError reports a tools/call that failed at the transport or protocol level,
// as opposed to the tool itself reporting failure through CallToolResult.IsError.
func translateCallError(server string, cause error) error {
	return &core.ProviderError{Provider: sourceName(server), Kind: core.ErrTransient, Cause: cause}
}
