package protocol

import (
	"testing"
)

func TestClientMessageType_Mapping(t *testing.T) {
	cases := map[string]int{
		"webchat":           1,
		"chat.voice":        31,
		"chat.photo":        32,
		"chat.sticker":      36,
		"chat.doodle":       37,
		"chat.link":         38,
		"chat.recommended":  38,
		"chat.location.new": 43,
		"chat.video.msg":    44,
		"share.file":        46,
		"chat.gif":          49,
		"something-new":     1,
		"":                  1,
	}
	for in, want := range cases {
		if got := clientMessageType(in); got != want {
			t.Errorf("clientMessageType(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestQuoteRef_Fallbacks(t *testing.T) {
	q := &QuoteRef{OwnerID: "u1", MsgID: "m-local", TS: "123", Msg: "hi"}
	if q.quoteMsgID() != "m-local" {
		t.Errorf("fallback to MsgID failed: %q", q.quoteMsgID())
	}
	if q.quoteMsgType() != 1 {
		t.Errorf("default type must be 1, got %d", q.quoteMsgType())
	}
	q2 := &QuoteRef{GlobalMsgID: "g-9", MsgID: "m-local", CliMsgType: 32, MsgType: "webchat"}
	if q2.quoteMsgID() != "g-9" {
		t.Errorf("server id must win: %q", q2.quoteMsgID())
	}
	if q2.quoteMsgType() != 32 {
		t.Errorf("explicit CliMsgType must win: %d", q2.quoteMsgType())
	}
}

func TestSendMessageWithQuote_RequiresQuote(t *testing.T) {
	if _, err := SendMessageWithQuote(nil, nil, "c", ThreadTypeUser, "hi", nil, nil); err == nil {
		t.Error("nil quote must error before any network use")
	}
	if _, err := SendMessageWithQuote(nil, nil, "c", ThreadTypeUser, "", &QuoteRef{}, nil); err == nil {
		t.Error("empty text must error before any network use")
	}
}
