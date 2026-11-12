package personal

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nextlevelbuilder/goclaw/internal/bus"
	"github.com/nextlevelbuilder/goclaw/internal/channels"
	"github.com/nextlevelbuilder/goclaw/internal/channels/zalo/personal/protocol"
	"github.com/nextlevelbuilder/goclaw/internal/config"
)

func TestExtractContentAndMediaWithQuote_TextQuote(t *testing.T) {
	current := textContent("@Tue Linh Pm - Cppai Task nay cung giao cho Hao luon.")
	quote := &protocol.TQuote{
		FromD: "Duc Nguyen CPP",
		Msg:   "Tao task giao cho Hao.",
	}

	got, media := extractContentAndMediaWithQuote(current, quote)
	if len(media) != 0 {
		t.Fatalf("media = %v, want none", media)
	}
	if !strings.Contains(got, "[Replying to Duc Nguyen CPP]\nTao task giao cho Hao.\n[/Replying]") {
		t.Fatalf("missing quote context: %q", got)
	}
	if !strings.Contains(got, "@Tue Linh Pm - Cppai Task nay cung giao cho Hao luon.") {
		t.Fatalf("missing current reply text: %q", got)
	}
}

func TestFormatQuoteContext_AttachmentQuote(t *testing.T) {
	var attach protocol.QuoteAttachment
	if err := attach.UnmarshalJSON([]byte(`"{\"title\":\"brief.png\",\"href\":\"https://f20-zpc.zdn.vn/jpg/abc123\"}"`)); err != nil {
		t.Fatal(err)
	}
	quote := &protocol.TQuote{
		FromD:  "Duc Nguyen CPP",
		Attach: attach,
	}

	got := formatQuoteContext(quote)
	if !strings.Contains(got, "[Replying to Duc Nguyen CPP]") {
		t.Fatalf("missing quote sender: %q", got)
	}
	if !strings.Contains(got, "[Quoted image: brief.png]") {
		t.Fatalf("missing quote attachment text: %q", got)
	}
}

func TestExtractContentAndMediaWithQuote_NestedStyleReply(t *testing.T) {
	current := textContent("@Tue Linh Pm - Cppai Task nay cung giao cho Hao luon.")
	quote := &protocol.TQuote{
		FromD: "Tue Linh Pm - Cppai",
		Msg:   "@Duong Anh Hao tao task follow Zalo reply context.",
	}

	got, media := extractContentAndMediaWithQuote(current, quote)
	if len(media) != 0 {
		t.Fatalf("media = %v, want none", media)
	}
	for _, want := range []string{
		"@Duong Anh Hao tao task follow Zalo reply context.",
		"@Tue Linh Pm - Cppai Task nay cung giao cho Hao luon.",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %q", want, got)
		}
	}
}

func TestZaloReplyContext_BuildsMultiLevelChainFromCache(t *testing.T) {
	ch := newTestChannel(t, config.ZaloPersonalConfig{DMPolicy: "open", GroupPolicy: "open"})
	threadID := "group-1"

	ch.rememberReplyContext(threadID, "Alice", protocol.TMessage{
		MsgID:    "msg-a",
		CliMsgID: "cli-a",
		UIDFrom:  "u-a",
	}, "original")
	ch.rememberReplyContext(threadID, "Bob", protocol.TMessage{
		MsgID:    "msg-b",
		CliMsgID: "cli-b",
		UIDFrom:  "u-b",
		Quote: &protocol.TQuote{
			OwnerID:     "u-a",
			CliMsgID:    "cli-a",
			GlobalMsgID: "msg-a",
		},
	}, "reply 1")
	ch.rememberReplyContext(threadID, "Carol", protocol.TMessage{
		MsgID:    "msg-c",
		CliMsgID: "cli-c",
		UIDFrom:  "u-c",
		Quote: &protocol.TQuote{
			OwnerID:     "u-b",
			CliMsgID:    "cli-b",
			GlobalMsgID: "msg-b",
		},
	}, "reply 2")

	got := ch.buildReplyContext(threadID, &protocol.TQuote{
		OwnerID:     "u-c",
		CliMsgID:    "cli-c",
		GlobalMsgID: "msg-c",
		FromD:       "Carol",
		Msg:         "fallback quote text",
	})

	for _, want := range []string{"original", "reply 1", "reply 2"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %q", want, got)
		}
	}
	assertContainsInOrder(t, got, "original", "reply 1", "reply 2")
	if strings.Contains(got, "fallback quote text") {
		t.Fatalf("used direct fallback despite cache hit: %q", got)
	}
}

