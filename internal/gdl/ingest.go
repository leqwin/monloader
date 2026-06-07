package gdl

import (
	"path/filepath"
	"strings"
)

// ingestableExts are the file types monbooru accepts; anything else a download
// path produces (audio, a document, a vector image) is skipped rather than
// pushed and rejected.
var ingestableExts = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".webp": true,
	".gif": true, ".mp4": true, ".webm": true, ".cbz": true,
}

// Ingestable reports whether monbooru accepts the file at path, keyed on its
// extension.
func Ingestable(path string) bool {
	return ingestableExts[strings.ToLower(filepath.Ext(path))]
}

// ingestableMedia narrows mediaExtension to the types monbooru can ingest, so a
// bare media URL of an unsupported type (an avif, an svg) is declined up front
// rather than downloaded and bounced off the push.
func ingestableMedia(contentType string) (string, bool) {
	ext, ok := mediaExtension(contentType)
	if !ok || !ingestableExts["."+ext] {
		return "", false
	}
	return ext, true
}
