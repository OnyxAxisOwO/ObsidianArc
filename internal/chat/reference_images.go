package chat

import (
	"encoding/base64"
	"errors"
	"strings"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/adapter"
)

// ReferenceError is a reference picture turned away, worded for whoever sent
// it. Each endpoint answers it in its own error shape; the sentence is shared
// so the panel's endpoint and /v1 cannot drift in what they tell a caller.
type ReferenceError struct{ Message string }

func (e *ReferenceError) Error() string { return e.Message }

// ReferencePicture checks one reference picture's bytes exactly like a picture
// that arrived from a provider: what a client calls a file has no more standing
// than what an upstream says it sent.
func ReferencePicture(data []byte, ceiling int64) (adapter.ImagePart, error) {
	checked, mime, err := checkImage(data, ceiling)
	if err != nil {
		if errors.Is(err, errImageSize) {
			return adapter.ImagePart{}, &ReferenceError{"The reference image is larger than this instance allows."}
		}
		return adapter.ImagePart{}, &ReferenceError{"The reference image is not an image this instance accepts."}
	}
	return adapter.ImagePart{Data: checked, Mime: mime}, nil
}

// DecodeReferencePictures decodes base64 reference pictures and checks each
// one. Blank entries are skipped rather than refused, as they always were.
//
// The base64 is bare: the panel's contract has never carried a data: prefix
// (web/src/api/images.ts says so), so a caller that tolerates one strips it
// before calling this.
func DecodeReferencePictures(raws []string, ceiling int64) ([]adapter.ImagePart, error) {
	var parts []adapter.ImagePart
	for _, raw := range raws {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		data, err := base64.StdEncoding.DecodeString(raw)
		if err != nil {
			return nil, &ReferenceError{"The reference image could not be read."}
		}
		part, err := ReferencePicture(data, ceiling)
		if err != nil {
			return nil, err
		}
		parts = append(parts, part)
	}
	return parts, nil
}
