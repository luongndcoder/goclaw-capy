package personal

import (
	"fmt"
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

	ref, ok := c.loadInboundRef("chat-1", "m1")
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
	if _, ok := c.loadInboundRef("nope", "m1"); ok {
		t.Error("unknown chat must miss")
	}
	if _, ok := c.loadInboundRef("chat-1", "nope"); ok {
		t.Error("unknown msgID must miss")
	}
	if _, ok := c.loadInboundRef("", ""); ok {
		t.Error("empty keys must miss")
	}
}

// TestInboundRef_ConcurrentMentions proves that when multiple users tag the bot
// in the same group, their inbound references do not overwrite each other.
func TestInboundRef_ConcurrentMentions(t *testing.T) {
	c := &Channel{}
	// Anh Hiển (M1) and Chị Bông (M3) tag bot concurrently in the same group.
	msg1 := protocol.TMessage{MsgID: "M1", UIDFrom: "u-hien"}
	msg3 := protocol.TMessage{MsgID: "M3", UIDFrom: "u-bong"}

	c.rememberInboundRef("group-1", "u-hien", "Anh Hiển", msg1, "@Báo Sư Đệ ăn c k")
	c.rememberInboundRef("group-1", "u-bong", "Chị Bông", msg3, "@Báo Sư Đệ giặt đồ")

	ref1, ok1 := c.loadInboundRef("group-1", "M1")
	if !ok1 {
		t.Fatal("ref1 missing")
	}
	if ref1.senderName != "Anh Hiển" || ref1.senderID != "u-hien" {
		t.Fatalf("ref1 wrong sender: %+v", ref1)
	}

	ref3, ok3 := c.loadInboundRef("group-1", "M3")
	if !ok3 {
		t.Fatal("ref3 missing")
	}
	if ref3.senderName != "Chị Bông" || ref3.senderID != "u-bong" {
		t.Fatalf("ref3 wrong sender: %+v", ref3)
	}
}

// TestResolveReplyMsgID verifies that reply_to_message_id is preferred over
// zalo_reply_msg_id, and missing/proactive metadata returns an empty string.
func TestResolveReplyMsgID(t *testing.T) {
	// 1. Both present: reply_to_message_id takes priority
	meta1 := map[string]string{
		"reply_to_message_id": "mid-from-normal-consumer",
		"zalo_reply_msg_id":   "mid-from-zalo-handler",
	}
	if got := resolveReplyMsgID(meta1); got != "mid-from-normal-consumer" {
		t.Errorf("got %q, want %q", got, "mid-from-normal-consumer")
	}

	// 2. Only zalo_reply_msg_id present (e.g. DM flow without isGroup)
	meta2 := map[string]string{
		"zalo_reply_msg_id": "mid-from-zalo-handler",
	}
	if got := resolveReplyMsgID(meta2); got != "mid-from-zalo-handler" {
		t.Errorf("got %q, want %q", got, "mid-from-zalo-handler")
	}

	// 3. Proactive / Cron / Followup split (none present)
	meta3 := map[string]string{
		"group_id": "group-1",
	}
	if got := resolveReplyMsgID(meta3); got != "" {
		t.Errorf("got %q, want empty for proactive/cron", got)
	}

	// 4. Nil map
	if got := resolveReplyMsgID(nil); got != "" {
		t.Errorf("got %q, want empty for nil meta", got)
	}
}

// TestInboundRefStore_BoundedEviction verifies that the store does not grow
// without bound: oldest entries are FIFO-evicted when capacity is exceeded.
func TestInboundRefStore_BoundedEviction(t *testing.T) {
	const maxRefs = 5
	store := newInboundRefStore(maxRefs)

	for i := 1; i <= 10; i++ {
		msgID := fmt.Sprintf("msg-%d", i)
		store.store(inboundRefKey{chatID: "group-1", msgID: msgID}, inboundRef{
			senderID:   fmt.Sprintf("user-%d", i),
			senderName: fmt.Sprintf("User %d", i),
		})
	}

	if store.len() != maxRefs {
		t.Fatalf("store len = %d, want %d", store.len(), maxRefs)
	}

	// The first 5 messages (msg-1 to msg-5) should have been evicted.
	for i := 1; i <= 5; i++ {
		msgID := fmt.Sprintf("msg-%d", i)
		if _, ok := store.load(inboundRefKey{chatID: "group-1", msgID: msgID}); ok {
			t.Errorf("expected msg-%d to be evicted", i)
		}
	}

	// The last 5 messages (msg-6 to msg-10) should be retained.
	for i := 6; i <= 10; i++ {
		msgID := fmt.Sprintf("msg-%d", i)
		ref, ok := store.load(inboundRefKey{chatID: "group-1", msgID: msgID})
		if !ok {
			t.Errorf("expected msg-%d to be present", i)
		} else if ref.senderID != fmt.Sprintf("user-%d", i) {
			t.Errorf("expected user-%d, got %s", i, ref.senderID)
		}
	}
}

func TestInboundRef_EmptyKeysIgnored(t *testing.T) {
	c := &Channel{}
	// Empty MsgID
	c.rememberInboundRef("group-1", "u-1", "User 1", protocol.TMessage{MsgID: ""}, "body")
	if _, ok := c.loadInboundRef("group-1", ""); ok {
		t.Error("should not find empty msgID")
	}

	// Empty threadID
	c.rememberInboundRef("", "u-1", "User 1", protocol.TMessage{MsgID: "m1"}, "body")
	if _, ok := c.loadInboundRef("", "m1"); ok {
		t.Error("should not find empty threadID")
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
