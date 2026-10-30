package personal

import (
	"testing"

	"github.com/nextlevelbuilder/goclaw/internal/channels/zalo/personal/protocol"
)

// TestRememberInboundRef_RoundTrip proves the quote wiring: an inbound
// message's wire identifiers survive into the ref the sender reads.
func TestRememberInboundRef_RoundTrip(t *testing.T) {
	c := &Channel{}
	msg := protocol.TMessage{
		MsgID:       "m1",
		GlobalMsgID: "g1",
		CliMsgID:    "c1",
		UIDFrom:     "u-sender",
		TS:          "999",
		MsgType:     "webchat",
	}
	c.rememberInboundRef("chat-1", "u-sender", "Anh Tuan", msg, "hello bot")

	ref, ok := c.loadInboundRef("chat-1")
	if !ok {
		t.Fatal("ref missing")
	}
	if ref.senderID != "u-sender" || ref.senderName != "Anh Tuan" {
		t.Errorf("sender not preserved: %+v", ref)
	}
	q := ref.quote
	if q.OwnerID != "u-sender" || q.GlobalMsgID != "g1" || q.CliMsgID != "c1" ||
		q.TS != "999" || q.Msg != "hello bot" || q.MsgType != "webchat" {
		t.Errorf("quote fields not preserved: %+v", q)
	}
	if _, ok := c.loadInboundRef("nope"); ok {
		t.Error("unknown chat must miss")
	}
}

func TestUtf16Len_ASCIIAndVietnamese(t *testing.T) {
	if utf16Len("@Tuan ") != 6 {
		t.Errorf("ascii len wrong: %d", utf16Len("@Tuan "))
	}
	// ệ is BMP (1 unit); emoji surrogate pair is 2 units.
	if utf16Len("ệ") != 1 {
		t.Errorf("BMP len wrong: %d", utf16Len("ệ"))
	}
	if utf16Len("👋") != 2 {
		t.Errorf("surrogate len wrong: %d", utf16Len("👋"))
	}
}
