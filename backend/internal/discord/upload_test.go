package discord

import (
	"encoding/base64"
	"testing"
)

func TestDecodeTranscriptAcceptsPasteAndText(t *testing.T) {
	text := "FAIRSHARE TRANSCRIPT 1\nraid=1\nsnapshot=x\nplayer=Alice\nversion=test"
	if got, err := decodeTranscript(text); err != nil || got != text {
		t.Fatalf("raw text: %q %v", got, err)
	}
	paste := base64.StdEncoding.EncodeToString([]byte(text))
	wrapped := paste[:10] + "\n" + paste[10:20] + " " + paste[20:]
	if got, err := decodeTranscript(wrapped); err != nil || got != text {
		t.Fatalf("paste with whitespace: %q %v", got, err)
	}
	if _, err := decodeTranscript("not base64 at all!!"); err == nil {
		t.Fatal("junk accepted")
	}
	if _, err := decodeTranscript(base64.StdEncoding.EncodeToString([]byte("hello"))); err == nil {
		t.Fatal("non-transcript accepted")
	}
}