func TestZaloReplyContext_KeepsUncachedOriginalQuoteAcrossDeepReplies(t *testing.T) {
	ch := newTestChannel(t, config.ZaloPersonalConfig{DMPolicy: "open", GroupPolicy: "open"})
	threadID := "direct-1"

	ch.rememberReplyContext(threadID, "Cppai", protocol.TMessage{
		MsgID:    "msg-1",
		CliMsgID: "cli-1",
		UIDFrom:  "u-1",
		Quote: &protocol.TQuote{
			OwnerID:     "agent",
			CliMsgID:    "cli-root",
			GlobalMsgID: "msg-root",
			FromD:       "Tue Linh Pm - Cppai",
			Msg:         "Chao ban! Minh la CPPAI PM.",
		},
	}, "Anh Duc Day")
	ch.rememberReplyContext(threadID, "Cppai", protocol.TMessage{
		MsgID:    "msg-2",
		CliMsgID: "cli-2",
		UIDFrom:  "u-2",
		Quote: &protocol.TQuote{
			OwnerID:     "u-1",
			CliMsgID:    "cli-1",
			GlobalMsgID: "msg-1",
		},
	}, "Em biet anh la ai khong?")
	ch.rememberReplyContext(threadID, "Cppai", protocol.TMessage{
		MsgID:    "msg-3",
		CliMsgID: "cli-3",
		UIDFrom:  "u-3",
		Quote: &protocol.TQuote{
			OwnerID:     "u-2",
			CliMsgID:    "cli-2",
			GlobalMsgID: "msg-2",
		},
	}, "Chac chua?")
	ch.rememberReplyContext(threadID, "Cppai", protocol.TMessage{
		MsgID:    "msg-4",
		CliMsgID: "cli-4",
		UIDFrom:  "u-4",
		Quote: &protocol.TQuote{
			OwnerID:     "u-3",
			CliMsgID:    "cli-3",
			GlobalMsgID: "msg-3",
		},
	}, "That?")
	ch.rememberReplyContext(threadID, "Cppai", protocol.TMessage{
		MsgID:    "msg-5",
		CliMsgID: "cli-5",
		UIDFrom:  "u-5",
		Quote: &protocol.TQuote{
			OwnerID:     "u-4",
			CliMsgID:    "cli-4",
			GlobalMsgID: "msg-4",
		},
	}, "Reply 5")
	ch.rememberReplyContext(threadID, "Cppai", protocol.TMessage{
		MsgID:    "msg-6",
		CliMsgID: "cli-6",
		UIDFrom:  "u-6",
		Quote: &protocol.TQuote{
			OwnerID:     "u-5",
			CliMsgID:    "cli-5",
			GlobalMsgID: "msg-5",
		},
	}, "Reply 6")

	got := ch.buildReplyContext(threadID, &protocol.TQuote{
		OwnerID:     "u-6",
		CliMsgID:    "cli-6",
		GlobalMsgID: "msg-6",
		FromD:       "Cppai",
		Msg:         "direct fallback",
	})

	for _, want := range []string{
		"Chao ban! Minh la CPPAI PM.",
		"Anh Duc Day",
		"Em biet anh la ai khong?",
		"Chac chua?",
		"That?",
		"Reply 5",
		"Reply 6",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %q", want, got)
		}
	}
	assertContainsInOrder(t, got,
		"Chao ban! Minh la CPPAI PM.",
		"Anh Duc Day",
		"Em biet anh la ai khong?",
		"Chac chua?",
		"That?",
		"Reply 5",
		"Reply 6",
	)
	if strings.Contains(got, "direct fallback") {
		t.Fatalf("used direct fallback despite cache hit: %q", got)
	}
}

