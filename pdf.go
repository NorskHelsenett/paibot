package main

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/ledongthuc/pdf"
)

// isPDFMimeType returns true if the MIME type is a PDF.
func isPDFMimeType(mime string) bool {
	return mime == "application/pdf"
}

// extractTextFromPDF extracts plain text content from a PDF file.
func extractTextFromPDF(data []byte) (string, error) {
	r, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("open pdf: %w", err)
	}

	var sb strings.Builder
	numPages := r.NumPage()
	for i := 1; i <= numPages; i++ {
		p := r.Page(i)
		if p.V.IsNull() {
			continue
		}
		text, err := p.GetPlainText(nil)
		if err != nil {
			logVerbose("failed to extract text from PDF page %d: %v", i, err)
			continue
		}
		text = strings.TrimSpace(text)
		if text != "" {
			if sb.Len() > 0 {
				sb.WriteString("\n\n---\n\n")
			}
			if numPages > 1 {
				sb.WriteString(fmt.Sprintf("[Page %d]\n", i))
			}
			sb.WriteString(text)
		}
	}

	result := strings.TrimSpace(sb.String())
	if result == "" {
		return "", fmt.Errorf("no text content found in PDF")
	}
	return result, nil
}
