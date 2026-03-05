package extract

import (
	"bytes"
	"fmt"
	"image/jpeg"

	"github.com/adrium/goheif"
)

func isHEICMimeType(mime string) bool {
	return mime == "image/heic" || mime == "image/heif"
}

// convertHEICToJPEG decodes a HEIC/HEIF file and re-encodes it as JPEG.
func convertHEICToJPEG(data []byte) ([]byte, error) {
	img, err := goheif.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decode heic: %w", err)
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		return nil, fmt.Errorf("encode jpeg: %w", err)
	}
	return buf.Bytes(), nil
}
