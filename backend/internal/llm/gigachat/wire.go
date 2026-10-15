package gigachat

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/llm"
)

// Wire types of POST /chat/completions (GigaChat API v1).

type chatRequest struct {
	Model        string        `json:"model"`
	Messages     []wireMessage `json:"messages"`
	Functions    []wireFunc    `json:"functions,omitempty"`
	FunctionCall string        `json:"function_call,omitempty"`
	Temperature  *float64      `json:"temperature,omitempty"`
}

type wireMessage struct {
	Role             string        `json:"role"`
	Content          string        `json:"content"`
	Name             string        `json:"name,omitempty"`
	FunctionCall     *wireFuncCall `json:"function_call,omitempty"`
	FunctionsStateID string        `json:"functions_state_id,omitempty"`
}

type wireFuncCall struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type wireFunc struct {
	Name            string            `json:"name"`
	Description     string            `json:"description,omitempty"`
	Parameters      json.RawMessage   `json:"parameters"`
	FewShotExamples []wireFewShotExpl `json:"few_shot_examples,omitempty"`
}

type wireFewShotExpl struct {
	Request string         `json:"request"`
	Params  map[string]any `json:"params"`
}

type chatResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		Message      wireMessage `json:"message"`
		FinishReason string      `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens          int `json:"prompt_tokens"`
		CompletionTokens      int `json:"completion_tokens"`
		PrecachedPromptTokens int `json:"precached_prompt_tokens"`
		TotalTokens           int `json:"total_tokens"`
	} `json:"usage"`
}

func (c *Client) buildRequest(req llm.Request) chatRequest {
	out := chatRequest{
		Model:       c.cfg.Model,
		Messages:    make([]wireMessage, 0, len(req.Messages)),
		Temperature: c.cfg.Temperature,
	}
	for _, m := range req.Messages {
		out.Messages = append(out.Messages, toWireMessage(m))
	}
	for _, t := range req.Tools {
		f := wireFunc{Name: t.Name, Description: t.Description, Parameters: t.Parameters}
		for _, ex := range t.Examples {
			f.FewShotExamples = append(f.FewShotExamples, wireFewShotExpl(ex))
		}
		out.Functions = append(out.Functions, f)
	}
	if len(out.Functions) > 0 {
		out.FunctionCall = "auto"
	}
	return out
}

func toWireMessage(m llm.Message) wireMessage {
	switch m.Role {
	case llm.RoleTool:
		// GigaChat calls tool results "function" messages.
		return wireMessage{Role: "function", Name: m.ToolName, Content: m.Content}
	case llm.RoleAssistant:
		w := wireMessage{Role: "assistant", Content: m.Content, FunctionsStateID: m.StateID}
		if m.ToolCall != nil {
			w.FunctionCall = &wireFuncCall{Name: m.ToolCall.Name, Arguments: m.ToolCall.Arguments}
		}
		return w
	default:
		return wireMessage{Role: string(m.Role), Content: m.Content}
	}
}

func convertResponse(resp chatResponse) (llm.Response, error) {
	if len(resp.Choices) == 0 {
		return llm.Response{}, errors.New("gigachat: response has no choices")
	}
	choice := resp.Choices[0]
	if choice.FinishReason == "blacklist" {
		return llm.Response{}, llm.ErrBlocked
	}

	msg := llm.Message{
		Role:    llm.RoleAssistant,
		Content: choice.Message.Content,
		StateID: choice.Message.FunctionsStateID,
	}
	if fc := choice.Message.FunctionCall; fc != nil && fc.Name != "" {
		args, err := normalizeArguments(fc.Arguments)
		if err != nil {
			return llm.Response{}, fmt.Errorf("gigachat: function %s: %w", fc.Name, err)
		}
		msg.ToolCall = &llm.ToolCall{Name: fc.Name, Arguments: args}
	}
	return llm.Response{
		Message: msg,
		Usage: llm.Usage{
			PromptTokens:     resp.Usage.PromptTokens,
			CompletionTokens: resp.Usage.CompletionTokens,
			CachedTokens:     resp.Usage.PrecachedPromptTokens,
			TotalTokens:      resp.Usage.TotalTokens,
		},
	}, nil
}

// normalizeArguments accepts arguments both as a JSON object and as a JSON
// string containing an object (the docs describe both) and returns the object.
func normalizeArguments(raw json.RawMessage) (json.RawMessage, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return json.RawMessage(`{}`), nil
	}
	if raw[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, fmt.Errorf("decode string arguments: %w", err)
		}
		raw = json.RawMessage(s)
	}
	if !json.Valid(raw) || bytes.TrimSpace(raw)[0] != '{' {
		return nil, fmt.Errorf("arguments are not a JSON object: %s", truncate(string(raw), 200))
	}
	return raw, nil
}
