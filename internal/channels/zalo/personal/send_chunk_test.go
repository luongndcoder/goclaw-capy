package personal

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/nextlevelbuilder/goclaw/internal/bus"
	"github.com/nextlevelbuilder/goclaw/internal/channels/zalo/personal/protocol"
	"github.com/nextlevelbuilder/goclaw/internal/config"
)

func TestSendChunkedText_MultiChunkMentionsAndEndpoints(t *testing.T) {
	oldPacing := zaloChunkPacing
	oldRetry := zaloChunkRetryDelay
	zaloChunkPacing = 0
	zaloChunkRetryDelay = 0
	defer func() {
		zaloChunkPacing = oldPacing
		zaloChunkRetryDelay = oldRetry
	}()

	var mu sync.Mutex
	var requestedPaths []string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requestedPaths = append(requestedPaths, r.URL.Path)
		mu.Unlock()

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

	// Store inbound ref for reply targeting
	ch.rememberInboundRef("group-1", "u-bong", "Bông", protocol.TMessage{MsgID: "M-1"}, "inbound text")

	// Message that will split into 3 chunks:
	// Chunk 0: ~1990 characters of introductory text (does not mention Bông)
	// Chunk 1: Mentions "@Chị Bông"
	// Chunk 2: Plain concluding text
	part1 := strings.Repeat("A", 1990)
	part2 := "Cảm ơn @Chị Bông rất nhiều vì đã hỗ trợ hôm nay!"
	part3 := strings.Repeat("B", 1990)
	fullText := part1 + "\n\n" + part2 + "\n\n" + part3

	msg := bus.OutboundMessage{
		ChatID:  "group-1",
		Content: fullText,
		Metadata: map[string]string{
			"group_id":            "group-1",
			"reply_to_message_id": "M-1",
		},
	}

	if err := ch.Send(context.Background(), msg); err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	// Chunk 0: Quote reply (/api/group/quote)
	// Chunk 1: Continuation with mention (/api/group/mention)
	// Chunk 2: Plain continuation (/api/group/sendmsg)
	wantPaths := []string{
		"/api/group/quote",
		"/api/group/mention",
		"/api/group/sendmsg",
	}

	if len(requestedPaths) != len(wantPaths) {
		t.Fatalf("expected %d requests, got %d: %v", len(wantPaths), len(requestedPaths), requestedPaths)
	}

	for i, want := range wantPaths {
		if requestedPaths[i] != want {
			t.Errorf("chunk %d: got path %q, want %q", i, requestedPaths[i], want)
		}
	}
}

func TestSendChunkedText_ContinuationMentionFallback(t *testing.T) {
	oldPacing := zaloChunkPacing
	oldRetry := zaloChunkRetryDelay
	zaloChunkPacing = 0
	zaloChunkRetryDelay = 0
	defer func() {
		zaloChunkPacing = oldPacing
		zaloChunkRetryDelay = oldRetry
	}()

	var mu sync.Mutex
	var requestedPaths []string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requestedPaths = append(requestedPaths, r.URL.Path)
		mu.Unlock()

		if r.URL.Path == "/api/group/mention" {
			// Simulate mention API endpoint failure
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

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

	ch.rememberInboundRef("group-1", "u-bong", "Bông", protocol.TMessage{MsgID: "M-1"}, "inbound")

	part1 := strings.Repeat("A", 1990)
	part2 := "Tiếp tục trao đổi với @Anh Bông nhé!"
	fullText := part1 + "\n\n" + part2

	msg := bus.OutboundMessage{
		ChatID:  "group-1",
		Content: fullText,
		Metadata: map[string]string{
			"group_id":            "group-1",
			"reply_to_message_id": "M-1",
		},
	}

	if err := ch.Send(context.Background(), msg); err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	// Chunk 0: /api/group/quote
	// Chunk 1: /api/group/mention (fails twice with retry) -> falls back to /api/group/sendmsg
	// Verify that /api/group/sendmsg was reached for chunk 1
	var hasSendMsg bool
	if slices.Contains(requestedPaths, "/api/group/sendmsg") {
		hasSendMsg = true
	}
	if !hasSendMsg {
		t.Fatalf("expected fallback to /api/group/sendmsg, got calls: %v", requestedPaths)
	}
}

func TestSendChunkedText_ChunkOffsetAccuracy(t *testing.T) {
	// Verify that findMentions on a continuation chunk produces offsets relative
	// to that specific chunk, rather than drifting or using full-text offsets.
	chunk1 := "Dạ chào @Anh Hiển, thông tin đã cập nhật."
	mentions := findMentions(chunk1, "u-hien", "Hiển")

	if len(mentions) != 1 {
		t.Fatalf("expected 1 mention, got %d", len(mentions))
	}

	wantPos := utf16Len("Dạ chào ")
	if mentions[0].Pos != wantPos {
		t.Errorf("chunk1 Pos = %d, want %d", mentions[0].Pos, wantPos)
	}

	wantLen := utf16Len("@Anh Hiển")
	if mentions[0].Len != wantLen {
		t.Errorf("chunk1 Len = %d, want %d", mentions[0].Len, wantLen)
	}
}
