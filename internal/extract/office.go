package extract

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/jonasbg/paibot/internal/logutil"
)

func isOfficeDocMimeType(mime string) bool {
	switch mime {
	case "application/vnd.openxmlformats-officedocument.presentationml.presentation", // .pptx
		"application/vnd.openxmlformats-officedocument.wordprocessingml.document",  // .docx
		"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":        // .xlsx
		return true
	}
	return false
}

// slideNumberRe matches ppt/slides/slide<N>.xml paths.
var slideNumberRe = regexp.MustCompile(`ppt/slides/slide(\d+)\.xml`)

// extractTextFromOfficeDoc extracts plain text from .pptx, .docx, or .xlsx files.
func extractTextFromOfficeDoc(data []byte, mime string) (string, error) {
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("open zip: %w", err)
	}

	var xmlPaths []string
	switch {
	case strings.Contains(mime, "presentationml"): // .pptx
		for _, f := range r.File {
			if slideNumberRe.MatchString(f.Name) {
				xmlPaths = append(xmlPaths, f.Name)
			}
		}
		sort.Strings(xmlPaths)
	case strings.Contains(mime, "wordprocessingml"): // .docx
		xmlPaths = []string{"word/document.xml"}
	case strings.Contains(mime, "spreadsheetml"): // .xlsx
		xmlPaths = []string{"xl/sharedStrings.xml"}
	}

	var sb strings.Builder
	for _, path := range xmlPaths {
		text, err := extractXMLText(r, path)
		if err != nil {
			logutil.Logf("failed to extract text from %s: %v", path, err)
			continue
		}
		if text != "" {
			if sb.Len() > 0 {
				sb.WriteString("\n\n---\n\n")
			}
			if strings.Contains(mime, "presentationml") {
				if m := slideNumberRe.FindStringSubmatch(path); len(m) > 1 {
					sb.WriteString(fmt.Sprintf("[Slide %s]\n", m[1]))
				}
			}
			sb.WriteString(text)
		}
	}

	result := strings.TrimSpace(sb.String())
	if result == "" {
		return "", fmt.Errorf("no text content found")
	}
	return result, nil
}

func extractXMLText(r *zip.Reader, path string) (string, error) {
	for _, f := range r.File {
		if f.Name != path {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return "", err
		}
		defer rc.Close()

		var sb strings.Builder
		decoder := xml.NewDecoder(rc)
		for {
			tok, err := decoder.Token()
			if err != nil {
				break
			}
			if charData, ok := tok.(xml.CharData); ok {
				text := strings.TrimSpace(string(charData))
				if text != "" {
					if sb.Len() > 0 {
						sb.WriteString(" ")
					}
					sb.WriteString(text)
				}
			}
		}
		return sb.String(), nil
	}
	return "", fmt.Errorf("%s not found in archive", path)
}
