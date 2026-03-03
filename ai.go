package main

import (
	"context"
	"sync"

	openai "github.com/sashabaranov/go-openai"
)

type AIClient struct {
	client  *openai.Client
	model   string
	history map[string][]openai.ChatCompletionMessage
	mu      sync.RWMutex
}

func NewAIClient(baseURL, apiKey, model string) *AIClient {
	cfg := openai.DefaultConfig(apiKey)
	cfg.BaseURL = baseURL

	return &AIClient{
		client:  openai.NewClientWithConfig(cfg),
		model:   model,
		history: make(map[string][]openai.ChatCompletionMessage),
	}
}

func (a *AIClient) Chat(ctx context.Context, conversationID, userMessage string) (string, error) {
	a.mu.Lock()
	a.history[conversationID] = append(a.history[conversationID], openai.ChatCompletionMessage{
		Role:    openai.ChatMessageRoleUser,
		Content: userMessage,
	})
	messages := make([]openai.ChatCompletionMessage, len(a.history[conversationID]))
	copy(messages, a.history[conversationID])
	a.mu.Unlock()

	resp, err := a.client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
		Model:    a.model,
		Messages: messages,
	})
	if err != nil {
		return "", err
	}

	reply := resp.Choices[0].Message.Content

	a.mu.Lock()
	a.history[conversationID] = append(a.history[conversationID], openai.ChatCompletionMessage{
		Role:    openai.ChatMessageRoleAssistant,
		Content: reply,
	})
	a.mu.Unlock()

	return reply, nil
}

// ChatWithThread sends the user message along with prior thread context to the AI.
// threadMessages should be the full thread history formatted as "user: text" lines.
func (a *AIClient) ChatWithThread(ctx context.Context, conversationID, userMessage string, threadContext string) (string, error) {
	a.mu.Lock()
	// Seed history with thread context so the bot can participate in the discussion
	if _, exists := a.history[conversationID]; !exists && threadContext != "" {
		a.history[conversationID] = append(a.history[conversationID], openai.ChatCompletionMessage{
			Role: openai.ChatMessageRoleSystem,
			Content: "You are PAI, a helpful Slack bot participating in a thread discussion. " +
				"Here is the conversation so far — read it to understand the context, " +
				"then respond naturally to the latest message as a participant:\n\n" + threadContext,
		})
	}
	a.history[conversationID] = append(a.history[conversationID], openai.ChatCompletionMessage{
		Role:    openai.ChatMessageRoleUser,
		Content: userMessage,
	})
	messages := make([]openai.ChatCompletionMessage, len(a.history[conversationID]))
	copy(messages, a.history[conversationID])
	a.mu.Unlock()

	resp, err := a.client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
		Model:    a.model,
		Messages: messages,
	})
	if err != nil {
		return "", err
	}

	reply := resp.Choices[0].Message.Content

	a.mu.Lock()
	a.history[conversationID] = append(a.history[conversationID], openai.ChatCompletionMessage{
		Role:    openai.ChatMessageRoleAssistant,
		Content: reply,
	})
	a.mu.Unlock()

	return reply, nil
}

// Summarize asks the AI to produce a concise summary of the given thread messages.
func (a *AIClient) Summarize(ctx context.Context, threadMessages string) (string, error) {
	messages := []openai.ChatCompletionMessage{
		{
			Role:    openai.ChatMessageRoleSystem,
			Content: "You are a helpful assistant. Summarize the following Slack thread conversation concisely, capturing the key points, decisions, and action items.",
		},
		{
			Role:    openai.ChatMessageRoleUser,
			Content: threadMessages,
		},
	}

	resp, err := a.client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
		Model:    a.model,
		Messages: messages,
	})
	if err != nil {
		return "", err
	}

	return resp.Choices[0].Message.Content, nil
}
