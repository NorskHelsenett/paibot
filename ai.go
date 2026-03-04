package main

import (
	"context"
	"sync"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/packages/param"
)

type AIClient struct {
	client  openai.Client
	config  *BotConfig
	history map[string][]openai.ChatCompletionMessageParamUnion
	mu      sync.RWMutex
}

func NewAIClient(baseURL, apiKey string, config *BotConfig) *AIClient {
	return &AIClient{
		client: openai.NewClient(
			option.WithAPIKey(apiKey),
			option.WithBaseURL(baseURL),
		),
		config:  config,
		history: make(map[string][]openai.ChatCompletionMessageParamUnion),
	}
}

func (a *AIClient) callAI(ctx context.Context, messages []openai.ChatCompletionMessageParamUnion) (string, error) {
	params := openai.ChatCompletionNewParams{
		Model:    openai.ChatModel(a.config.Model),
		Messages: messages,
	}
	if a.config.Temp != 0 {
		params.Temperature = param.NewOpt(float64(a.config.Temp))
	}
	if a.config.MaxTokens > 0 {
		params.MaxTokens = param.NewOpt(int64(a.config.MaxTokens))
	}

	resp, err := a.client.Chat.Completions.New(ctx, params)
	if err != nil {
		return "", err
	}
	return resp.Choices[0].Message.Content, nil
}

// chat is the shared implementation for Chat and ChatWithThread.
// systemPrompt is used to seed history on the first message.
func (a *AIClient) chat(ctx context.Context, conversationID, userMessage, systemPrompt string) (string, error) {
	a.mu.Lock()
	if _, exists := a.history[conversationID]; !exists && systemPrompt != "" {
		a.history[conversationID] = []openai.ChatCompletionMessageParamUnion{openai.SystemMessage(systemPrompt)}
	}
	a.history[conversationID] = append(a.history[conversationID], openai.UserMessage(userMessage))
	messages := append([]openai.ChatCompletionMessageParamUnion{}, a.history[conversationID]...)
	a.mu.Unlock()

	reply, err := a.callAI(ctx, messages)
	if err != nil {
		return "", err
	}

	a.mu.Lock()
	a.history[conversationID] = append(a.history[conversationID], openai.AssistantMessage(reply))
	a.mu.Unlock()

	return reply, nil
}

func (a *AIClient) Chat(ctx context.Context, conversationID, userMessage string) (string, error) {
	return a.chat(ctx, conversationID, userMessage, a.config.Prompts.Chat)
}

func (a *AIClient) ChatWithThread(ctx context.Context, conversationID, userMessage, threadContext string) (string, error) {
	return a.chat(ctx, conversationID, userMessage, a.config.Prompts.Thread+"\n\n"+threadContext)
}

func (a *AIClient) Summarize(ctx context.Context, threadMessages string) (string, error) {
	return a.callAI(ctx, []openai.ChatCompletionMessageParamUnion{
		openai.SystemMessage(a.config.Prompts.Summarize),
		openai.UserMessage(threadMessages),
	})
}
