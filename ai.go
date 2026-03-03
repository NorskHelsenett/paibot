package main

import (
	"context"
	"sync"

	openai "github.com/sashabaranov/go-openai"
)

type AIClient struct {
	client  *openai.Client
	config  *BotConfig
	history map[string][]openai.ChatCompletionMessage
	mu      sync.RWMutex
}

func NewAIClient(baseURL, apiKey string, config *BotConfig) *AIClient {
	cfg := openai.DefaultConfig(apiKey)
	cfg.BaseURL = baseURL

	return &AIClient{
		client:  openai.NewClientWithConfig(cfg),
		config:  config,
		history: make(map[string][]openai.ChatCompletionMessage),
	}
}

func (a *AIClient) Chat(ctx context.Context, conversationID, userMessage string) (string, error) {
	a.mu.Lock()
	// Seed with system prompt on first message
	if _, exists := a.history[conversationID]; !exists && a.config.Prompts.Chat != "" {
		a.history[conversationID] = append(a.history[conversationID], openai.ChatCompletionMessage{
			Role:    openai.ChatMessageRoleSystem,
			Content: a.config.Prompts.Chat,
		})
	}
	a.history[conversationID] = append(a.history[conversationID], openai.ChatCompletionMessage{
		Role:    openai.ChatMessageRoleUser,
		Content: userMessage,
	})
	messages := make([]openai.ChatCompletionMessage, len(a.history[conversationID]))
	copy(messages, a.history[conversationID])
	a.mu.Unlock()

	req := openai.ChatCompletionRequest{
		Model:       a.config.Model,
		Messages:    messages,
		Temperature: a.config.Temp,
	}
	if a.config.MaxTokens > 0 {
		req.MaxTokens = a.config.MaxTokens
	}

	resp, err := a.client.CreateChatCompletion(ctx, req)
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
		prompt := a.config.Prompts.Thread + "\n\n" + threadContext
		a.history[conversationID] = append(a.history[conversationID], openai.ChatCompletionMessage{
			Role:    openai.ChatMessageRoleSystem,
			Content: prompt,
		})
	}
	a.history[conversationID] = append(a.history[conversationID], openai.ChatCompletionMessage{
		Role:    openai.ChatMessageRoleUser,
		Content: userMessage,
	})
	messages := make([]openai.ChatCompletionMessage, len(a.history[conversationID]))
	copy(messages, a.history[conversationID])
	a.mu.Unlock()

	req := openai.ChatCompletionRequest{
		Model:       a.config.Model,
		Messages:    messages,
		Temperature: a.config.Temp,
	}
	if a.config.MaxTokens > 0 {
		req.MaxTokens = a.config.MaxTokens
	}

	resp, err := a.client.CreateChatCompletion(ctx, req)
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
			Content: a.config.Prompts.Summarize,
		},
		{
			Role:    openai.ChatMessageRoleUser,
			Content: threadMessages,
		},
	}

	req := openai.ChatCompletionRequest{
		Model:       a.config.Model,
		Messages:    messages,
		Temperature: a.config.Temp,
	}
	if a.config.MaxTokens > 0 {
		req.MaxTokens = a.config.MaxTokens
	}

	resp, err := a.client.CreateChatCompletion(ctx, req)
	if err != nil {
		return "", err
	}

	return resp.Choices[0].Message.Content, nil
}
