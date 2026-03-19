package bot

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/jonasbg/paibot/internal/ai"
	"github.com/jonasbg/paibot/internal/extract"
	"github.com/jonasbg/paibot/internal/logutil"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
)

// Message represents an in-flight message being processed
type Message struct {
	Channel   string
	Timestamp string
}

// MessageTracker tracks messages currently being processed
var (
	inFlightMessages = make(map[string]Message)
	inFlightMutex    sync.Mutex
	handlerWg        sync.WaitGroup
)

// AddInFlightMessage adds a message to the in-flight tracking
func AddInFlightMessage(channel, ts string) {
	inFlightMutex.Lock()
	defer inFlightMutex.Unlock()
	key := channel + ":" + ts
	inFlightMessages[key] = Message{Channel: channel, Timestamp: ts}
}

// RemoveInFlightMessage removes a message from in-flight tracking
func RemoveInFlightMessage(channel, ts string) {
	inFlightMutex.Lock()
	defer inFlightMutex.Unlock()
	key := channel + ":" + ts
	delete(inFlightMessages, key)
}

// GetInFlightMessages returns a copy of all in-flight messages
func GetInFlightMessages() []Message {
	inFlightMutex.Lock()
	defer inFlightMutex.Unlock()
	messages := make([]Message, 0, len(inFlightMessages))
	for _, msg := range inFlightMessages {
		messages = append(messages, msg)
	}
	return messages
}

func nextEventID() string {
	b := make([]byte, 3)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func react(client *slack.Client, add bool, emoji, channel, ts string) {
	ref := slack.ItemRef{Channel: channel, Timestamp: ts}
	if add {
		_ = client.AddReaction(emoji, ref)
	} else {
		_ = client.RemoveReaction(emoji, ref)
	}
}

func reactError(client *slack.Client, eid, msg, channel, ts string, err error) {
	log.Printf("[%s] %s: %v", eid, msg, err)
	react(client, false, "thinking_face", channel, ts)
	react(client, true, "usererror", channel, ts)
}

// resolveUser looks up a Slack user by ID and returns "DisplayName (<@ID>)".
// Results are cached in nameCache. The botUserID is mapped to "PAI (you)".
func resolveUser(client *slack.Client, nameCache map[string]string, userID string) string {
	if userID == "" {
		return "unknown"
	}
	if label, ok := nameCache[userID]; ok {
		return label
	}
	info, err := client.GetUserInfo(userID)
	if err != nil {
		logutil.Logf("GetUserInfo(%s) failed: %v", userID, err)
		nameCache[userID] = fmt.Sprintf("<@%s>", userID)
		return nameCache[userID]
	}
	name := info.Profile.DisplayName
	if name == "" {
		name = info.RealName
	}
	if name == "" {
		name = info.Profile.RealNameNormalized
	}
	if name == "" {
		name = info.Name
	}
	if name == "" {
		nameCache[userID] = fmt.Sprintf("<@%s>", userID)
		return nameCache[userID]
	}
	nameCache[userID] = fmt.Sprintf("%s (<@%s>)", name, userID)
	return nameCache[userID]
}

// fetchThreadContext retrieves all messages in a thread and formats them for AI context.
// Files from the most recent prior message that has files are returned as FileAttachments
// so they can be forwarded to the AI as proper content parts (including images).
// Older file messages are noted by name only. Files from currentTS are skipped (handled by caller).
func fetchThreadContext(client *slack.Client, botToken, botUserID, channel, threadTS, currentTS string) (string, []extract.FileAttachment, error) {
	msgs, _, _, err := client.GetConversationReplies(&slack.GetConversationRepliesParameters{
		ChannelID: channel,
		Timestamp: threadTS,
	})
	if err != nil {
		return "", nil, fmt.Errorf("conversations.replies: %w", err)
	}

	// Find the most recent prior message that has files — only that one is downloaded.
	latestFileTS := ""
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Timestamp != currentTS && len(msgs[i].Files) > 0 {
			latestFileTS = msgs[i].Timestamp
			break
		}
	}

	nameCache := map[string]string{
		botUserID: "PAI (you)",
	}

	// First pass: resolve all participants so we can build a directory.
	for _, m := range msgs {
		uid := m.User
		if uid == "" {
			uid = m.BotID
		}
		resolveUser(client, nameCache, uid)
	}

	var sb strings.Builder

	// Participants directory so the AI knows who is who and can tag them.
	sb.WriteString("Participants in this thread:\n")
	for uid, label := range nameCache {
		if uid == botUserID {
			continue
		}
		sb.WriteString(fmt.Sprintf("- %s\n", label))
	}
	sb.WriteString("\nConversation:\n")

	var threadFiles []extract.FileAttachment
	for _, m := range msgs {
		user := m.User
		if user == "" {
			user = m.BotID
		}
		label := resolveUser(client, nameCache, user)
		sb.WriteString(fmt.Sprintf("%s: %s\n", label, m.Text))

		if m.Timestamp == currentTS || len(m.Files) == 0 {
			continue
		}

		if m.Timestamp != latestFileTS {
			for _, f := range m.Files {
				sb.WriteString(fmt.Sprintf("[Earlier attached file: %s]\n", f.Name))
			}
			continue
		}

		// Download the latest file message and return as FileAttachments so the AI
		// receives proper content parts (images as ImageContentPart, etc.).
		threadFiles = extract.ExtractFiles(botToken, m.Files)
		for _, f := range threadFiles {
			sb.WriteString(fmt.Sprintf("[Attached file: %s]\n", f.Name))
		}
	}
	return sb.String(), threadFiles, nil
}

