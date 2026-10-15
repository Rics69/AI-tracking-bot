// Package llm defines a provider-neutral chat model interface with tool calling.
// Providers (GigaChat, OpenAI-compatible, ...) live in subpackages and translate
// these types to their own wire formats.
package llm

import (
	"context"
	"encoding/json"
	"errors"
)

// Role is the author of a message.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool" // result of a tool call
)

// Message is one entry of the conversation.
type Message struct {
	Role    Role
	Content string

	// ToolCall is set on an assistant message that asks to call a tool.
	ToolCall *ToolCall
	// ToolName is set on a RoleTool message: which tool produced Content.
	ToolName string
	// StateID is opaque provider state attached to an assistant tool call
	// (GigaChat's functions_state_id); it is sent back unchanged.
	StateID string
}

// ToolCall is the model's request to run a tool.
type ToolCall struct {
	ID        string
	Name      string
	Arguments json.RawMessage // JSON object
}

// Tool describes a function the model may call.
type Tool struct {
	Name        string
	Description string
	Parameters  json.RawMessage // JSON Schema of the arguments object
	Examples    []ToolExample   // few-shot examples; providers without support ignore them
}

// ToolExample shows the model how a user request maps to tool arguments.
type ToolExample struct {
	Request string
	Params  map[string]any
}

// Request is one chat completion call.
type Request struct {
	Messages []Message
	Tools    []Tool
	// SessionID groups requests of one conversation; providers may use it for
	// prompt caching and logging.
	SessionID string
}

// Usage is token accounting of one call.
type Usage struct {
	PromptTokens     int
	CompletionTokens int
	CachedTokens     int
	TotalTokens      int
}

// Response is the model's reply: either text or a tool call in Message.
type Response struct {
	Message Message
	Usage   Usage
}

// Client is a chat model.
type Client interface {
	Chat(ctx context.Context, req Request) (Response, error)
}

// ErrBlocked means the provider refused to answer the request (content policy).
var ErrBlocked = errors.New("request blocked by the model provider")
