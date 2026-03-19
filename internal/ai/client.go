package ai

import (
	"context"
	"fmt"
	"time"

	"github.com/jonasbg/paibot/internal/config"
	"github.com/jonasbg/paibot/internal/extract"
	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/packages/param"
)

type Client struct {
	client openai.Client
	config *config.BotConfig
}

func NewClient(baseURL, apiKey string, cfg *config.BotConfig) *Client {
	return &Client{
		client: openai.NewClient(
			option.WithAPIKey(apiKey),
			option.WithBaseURL(baseURL),
		),
		config: cfg,
	}
}

// buildSystemPrompt adds current date/time and workspace context to the system prompt
func (a *Client) buildSystemPrompt(basePrompt string) string {
	now := time.Now()
	dayName := now.Weekday().String()
	dateStr := now.Format("January 2, 2006") // e.g., "March 19, 2026"
	timeStr := now.Format("15:04:05")        // e.g., "14:30:45"

	header := fmt.Sprintf("**Current context:**\nDay: %s\nDate: %s\nTime: %s (UTC)\nWorkspace: Norsk helsenett SF\n\n", dayName, dateStr, timeStr)

	return header + basePrompt
}

func (a *Client) sendMessage(ctx context.Context, system string, messages []openai.ChatCompletionMessageParamUnion) (string, error) {
	all := make([]openai.ChatCompletionMessageParamUnion, 0, len(messages)+1)
	if system != "" {
		// Add current date/time and workspace context to the system prompt
		enrichedSystem := a.buildSystemPrompt(system)
		all = append(all, openai.SystemMessage(enrichedSystem))
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
func (a *Client) Chat(ctx context.Context, userMessage string) (string, error) {
	return a.sendMessage(ctx, a.config.Prompts.Chat, []openai.ChatCompletionMessageParamUnion{
		openai.UserMessage(userMessage),
	})
}

// ChatWithThread answers using the full Slack thread as context.
func (a *Client) ChatWithThread(ctx context.Context, userMessage, threadContext string) (string, error) {
	return a.sendMessage(ctx, a.config.Prompts.Thread, []openai.ChatCompletionMessageParamUnion{
		openai.UserMessage(threadContext),
		openai.UserMessage(userMessage),
	})
}

// ChatWithFiles answers a message that includes file attachments.
func (a *Client) ChatWithFiles(ctx context.Context, userMessage string, files []extract.FileAttachment) (string, error) {
	parts := []openai.ChatCompletionContentPartUnionParam{openai.TextContentPart(userMessage)}
	parts = append(parts, extract.ToContentParts(files)...)
	return a.sendMessage(ctx, a.config.Prompts.Chat, []openai.ChatCompletionMessageParamUnion{
		openai.UserMessage(parts),
	})
}

// ChatWithThreadAndFiles answers using thread context and file attachments.
func (a *Client) ChatWithThreadAndFiles(ctx context.Context, userMessage, threadContext string, files []extract.FileAttachment) (string, error) {
	parts := []openai.ChatCompletionContentPartUnionParam{openai.TextContentPart(userMessage)}
	parts = append(parts, extract.ToContentParts(files)...)
	return a.sendMessage(ctx, a.config.Prompts.Thread, []openai.ChatCompletionMessageParamUnion{
		openai.UserMessage(threadContext),
		openai.UserMessage(parts),
	})
}
