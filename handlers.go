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

// reactError replaces the thinking reaction with :usererror: and returns false,
// intended to be used as: if reactError(...) { return }.
func reactError(client *slack.Client, eid, msg, channel, ts string, err error) {
	log.Printf("[%s] %s: %v", eid, msg, err)
	react(client, false, "thinking_face", channel, ts)
	react(client, true, "usererror", channel, ts)
}

// fetchThreadContext retrieves all messages in a thread and formats them for AI context.
// Files from earlier messages are downloaded and their text content is included inline,
// so the AI has access to previously shared documents without reprocessing them separately.
// Files from currentTS are skipped since they are handled separately by the caller.
func fetchThreadContext(client *slack.Client, botToken, channel, threadTS, currentTS string) (string, error) {
	msgs, _, _, err := client.GetConversationReplies(&slack.GetConversationRepliesParameters{
		ChannelID: channel,
		Timestamp: threadTS,
	})
	if err != nil {
		return "", fmt.Errorf("conversations.replies: %w", err)
	}

	// Find the most recent prior message that has files — only that one is downloaded.
	// Earlier file messages are noted by name only to keep context concise.
	latestFileTS := ""
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Timestamp != currentTS && len(msgs[i].Files) > 0 {
			latestFileTS = msgs[i].Timestamp
			break
		}
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

		if m.Timestamp == currentTS || len(m.Files) == 0 {
			continue
		}

		if m.Timestamp != latestFileTS {
			// Older file messages: just note their names, don't download.
			for _, f := range m.Files {
				sb.WriteString(fmt.Sprintf("[Earlier attached file: %s]\n", f.Name))
			}
			continue
		}

		// Latest file message: download and inline content.
		attachments := extractFiles(botToken, m.Files)
		for _, f := range attachments {
			sb.WriteString(fileToInlineText(f))
		}
	}
	return sb.String(), nil
}

func handleAppMention(client *slack.Client, ai *AIClient, botToken string, event *slackevents.AppMentionEvent) {
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

	log.Printf("[%s] mention from user=%s channel=%s:%s — forwarding to AI", eid, event.User, event.Channel, threadTS)

	react(client, true, "thinking_face", event.Channel, event.TimeStamp)

	// Fetch files attached to this message (AppMentionEvent doesn't include them directly)
	var files []FileAttachment
	slackFiles, fetchErr := fetchMessageFiles(client, event.Channel, event.TimeStamp, event.ThreadTimeStamp)
	if fetchErr != nil {
		logVerbose("[%s] failed to fetch message files: %v", eid, fetchErr)
	} else if len(slackFiles) > 0 {
		files = extractFiles(botToken, slackFiles)
		log.Printf("[%s] extracted %d file(s) from mention", eid, len(files))
	}

	var (
		reply string
		err   error
	)
	if event.ThreadTimeStamp != "" {
		threadContext, fetchErr := fetchThreadContext(client, botToken, event.Channel, threadTS, event.TimeStamp)
		if fetchErr != nil {
			logVerbose("[%s] failed to fetch thread context: %v", eid, fetchErr)
			if len(files) > 0 {
				reply, err = ai.ChatWithFiles(context.Background(), text, files)
			} else {
				reply, err = ai.Chat(context.Background(), text)
			}
		} else {
			if len(files) > 0 {
				reply, err = ai.ChatWithThreadAndFiles(context.Background(), text, threadContext, files)
			} else {
				reply, err = ai.ChatWithThread(context.Background(), text, threadContext)
			}
		}
	} else {
		if len(files) > 0 {
			reply, err = ai.ChatWithFiles(context.Background(), text, files)
		} else {
			reply, err = ai.Chat(context.Background(), text)
		}
	}

	if err != nil {
		reactError(client, eid, "error from AI", event.Channel, event.TimeStamp, err)
		return
	}

	if _, _, postErr := client.PostMessage(
		event.Channel,
		slack.MsgOptionText(reply, false),
		slack.MsgOptionTS(threadTS),
	); postErr != nil {
		reactError(client, eid, "failed to post reply", event.Channel, event.TimeStamp, postErr)
		return
	}

	react(client, false, "thinking_face", event.Channel, event.TimeStamp)
	react(client, true, "white_check_mark", event.Channel, event.TimeStamp)

	log.Printf("[%s] answered", eid)
}

func handleDM(client *slack.Client, ai *AIClient, botToken string, event *slackevents.MessageEvent) {
	eid := nextEventID()

	if event.BotID != "" {
		return
	}
	// Allow "file_share" subtype (user uploaded a file); skip other subtypes.
	if event.SubType != "" && event.SubType != "file_share" {
		return
	}

	text := strings.TrimSpace(event.Text)

	// Extract files from the message
	var files []FileAttachment
	if event.Message != nil && len(event.Message.Files) > 0 {
		files = extractFiles(botToken, event.Message.Files)
		log.Printf("[%s] extracted %d file(s) from DM", eid, len(files))
	}

	// If there's no text and no files, nothing to do
	if text == "" && len(files) == 0 {
		return
	}
	// Default prompt when user sends only files without text
	if text == "" {
		text = "Please analyze the attached file(s)."
	}

	threadTS := event.ThreadTimeStamp
	if threadTS == "" {
		threadTS = event.TimeStamp
	}

	log.Printf("[%s] DM from user=%s — forwarding to AI", eid, event.User)

	react(client, true, "thinking_face", event.Channel, event.TimeStamp)

	var reply string
	var err error
	if event.ThreadTimeStamp != "" {
		threadContext, fetchErr := fetchThreadContext(client, botToken, event.Channel, threadTS, event.TimeStamp)
		if fetchErr != nil {
			logVerbose("[%s] failed to fetch DM thread context: %v", eid, fetchErr)
			if len(files) > 0 {
				reply, err = ai.ChatWithFiles(context.Background(), text, files)
			} else {
				reply, err = ai.Chat(context.Background(), text)
			}
		} else {
			if len(files) > 0 {
				reply, err = ai.ChatWithThreadAndFiles(context.Background(), text, threadContext, files)
			} else {
				reply, err = ai.ChatWithThread(context.Background(), text, threadContext)
			}
		}
	} else {
		if len(files) > 0 {
			reply, err = ai.ChatWithFiles(context.Background(), text, files)
		} else {
			reply, err = ai.Chat(context.Background(), text)
		}
	}
	if err != nil {
		reactError(client, eid, "error from AI", event.Channel, event.TimeStamp, err)
		return
	}

	if _, _, postErr := client.PostMessage(
		event.Channel,
		slack.MsgOptionText(reply, false),
		slack.MsgOptionTS(threadTS),
	); postErr != nil {
		reactError(client, eid, "failed to post reply", event.Channel, event.TimeStamp, postErr)
		return
	}

	react(client, false, "thinking_face", event.Channel, event.TimeStamp)
	react(client, true, "white_check_mark", event.Channel, event.TimeStamp)

	log.Printf("[%s] answered", eid)
}

func handleEvents(client *slack.Client, ai *AIClient, botToken string, socketClient *socketmode.Client) {
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
				go handleAppMention(client, ai, botToken, ev)
			case *slackevents.MessageEvent:
				logVerbose("message event: channel=%s subtype=%q user=%q threadTS=%q",
					ev.Channel, ev.SubType, ev.User, ev.ThreadTimeStamp)
				if strings.HasPrefix(ev.Channel, "D") {
					go handleDM(client, ai, botToken, ev)
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