func TestHandleDM_RejectedMessageDoesNotDownloadAttachment(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte("image bytes"))
	}))
	defer server.Close()

	msgBus := bus.New()
	ch, err := New(config.ZaloPersonalConfig{
		AllowFrom: config.FlexibleStringSlice{"allowed-user"},
		DMPolicy:  "allowlist",
	}, msgBus, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	ch.handleDM(protocol.NewUserMessage("bot", protocol.TMessage{
		MsgID:   "denied-attachment",
		UIDFrom: "denied-user",
		IDTo:    "bot",
		Content: attachmentContent(t, server.URL+"/secret.png"),
	}))

	if got := hits.Load(); got != 0 {
		t.Fatalf("attachment endpoint hit %d times, want 0", got)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if got, ok := msgBus.ConsumeInbound(ctx); ok {
		t.Fatalf("unexpected inbound for rejected message: %+v", got)
	}
}

func textContent(text string) protocol.Content {
	return protocol.Content{String: &text}
}

func attachmentContent(t *testing.T, href string) protocol.Content {
	t.Helper()
	raw := `{"title":"secret.png","href":` + strconvQuote(href) + `}`
	var content protocol.Content
	if err := json.Unmarshal([]byte(raw), &content); err != nil {
		t.Fatal(err)
	}
	return content
}

func newTestChannel(t *testing.T, cfg config.ZaloPersonalConfig) *Channel {
	t.Helper()
	ch, err := New(cfg, bus.New(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	return ch
}

func assertContainsInOrder(t *testing.T, text string, values ...string) {
	t.Helper()
	last := -1
	for _, value := range values {
		idx := strings.Index(text, value)
		if idx < 0 {
			t.Fatalf("missing %q in %q", value, text)
		}
		if idx <= last {
			t.Fatalf("%q appears out of order in %q", value, text)
		}
		last = idx
	}
}

func strconvQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func TestHandleGroupMessage_ReplyTargetBugReproduction(t *testing.T) {
	msgBus := bus.New()
	ch, err := New(config.ZaloPersonalConfig{
		DMPolicy:    "open",
		GroupPolicy: "open",
	}, msgBus, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	botUID := "bot-user-id"
	ch.sess = &protocol.Session{UID: botUID}

	// 1. Anh Hiển (u-hien, M1) sends "@Báo Sư Đệ ăn c k" tagging the bot.
	msgHien := protocol.NewGroupMessage(botUID, protocol.TGroupMessage{
		TMessage: protocol.TMessage{
			MsgID:   "M1",
			UIDFrom: "u-hien",
			IDTo:    "group-zalo-1",
			DName:   "Anh Hiển",
			Content: textContent("@Báo Sư Đệ ăn c k"),
		},
		Mentions: []*protocol.TMention{
			{Pos: 0, Len: utf16Len("@Báo Sư Đệ"), UID: botUID},
		},
	})
	ch.handleGroupMessage(msgHien)

	// Consume inbound from bus
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	inboundHien, ok := msgBus.ConsumeInbound(ctx)
	if !ok {
		t.Fatal("expected inbound message for Anh Hien")
	}
	if inboundHien.Metadata["zalo_reply_msg_id"] != "M1" {
		t.Fatalf("zalo_reply_msg_id = %q, want M1", inboundHien.Metadata["zalo_reply_msg_id"])
	}

	// 2. While bot is thinking, Nguyễn Lương (u-luong, M2) sends "có về sớm ko ạ?" (NO bot mention).
	msgLuong := protocol.NewGroupMessage(botUID, protocol.TGroupMessage{
		TMessage: protocol.TMessage{
			MsgID:   "M2",
			UIDFrom: "u-luong",
			IDTo:    "group-zalo-1",
			DName:   "Nguyễn Lương",
			Content: textContent("có về sớm ko ạ?"),
		},
		Mentions: nil,
	})
	ch.handleGroupMessage(msgLuong)

	// Verify M2 did NOT generate an inbound event to the agent
	ctx2, cancel2 := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel2()
	if unexpected, ok := msgBus.ConsumeInbound(ctx2); ok {
		t.Fatalf("unexpected inbound message for Nguyen Luong: %+v", unexpected)
	}

	// CRITICAL ASSERTION 1: M2 must NOT be stored in inboundRefs!
	if _, ok := ch.loadInboundRef("group-zalo-1", "M2"); ok {
		t.Fatal("Nguyen Luong's message (M2) was incorrectly recorded in inboundRefs!")
	}

	// CRITICAL ASSERTION 2: Anh Hiển's message (M1) must still be recorded in inboundRefs!
	refHien, ok := ch.loadInboundRef("group-zalo-1", "M1")
	if !ok {
		t.Fatal("Anh Hien's message (M1) missing from inboundRefs")
	}
	if refHien.senderID != "u-hien" || refHien.senderName != "Anh Hiển" {
		t.Fatalf("inboundRef has wrong sender: ID=%q, Name=%q", refHien.senderID, refHien.senderName)
	}

	// CRITICAL ASSERTION 3: When outbound message is routed with reply_to_message_id = "M1":
	outMeta := channels.CopyFinalRoutingMeta(inboundHien.Metadata)
	outMeta["reply_to_message_id"] = inboundHien.Metadata["message_id"]
	replyMsgID := resolveReplyMsgID(outMeta)
	if replyMsgID != "M1" {
		t.Fatalf("resolved replyMsgID = %q, want M1", replyMsgID)
	}

	resolvedRef, ok := ch.loadInboundRef(inboundHien.ChatID, replyMsgID)
	if !ok {
		t.Fatalf("failed to load inbound ref for chat %s and msgID %s", inboundHien.ChatID, replyMsgID)
	}
	if resolvedRef.senderName != "Anh Hiển" {
		t.Fatalf("resolved sender = %q, want 'Anh Hiển' (must not be Nguyễn Lương!)", resolvedRef.senderName)
	}

	// Verify group mention prepends/tags Anh Hiển, not Nguyễn Lương
	replyContent, mentions := prepareGroupMentions("Dạ em ăn rồi nha anh Hiển", resolvedRef.senderID, resolvedRef.senderName)
	if !strings.Contains(replyContent, "@Anh Hiển") {
		t.Fatalf("replyContent missing @Anh Hiển: %q", replyContent)
	}
	if strings.Contains(replyContent, "Nguyễn Lương") {
		t.Fatalf("replyContent incorrectly mentions Nguyễn Lương: %q", replyContent)
	}
	if len(mentions) != 1 || mentions[0].UID != "u-hien" {
		t.Fatalf("mentions wrong: %+v", mentions)
	}
}

func TestHandleDM_RecordsInboundRefWithZaloReplyMsgID(t *testing.T) {
	msgBus := bus.New()
	ch, err := New(config.ZaloPersonalConfig{
		DMPolicy: "open",
	}, msgBus, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	msg := protocol.NewUserMessage("bot", protocol.TMessage{
		MsgID:   "dm-100",
		UIDFrom: "u-alice",
		IDTo:    "bot",
		DName:   "Alice",
		Content: textContent("Hello in DM"),
	})
	ch.handleDM(msg)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	inbound, ok := msgBus.ConsumeInbound(ctx)
	if !ok {
		t.Fatal("expected inbound for DM")
	}
	if inbound.Metadata["zalo_reply_msg_id"] != "dm-100" {
		t.Fatalf("zalo_reply_msg_id = %q, want dm-100", inbound.Metadata["zalo_reply_msg_id"])
	}

	ref, ok := ch.loadInboundRef("u-alice", "dm-100")
	if !ok {
		t.Fatal("DM inbound ref missing")
	}
	if ref.senderID != "u-alice" || ref.senderName != "Alice" {
		t.Fatalf("wrong DM ref: %+v", ref)
	}

	// Simulate CopyFinalRoutingMeta (which preserves zalo_reply_msg_id even without isGroup)
	finalMeta := channels.CopyFinalRoutingMeta(inbound.Metadata)
	replyMsgID := resolveReplyMsgID(finalMeta)
	if replyMsgID != "dm-100" {
		t.Fatalf("resolved replyMsgID = %q, want dm-100", replyMsgID)
	}
}

func TestSend_ReplyTargetVersusProactive(t *testing.T) {
	var requestedPaths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestedPaths = append(requestedPaths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"error_code": 0}`))
	}))
	defer server.Close()

	ch, err := New(config.ZaloPersonalConfig{
		DMPolicy:    "open",
		GroupPolicy: "open",
	}, bus.New(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	ch.SetRunning(true)

	sess := protocol.NewSession()
	sess.SecretKey = base64.StdEncoding.EncodeToString(make([]byte, 32))
	sess.LoginInfo = &protocol.LoginInfo{
		ZpwServiceMapV3: protocol.ZpwServiceMapV3{
			Group: []string{server.URL},
			Chat:  []string{server.URL},
		},
	}
	ch.sess = sess

	// Store an old message in group-1 from "old-user"
	ch.rememberInboundRef("group-1", "u-old", "Old User", protocol.TMessage{MsgID: "M-old"}, "câu cũ")

	// 1. Proactive send (from Cron or message tool): no reply_to_message_id, no zalo_reply_msg_id
	requestedPaths = nil
	proactiveMsg := bus.OutboundMessage{
		ChatID:   "group-1",
		Content:  "Bản tin sáng",
		Metadata: map[string]string{"group_id": "group-1"},
	}
	if err := ch.Send(context.Background(), proactiveMsg); err != nil {
		t.Fatalf("Send proactive failed: %v", err)
	}
	if len(requestedPaths) != 1 || requestedPaths[0] != "/api/group/sendmsg" {
		t.Fatalf("proactive message must use /api/group/sendmsg (plain), got %v", requestedPaths)
	}

	// 2. Targeted reply to M-old
	requestedPaths = nil
	replyMsg := bus.OutboundMessage{
		ChatID:  "group-1",
		Content: "Chào bạn",
		Metadata: map[string]string{
			"group_id":            "group-1",
			"reply_to_message_id": "M-old",
		},
	}
	if err := ch.Send(context.Background(), replyMsg); err != nil {
		t.Fatalf("Send reply failed: %v", err)
	}
	if len(requestedPaths) != 1 || requestedPaths[0] != "/api/group/quote" {
		t.Fatalf("targeted reply message must use /api/group/quote, got %v", requestedPaths)
	}

	// 3. Followup split bubble: original had zalo_reply_msg_id, but followup passed through CopyFollowupRoutingMeta
	requestedPaths = nil
	inboundMeta := map[string]string{
		"group_id":          "group-1",
		"zalo_reply_msg_id": "M-old",
	}
	followupMeta := channels.CopyFollowupRoutingMeta(inboundMeta)
	followupMsg := bus.OutboundMessage{
		ChatID:   "group-1",
		Content:  "Phần tiếp theo của câu trả lời",
		Metadata: followupMeta,
	}
	if err := ch.Send(context.Background(), followupMsg); err != nil {
		t.Fatalf("Send followup failed: %v", err)
	}
	if len(requestedPaths) != 1 || requestedPaths[0] != "/api/group/sendmsg" {
		t.Fatalf("followup split message must use /api/group/sendmsg (plain, no quote), got %v", requestedPaths)
	}
}
