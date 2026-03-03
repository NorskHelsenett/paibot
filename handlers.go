package main

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
)

// activeThreads tracks threads the bot is participating in.
// Key: "channel:threadTS"
var activeThreads sync.Map

// botInThread checks whether the bot has previously posted in or been @mentioned
// in the given thread.
func botInThread(client *slack.Client, botUserID, channel, threadTS string) bool {
	params := &slack.GetConversationRepliesParameters{
		ChannelID: channel,
		Timestamp: threadTS,
	}
	msgs, _, _, err := client.GetConversationReplies(params)
	if err != nil {
		log.Printf("botInThread: failed to fetch replies for %s:%s: %v", channel, threadTS, err)
		return false
	}
	mention := "<@" + botUserID + ">"
	for _, m := range msgs {
		if m.User == botUserID {
			return true
		}
		if strings.Contains(m.Text, mention) {
			return true
		}
	}
	return false
}

// fetchThreadContext retrieves all messages in a thread and formats them for AI context.
func fetchThreadContext(client *slack.Client, channel, threadTS string) (string, error) {
	params := &slack.GetConversationRepliesParameters{
		ChannelID: channel,
		Timestamp: threadTS,
	}
	msgs, _, _, err := client.GetConversationReplies(params)
	if err != nil {
		return "", fmt.Errorf("conversations.replies: %w", err)
	}

	var sb strings.Builder
	for _, m := range msgs {
		user := m.User
		if user == "" {
			user = m.BotID
		}
		if user == "" {
			user = "unknown"
		}
		sb.WriteString(fmt.Sprintf("<@%s>: %s\n", user, m.Text))
	}
	return sb.String(), nil
}

func handleAppMention(client *slack.Client, ai *AIClient, botUserID string, event *slackevents.AppMentionEvent) {
	// Strip the bot mention prefix (e.g. "<@U12345> ")
	text := strings.TrimSpace(event.Text)
	if idx := strings.Index(text, ">"); idx != -1 {
		text = strings.TrimSpace(text[idx+1:])
	}
	if text == "" {
		return
	}

	// Use thread_ts if in a thread, otherwise the message ts starts a new thread
	threadTS := event.ThreadTimeStamp
	if threadTS == "" {
		threadTS = event.TimeStamp
	}

	// Add 🤔 reaction to indicate we're working on it
	if err := client.AddReaction("thinking_face", slack.ItemRef{
		Channel:   event.Channel,
		Timestamp: event.TimeStamp,
	}); err != nil {
		log.Printf("Failed to add thinking reaction: %v", err)
	}

	conversationID := event.Channel + ":" + threadTS

	var reply string
	var err error

	// If mentioned inside an existing thread, fetch thread context so the bot can participate
	if event.ThreadTimeStamp != "" {
		threadContext, fetchErr := fetchThreadContext(client, event.Channel, threadTS)
		if fetchErr != nil {
			log.Printf("Failed to fetch thread context: %v", fetchErr)
		}
		reply, err = ai.ChatWithThread(context.Background(), conversationID, text, threadContext)
	} else {
		reply, err = ai.Chat(context.Background(), conversationID, text)
	}

	if err != nil {
		log.Printf("AI error for mention in %s: %v", conversationID, err)
		reply = "Sorry, I encountered an error. Please try again."
	}

	_, _, postErr := client.PostMessage(
		event.Channel,
		slack.MsgOptionText(reply, false),
		slack.MsgOptionTS(threadTS),
	)
	if postErr != nil {
		log.Printf("Failed to post mention reply: %v", postErr)
	}

	// Remove 🤔 and add ✅ to indicate we're done
	_ = client.RemoveReaction("thinking_face", slack.ItemRef{
		Channel:   event.Channel,
		Timestamp: event.TimeStamp,
	})
	if err := client.AddReaction("white_check_mark", slack.ItemRef{
		Channel:   event.Channel,
		Timestamp: event.TimeStamp,
	}); err != nil {
		log.Printf("Failed to add done reaction: %v", err)
	}

	// Mark this thread as active so we respond to follow-up messages
	activeThreads.Store(event.Channel+":"+threadTS, true)
}