func handleAppMention(client *slack.Client, aiClient *ai.Client, botToken, botUserID string, event *slackevents.AppMentionEvent) {
	eid := nextEventID()

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
	AddInFlightMessage(event.Channel, event.TimeStamp)
	defer RemoveInFlightMessage(event.Channel, event.TimeStamp)

	var files []extract.FileAttachment
	slackFiles, fetchErr := extract.FetchMessageFiles(client, event.Channel, event.TimeStamp, event.ThreadTimeStamp)
	if fetchErr != nil {
		logutil.Logf("[%s] failed to fetch message files: %v", eid, fetchErr)
	} else if len(slackFiles) > 0 {
		files = extract.ExtractFiles(botToken, slackFiles)
		log.Printf("[%s] extracted %d file(s) from mention", eid, len(files))
	}

	reply, err := callAI(aiClient, client, botToken, botUserID, eid, event.Channel, threadTS, event.TimeStamp, event.ThreadTimeStamp != "", event.User, text, files)
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

func handleDM(client *slack.Client, aiClient *ai.Client, botToken, botUserID string, event *slackevents.MessageEvent) {
	eid := nextEventID()

	if event.BotID != "" {
		return
	}
	if event.SubType != "" && event.SubType != "file_share" {
		return
	}

	text := strings.TrimSpace(event.Text)

	var files []extract.FileAttachment
	if event.Message != nil && len(event.Message.Files) > 0 {
		files = extract.ExtractFiles(botToken, event.Message.Files)
		log.Printf("[%s] extracted %d file(s) from DM", eid, len(files))
	}

	if text == "" && len(files) == 0 {
		return
	}
	if text == "" {
		text = "Please analyze the attached file(s)."
	}

	threadTS := event.ThreadTimeStamp
	if threadTS == "" {
		threadTS = event.TimeStamp
	}

	log.Printf("[%s] DM from user=%s — forwarding to AI", eid, event.User)
	react(client, true, "thinking_face", event.Channel, event.TimeStamp)
	AddInFlightMessage(event.Channel, event.TimeStamp)
	defer RemoveInFlightMessage(event.Channel, event.TimeStamp)

	reply, err := callAI(aiClient, client, botToken, botUserID, eid, event.Channel, threadTS, event.TimeStamp, event.ThreadTimeStamp != "", event.User, text, files)
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

// callAI dispatches to the right AI method based on whether we have a thread and/or files.
// It also handles the fallback if fetching thread context fails.
func callAI(aiClient *ai.Client, slackClient *slack.Client, botToken, botUserID, eid, channel, threadTS, currentTS string, inThread bool, callerUserID, text string, files []extract.FileAttachment) (string, error) {
	ctx := context.Background()

	// Resolve the caller so the AI knows who is asking and can tag them.
	nameCache := map[string]string{botUserID: "PAI (you)"}
	callerLabel := resolveUser(slackClient, nameCache, callerUserID)

	if !inThread {
		taggedText := fmt.Sprintf("[From: %s]\n%s", callerLabel, text)
		if len(files) > 0 {
			return aiClient.ChatWithFiles(ctx, taggedText, files)
		}
		return aiClient.Chat(ctx, taggedText)
	}

	threadContext, threadFiles, err := fetchThreadContext(slackClient, botToken, botUserID, channel, threadTS, currentTS)
	if err != nil {
		logutil.Logf("[%s] failed to fetch thread context: %v", eid, err)
		if len(files) > 0 {
			return aiClient.ChatWithFiles(ctx, text, files)
		}
		return aiClient.Chat(ctx, text)
	}

	// Merge files from the current message with the latest file from the thread,
	// so follow-up questions always forward the last uploaded file to the AI.
	allFiles := append(files, threadFiles...)
	if len(allFiles) > 0 {
		if len(allFiles) > len(files) {
			log.Printf("[%s] forwarding %d thread file(s) to AI for follow-up", eid, len(threadFiles))
		}
		return aiClient.ChatWithThreadAndFiles(ctx, text, threadContext, allFiles)
	}
	return aiClient.ChatWithThread(ctx, text, threadContext)
}

// WaitForHandlers blocks until all in-progress handler goroutines have finished.
func WaitForHandlers() {
	handlerWg.Wait()
}

// MarkInFlightAsError marks all in-flight messages with a skull emoji
// This is called when the bot is shutting down gracefully
func MarkInFlightAsError(client *slack.Client) {
	messages := GetInFlightMessages()
	if len(messages) == 0 {
		log.Println("No in-flight messages to mark as error")
		return
	}

	log.Printf("Marking %d in-flight message(s) as error", len(messages))
	for _, msg := range messages {
		react(client, false, "thinking_face", msg.Channel, msg.Timestamp)
		react(client, true, "skull", msg.Channel, msg.Timestamp)
		log.Printf("Marked message %s:%s as error", msg.Channel, msg.Timestamp)
	}
}

// HandleEvents is the main event loop consuming socket mode events.
func HandleEvents(client *slack.Client, aiClient *ai.Client, botToken, botUserID string, socketClient *socketmode.Client) {
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
			logutil.Logf("Event received: type=%s inner=%s", eventsAPIEvent.Type, eventsAPIEvent.InnerEvent.Type)

			if eventsAPIEvent.Type != slackevents.CallbackEvent {
				continue
			}

			switch ev := eventsAPIEvent.InnerEvent.Data.(type) {
			case *slackevents.AppMentionEvent:
				handlerWg.Add(1)
				go func() {
					defer handlerWg.Done()
					handleAppMention(client, aiClient, botToken, botUserID, ev)
				}()
			case *slackevents.MessageEvent:
				logutil.Logf("message event: channel=%s subtype=%q user=%q threadTS=%q",
					ev.Channel, ev.SubType, ev.User, ev.ThreadTimeStamp)
				if strings.HasPrefix(ev.Channel, "D") {
					handlerWg.Add(1)
					go func() {
						defer handlerWg.Done()
						handleDM(client, aiClient, botToken, botUserID, ev)
					}()
				}
			default:
				logutil.Logf("unhandled inner event type: %T", eventsAPIEvent.InnerEvent.Data)
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
			logutil.Logf("Interactive event: type=%s", callback.Type)
			socketClient.Ack(*evt.Request)

		default:
			logutil.Logf("Unhandled event type: %s", evt.Type)
		}
	}
}
