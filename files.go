package main

import (
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/openai/openai-go"
	"github.com/slack-go/slack"
)

// FileAttachment holds downloaded file data from Slack.
type FileAttachment struct {
	Name     string
	MimeType string
	Data     []byte
}

// maxFileSize is the maximum file size we'll download (20 MB).
const maxFileSize = 20 * 1024 * 1024

// downloadSlackFile downloads a file from Slack using the bot token for auth.
func downloadSlackFile(botToken, url string) ([]byte, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+botToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download: HTTP %d", resp.StatusCode)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxFileSize+1))
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	if len(data) > maxFileSize {
		return nil, fmt.Errorf("file too large (>%d bytes)", maxFileSize)
	}
	return data, nil
}

// extractFiles downloads Slack files and returns them as FileAttachments.
func extractFiles(botToken string, files []slack.File) []FileAttachment {
	var attachments []FileAttachment
	for _, f := range files {
		url := f.URLPrivateDownload
		if url == "" {
			url = f.URLPrivate
		}
		if url == "" {
			logVerbose("skipping file %s: no download URL", f.ID)
			continue
		}

		data, err := downloadSlackFile(botToken, url)
		if err != nil {
			log.Printf("failed to download file %s (%s): %v", f.ID, f.Name, err)
			continue
		}

		attachments = append(attachments, FileAttachment{
			Name:     f.Name,
			MimeType: f.Mimetype,
			Data:     data,
		})
		logVerbose("downloaded file %s (%s, %d bytes, %s)", f.ID, f.Name, len(data), f.Mimetype)
	}
	return attachments
}

// fetchMessageFiles retrieves files from a specific Slack message using the conversations API.
// This is needed for AppMentionEvent which doesn't include file data directly.
func fetchMessageFiles(client *slack.Client, channel, ts, threadTS string) ([]slack.File, error) {
	lookupTS := threadTS
	if lookupTS == "" {
		lookupTS = ts
	}
	msgs, _, _, err := client.GetConversationReplies(&slack.GetConversationRepliesParameters{
		ChannelID: channel,
		Timestamp: lookupTS,
	})
	if err != nil {
		return nil, fmt.Errorf("conversations.replies: %w", err)
	}
	for _, m := range msgs {
		if m.Timestamp == ts {
			return m.Files, nil
		}
	}
	return nil, nil
}

// isImageMimeType returns true if the MIME type is an image type.
func isImageMimeType(mime string) bool {
	return strings.HasPrefix(mime, "image/")
}

// isTextLikeMimeType returns true for MIME types that should be sent as inline text.
func isTextLikeMimeType(mime string) bool {
	if strings.HasPrefix(mime, "text/") {
		return true
	}
	textTypes := []string{
		"application/json",
		"application/xml",
		"application/javascript",
		"application/x-yaml",
		"application/yaml",
		"application/toml",
		"application/x-sh",
	}
	for _, t := range textTypes {
		if mime == t {
			return true
		}
	}
	return false
}

// filesToContentParts converts FileAttachments to openai content parts.
// Images become ImageContentPart with base64 data URLs.
// Text-like files become TextContentPart with inline content.
// Office documents (.pptx, .docx, .xlsx) have text extracted and sent inline.
// Other binary files are noted as text so the AI knows they were attached.
func filesToContentParts(files []FileAttachment) []openai.ChatCompletionContentPartUnionParam {
	var parts []openai.ChatCompletionContentPartUnionParam
	for _, f := range files {
		if isImageMimeType(f.MimeType) {
			b64 := base64.StdEncoding.EncodeToString(f.Data)
			dataURL := fmt.Sprintf("data:%s;base64,%s", f.MimeType, b64)
			parts = append(parts, openai.ImageContentPart(openai.ChatCompletionContentPartImageImageURLParam{
				URL: dataURL,
			}))
		} else if isTextLikeMimeType(f.MimeType) {
			content := fmt.Sprintf("[File: %s]\n%s", f.Name, string(f.Data))
			parts = append(parts, openai.TextContentPart(content))
		} else if isOfficeDocMimeType(f.MimeType) {
			text, err := extractTextFromOfficeDoc(f.Data, f.MimeType)
			if err != nil {
				log.Printf("failed to extract text from %s: %v", f.Name, err)
				note := fmt.Sprintf("[Attached file: %s (type: %s, %d bytes) — could not extract text]",
					f.Name, f.MimeType, len(f.Data))
				parts = append(parts, openai.TextContentPart(note))
			} else {
				content := fmt.Sprintf("[File: %s]\n%s", f.Name, text)
				parts = append(parts, openai.TextContentPart(content))
				logVerbose("extracted %d chars of text from %s", len(text), f.Name)
			}
		} else {
			// Truly unsupported binary files.
			note := fmt.Sprintf("[Attached file: %s (type: %s, %d bytes) — binary file contents cannot be processed directly]",
				f.Name, f.MimeType, len(f.Data))
			parts = append(parts, openai.TextContentPart(note))
			log.Printf("unsupported file type for AI: %s (%s)", f.Name, f.MimeType)
		}
	}
	return parts
}