// handleThreadMessage handles messages in threads the bot is already participating in.
func handleThreadMessage(client *slack.Client, ai *AIClient, botUserID string, event *slackevents.MessageEvent) {
	log.Printf("handleThreadMessage: entering for user=%s channel=%s threadTS=%s text=%q",
		event.User, event.Channel, event.ThreadTimeStamp, event.Text)

	// Ignore bot's own messages, edited messages, and empty text
	if event.User == botUserID {
		log.Printf("handleThreadMessage: ignoring own message (botUserID=%s)", botUserID)
		return
	}
	if event.BotID != "" {
		log.Printf("handleThreadMessage: ignoring bot message (botID=%s)", event.BotID)
		return
	}
	if event.SubType != "" {
		log.Printf("handleThreadMessage: ignoring subtype=%s", event.SubType)
		return
	}

	text := strings.TrimSpace(event.Text)
	if text == "" {
		log.Printf("handleThreadMessage: ignoring empty text")
		return
	}

	threadTS := event.ThreadTimeStamp
	if threadTS == "" {
		log.Printf("handleThreadMessage: ignoring non-threaded message")
		return
	}

	threadKey := event.Channel + ":" + threadTS
	if _, active := activeThreads.Load(threadKey); !active {
		log.Printf("handleThreadMessage: thread %s not in memory, checking API...", threadKey)
		// Not tracked in memory — check actual thread history for bot participation
		if !botInThread(client, botUserID, event.Channel, threadTS) {
			log.Printf("handleThreadMessage: bot NOT found in thread %s, ignoring", threadKey)
			return
		}
		log.Printf("handleThreadMessage: bot found in thread %s via API, registering", threadKey)
		// Re-register so subsequent messages don't need the API call
		activeThreads.Store(threadKey, true)
	} else {
		log.Printf("handleThreadMessage: thread %s is active in memory", threadKey)
	}

	log.Printf("Thread reply from %s in %s: %q", event.User, threadKey, text)

	// Add 🤔 reaction while processing
	if err := client.AddReaction("thinking_face", slack.ItemRef{
		Channel:   event.Channel,
		Timestamp: event.TimeStamp,
	}); err != nil {
		log.Printf("Failed to add thinking reaction: %v", err)
	}

	conversationID := threadKey

	// Fetch thread context for the AI
	threadContext, fetchErr := fetchThreadContext(client, event.Channel, threadTS)
	if fetchErr != nil {
		log.Printf("Failed to fetch thread context: %v", fetchErr)
	}

	reply, err := ai.ChatWithThread(context.Background(), conversationID, text, threadContext)
	if err != nil {
		log.Printf("AI error for thread reply in %s: %v", conversationID, err)
		reply = "Sorry, I encountered an error. Please try again."
	}

	_, _, postErr := client.PostMessage(
		event.Channel,
		slack.MsgOptionText(reply, false),
		slack.MsgOptionTS(threadTS),
	)
	if postErr != nil {
		log.Printf("Failed to post thread reply: %v", postErr)
	}

	// Remove 🤔 and add ✅
	_ = client.RemoveReaction("thinking_face", slack.ItemRef{
		Channel:   event.Channel,
		Timestamp: event.TimeStamp,
	})
	_ = client.AddReaction("white_check_mark", slack.ItemRef{
		Channel:   event.Channel,
		Timestamp: event.TimeStamp,
	})
}

func handleDM(client *slack.Client, ai *AIClient, event *slackevents.MessageEvent) {
	// Ignore bot messages and edited messages
	if event.BotID != "" || event.SubType != "" {
		return
	}

	text := strings.TrimSpace(event.Text)
	if text == "" {
		return
	}

	// Use existing thread or start a new one from the message timestamp
	threadTS := event.ThreadTimeStamp
	if threadTS == "" {
		threadTS = event.TimeStamp
	}

	// Use channel + thread as the conversation key
	conversationID := event.Channel + ":" + threadTS

	reply, err := ai.Chat(context.Background(), conversationID, text)
	if err != nil {
		log.Printf("AI error for DM in %s: %v", conversationID, err)
		reply = "Sorry, I encountered an error. Please try again."
	}

	_, _, err = client.PostMessage(
		event.Channel,
		slack.MsgOptionText(reply, false),
		slack.MsgOptionTS(threadTS),
	)
	if err != nil {
		log.Printf("Failed to post DM reply: %v", err)
	}
}

func handleEvents(client *slack.Client, ai *AIClient, botUserID string, socketClient *socketmode.Client) {
	for evt := range socketClient.Events {
		switch evt.Type {
		case socketmode.EventTypeConnecting:
			log.Println("Connecting to Slack...")

		case socketmode.EventTypeConnectionError:
			log.Printf("Connection error: %v", evt.Data)

		case socketmode.EventTypeConnected:
			log.Println("Connected to Slack with Socket Mode")

		case socketmode.EventTypeDisconnect:
			log.Printf("Disconnected from Slack: %v", evt.Data)

		case socketmode.EventTypeHello:
			log.Println("Received hello from Slack")

		case socketmode.EventTypeEventsAPI:
			eventsAPIEvent, ok := evt.Data.(slackevents.EventsAPIEvent)
			if !ok {
				continue
			}
			socketClient.Ack(*evt.Request)
			log.Printf("Event received: type=%s inner=%s", eventsAPIEvent.Type, eventsAPIEvent.InnerEvent.Type)

			switch eventsAPIEvent.Type {
			case slackevents.CallbackEvent:
				switch ev := eventsAPIEvent.InnerEvent.Data.(type) {
				case *slackevents.AppMentionEvent:
					log.Printf("app_mention from %s: %q", ev.User, ev.Text)
					go handleAppMention(client, ai, botUserID, ev)
				case *slackevents.MessageEvent:
					log.Printf("message event: channel=%s subtype=%q botID=%q user=%q threadTS=%q text=%q",
						ev.Channel, ev.SubType, ev.BotID, ev.User, ev.ThreadTimeStamp, ev.Text)
					if strings.HasPrefix(ev.Channel, "D") {
						// Direct messages
						go handleDM(client, ai, ev)
					} else if ev.ThreadTimeStamp != "" {
						// Thread reply in a channel — respond if bot is participating
						go handleThreadMessage(client, ai, botUserID, ev)
					}
				default:
					log.Printf("unhandled inner event type: %T", eventsAPIEvent.InnerEvent.Data)
				}
			}

		case socketmode.EventTypeSlashCommand:
			cmd, ok := evt.Data.(slack.SlashCommand)
			if !ok {
				continue
			}
			log.Printf("Slash command received: %s %s (from %s)", cmd.Command, cmd.Text, cmd.UserName)
			socketClient.Ack(*evt.Request)

		case socketmode.EventTypeInteractive:
			callback, ok := evt.Data.(slack.InteractionCallback)
			if !ok {
				continue
			}
			log.Printf("Interactive event received: type=%s", callback.Type)
			socketClient.Ack(*evt.Request)

		default:
			log.Printf("Unhandled event type: %s", evt.Type)
		}
	}
}
