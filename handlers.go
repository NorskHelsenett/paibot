package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"strings"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
)

// verboseLogging controls debug-level log output. Set via --verbose flag or VERBOSE_LOGS env.
var verboseLogging bool

// nextEventID returns a random 6-char hex event ID (e.g. "a3f7b2").
func nextEventID() string {
	b := make([]byte, 3)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// logVerbose logs only when verbose mode is enabled.
func logVerbose(format string, args ...any) {
	if verboseLogging {
		log.Printf(format, args...)
	}
}

// react adds or removes a reaction emoji on a message.
func react(client *slack.Client, add bool, emoji, channel, ts string) {
	ref := slack.ItemRef{Channel: channel, Timestamp: ts}
	if add {
		_ = client.AddReaction(emoji, ref)
	} else {
		_ = client.RemoveReaction(emoji, ref)
	}
}

// fetchThreadContext retrieves all messages in a thread and formats them for AI context.
func fetchThreadContext(client *slack.Client, channel, threadTS string) (string, error) {
	msgs, _, _, err := client.GetConversationReplies(&slack.GetConversationRepliesParameters{
		ChannelID: channel,
		Timestamp: threadTS,
	})
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

func handleAppMention(client *slack.Client, ai *AIClient, event *slackevents.AppMentionEvent) {
	eid := nextEventID()

	// Strip the bot mention prefix (e.g. "<@U12345> ")
	text := strings.TrimSpace(event.Text)
	if idx := strings.Index(text, ">"); idx != -1 {
		text = strings.TrimSpace(text[idx+1:])
	}
	if text == "" {
		return
	}

	threadTS := event.ThreadTimeStamp
	if threadTS == "" {
		threadTS = event.TimeStamp
	}

	conversationID := event.Channel + ":" + threadTS
	log.Printf("[%s] mention from user=%s channel=%s — forwarding to AI", eid, event.User, conversationID)

	react(client, true, "thinking_face", event.Channel, event.TimeStamp)

	var (
		reply string
		err   error
	)
	if event.ThreadTimeStamp != "" {
		threadContext, fetchErr := fetchThreadContext(client, event.Channel, threadTS)
		if fetchErr != nil {
			logVerbose("[%s] failed to fetch thread context: %v", eid, fetchErr)
		}
		reply, err = ai.ChatWithThread(context.Background(), conversationID, text, threadContext)
	} else {
		reply, err = ai.Chat(context.Background(), conversationID, text)
	}

	if err != nil {
		log.Printf("[%s] error from AI: %v", eid, err)
		reply = "Sorry, I encountered an error. Please try again."
	}

	if _, _, postErr := client.PostMessage(
		event.Channel,
		slack.MsgOptionText(reply, false),
		slack.MsgOptionTS(threadTS),
	); postErr != nil {
		log.Printf("[%s] failed to post reply: %v", eid, postErr)
	}

	react(client, false, "thinking_face", event.Channel, event.TimeStamp)
	react(client, true, "white_check_mark", event.Channel, event.TimeStamp)

	log.Printf("[%s] answered", eid)
}

func handleDM(client *slack.Client, ai *AIClient, event *slackevents.MessageEvent) {
	eid := nextEventID()

	if event.BotID != "" || event.SubType != "" {
		return
	}

	text := strings.TrimSpace(event.Text)
	if text == "" {
		return
	}

	threadTS := event.ThreadTimeStamp
	if threadTS == "" {
		threadTS = event.TimeStamp
	}

	conversationID := event.Channel + ":" + threadTS
	log.Printf("[%s] DM from user=%s — forwarding to AI", eid, event.User)

	reply, err := ai.Chat(context.Background(), conversationID, text)
	if err != nil {
		log.Printf("[%s] error from AI: %v", eid, err)
		reply = "Sorry, I encountered an error. Please try again."
	}

	if _, _, err = client.PostMessage(
		event.Channel,
		slack.MsgOptionText(reply, false),
		slack.MsgOptionTS(threadTS),
	); err != nil {
		log.Printf("[%s] failed to post reply: %v", eid, err)
	}

	log.Printf("[%s] answered", eid)
}

func handleEvents(client *slack.Client, ai *AIClient, socketClient *socketmode.Client) {
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
			logVerbose("Event received: type=%s inner=%s", eventsAPIEvent.Type, eventsAPIEvent.InnerEvent.Type)

			if eventsAPIEvent.Type != slackevents.CallbackEvent {
				continue
			}

			switch ev := eventsAPIEvent.InnerEvent.Data.(type) {
			case *slackevents.AppMentionEvent:
				// All @mentions (channel or thread) are handled here.
				// This ensures the bot only responds in threads when explicitly mentioned.
				go handleAppMention(client, ai, ev)
			case *slackevents.MessageEvent:
				logVerbose("message event: channel=%s subtype=%q user=%q threadTS=%q",
					ev.Channel, ev.SubType, ev.User, ev.ThreadTimeStamp)
				if strings.HasPrefix(ev.Channel, "D") {
					go handleDM(client, ai, ev)
				}
				// Thread messages are intentionally ignored here; app_mention handles @mentions.
			default:
				logVerbose("unhandled inner event type: %T", eventsAPIEvent.InnerEvent.Data)
			}

		case socketmode.EventTypeSlashCommand:
			cmd, ok := evt.Data.(slack.SlashCommand)
			if !ok {
				continue
			}
			log.Printf("Slash command: %s from %s", cmd.Command, cmd.UserName)
			socketClient.Ack(*evt.Request)

		case socketmode.EventTypeInteractive:
			callback, ok := evt.Data.(slack.InteractionCallback)
			if !ok {
				continue
			}
			logVerbose("Interactive event: type=%s", callback.Type)
			socketClient.Ack(*evt.Request)

		default:
			logVerbose("Unhandled event type: %s", evt.Type)
		}
	}
}
