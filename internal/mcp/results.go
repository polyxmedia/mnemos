package mcp

import (
	"encoding/json"
	"fmt"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// jsonResult packages a value into a ToolResult with a single pretty-
// printed JSON text block. Every tool handler ends with this (or
// textResult) so agents receive structured, parseable output.
func jsonResult(v any) (*mcpsdk.CallToolResult, any, error) {
	buf, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, nil, fmt.Errorf("marshal result: %w", err)
	}
	return &mcpsdk.CallToolResult{
		Content:           []mcpsdk.Content{&mcpsdk.TextContent{Text: string(buf)}},
		StructuredContent: v,
	}, nil, nil
}

// textResult packages a plain string into a ToolResult. Use for
// operations that don't return data (delete, close, touch, link).
func textResult(msg string) (*mcpsdk.CallToolResult, any, error) {
	return &mcpsdk.CallToolResult{
		Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: msg}},
	}, nil, nil
}
