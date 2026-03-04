package main

import (
	"context"
	"fmt"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/packages/param"
)

type AIClient struct {
	client openai.Client
	config *BotConfig
}

func NewAIClient(baseURL, apiKey string, config *BotConfig) *AIClient {
	return &AIClient{
		client: openai.NewClient(
			option.WithAPIKey(apiKey),
			option.WithBaseURL(baseURL),
		),
		config: config,
	}
}

func (a *AIClient) sendMessage(ctx context.Context, system string, messages []openai.ChatCompletionMessageParamUnion) (string, error) {
	all := make([]openai.ChatCompletionMessageParamUnion, 0, len(messages)+1)
	if system != "" {
		all = append(all, openai.SystemMessage(system))
	}
	all = append(all, messages...)

	p := openai.ChatCompletionNewParams{
		Model:    openai.ChatModel(a.config.Model),
		Messages: all,
	}
	if a.config.Temp != 0 {
		p.Temperature = param.NewOpt(float64(a.config.Temp))
	}
	if a.config.MaxTokens > 0 {
		p.MaxTokens = param.NewOpt(int64(a.config.MaxTokens))
	}

	resp, err := a.client.Chat.Completions.New(ctx, p)
	if err != nil {
		return "", err
	}
	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("AI returned no choices")
	}
	return resp.Choices[0].Message.Content, nil
}

// Chat answers a single message with no prior context (e.g. a fresh DM).
func (a *AIClient) Chat(ctx context.Context, userMessage string) (string, error) {
	return a.sendMessage(ctx, a.config.Prompts.Chat, []openai.ChatCompletionMessageParamUnion{
		openai.UserMessage(userMessage),
	})
}

// ChatWithThread answers using the full Slack thread fetched fresh on every call as context.
// No in-memory history is kept — Slack is the source of truth.
func (a *AIClient) ChatWithThread(ctx context.Context, userMessage, threadContext string) (string, error) {
	return a.sendMessage(ctx, a.config.Prompts.Thread, []openai.ChatCompletionMessageParamUnion{
		openai.UserMessage(threadContext),
		openai.UserMessage(userMessage),
	})
}

// ChatWithFiles answers a message that includes file attachments.
func (a *AIClient) ChatWithFiles(ctx context.Context, userMessage string, files []FileAttachment) (string, error) {
	parts := []openai.ChatCompletionContentPartUnionParam{
		openai.TextContentPart(userMessage),
	}
	parts = append(parts, filesToContentParts(files)...)
	return a.sendMessage(ctx, a.config.Prompts.Chat, []openai.ChatCompletionMessageParamUnion{
		openai.UserMessage(parts),
	})
}

// ChatWithThreadAndFiles answers using thread context and file attachments.
func (a *AIClient) ChatWithThreadAndFiles(ctx context.Context, userMessage, threadContext string, files []FileAttachment) (string, error) {
	parts := []openai.ChatCompletionContentPartUnionParam{
		openai.TextContentPart(userMessage),
	}
	parts = append(parts, filesToContentParts(files)...)
	return a.sendMessage(ctx, a.config.Prompts.Thread, []openai.ChatCompletionMessageParamUnion{
		openai.UserMessage(threadContext),
		openai.UserMessage(parts),
	})
}
