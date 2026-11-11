package personal

import (
	"testing"

	"github.com/nextlevelbuilder/goclaw/internal/channels/zalo/personal/protocol"
)

func TestPrepareGroupMentions_UserMentionedInBody(t *testing.T) {
	// Recreating the exact scenario from the screenshot:
	// The LLM output has a poem with "@Bông ơi..." on line 2 and "Chị @Bông..." on the last line.
	// It should NOT prepend "@Bông " at line 1.
	// Both "@Bông" should be extracted into mentions with correct offsets.
	body := "Loa loa loa loa!\n@Bông ơi chị Bông xinh đẹp ơi,\nMấy ẻm áo quần đứng đợi rồi.\n\nChị @Bông mau mang quần áo xuống giặt nha! 🐆"
	senderID := "user-bong-123"
	senderName := "Bông"

	gotText, mentions := prepareGroupMentions(body, senderID, senderName)

	if gotText != body {
		t.Errorf("gotText changed unexpectedly; got %q, want %q", gotText, body)
	}

	if len(mentions) != 2 {
		t.Fatalf("expected 2 mentions, got %d", len(mentions))
	}

	// Verify first mention: "@Bông" on line 2
	m1 := mentions[0]
	if m1.UID != senderID {
		t.Errorf("m1 UID = %q, want %q", m1.UID, senderID)
	}
	expectedLen := utf16Len("@Bông")
	if m1.Len != expectedLen {
		t.Errorf("m1 Len = %d, want %d", m1.Len, expectedLen)
	}
	prefixLine1 := "Loa loa loa loa!\n"
	if m1.Pos != utf16Len(prefixLine1) {
		t.Errorf("m1 Pos = %d, want %d", m1.Pos, utf16Len(prefixLine1))
	}

	// Verify second mention: "Chị @Bông"
	m2 := mentions[1]
	prefixBeforeM2 := "Loa loa loa loa!\n@Bông ơi chị Bông xinh đẹp ơi,\nMấy ẻm áo quần đứng đợi rồi.\n\nChị "
	if m2.Pos != utf16Len(prefixBeforeM2) {
		t.Errorf("m2 Pos = %d, want %d", m2.Pos, utf16Len(prefixBeforeM2))
	}
	if m2.Len != expectedLen {
		t.Errorf("m2 Len = %d, want %d", m2.Len, expectedLen)
	}
}

func TestPrepareGroupMentions_AlreadyStartsWithTag(t *testing.T) {
	body := "@Bông mau mang quần áo xuống giặt nha!"
	senderID := "u-1"
	senderName := "Bông"

	gotText, mentions := prepareGroupMentions(body, senderID, senderName)
	if gotText != body {
		t.Errorf("gotText = %q, want %q (should not duplicate tag)", gotText, body)
	}
	if len(mentions) != 1 {
		t.Fatalf("expected 1 mention, got %d", len(mentions))
	}
	if mentions[0].Pos != 0 || mentions[0].Len != utf16Len("@Bông") {
		t.Errorf("mention span wrong: %+v", mentions[0])
	}
}

func TestPrepareGroupMentions_StartsWithAtAll(t *testing.T) {
	body := "@all mọi người chú ý"
	senderID := "u-1"
	senderName := "Bông"

	gotText, mentions := prepareGroupMentions(body, senderID, senderName)
	if gotText != body {
		t.Errorf("gotText = %q, want %q (should not prepend @Bông)", gotText, body)
	}
	if len(mentions) != 0 {
		t.Errorf("expected 0 mentions for @all prefix, got %d", len(mentions))
	}
}

func TestPrepareGroupMentions_FallbackPrepend(t *testing.T) {
	body := "Máy giặt xong rồi nha!"
	senderID := "u-1"
	senderName := "Bông"

	gotText, mentions := prepareGroupMentions(body, senderID, senderName)
	wantText := "@Bông Máy giặt xong rồi nha!"
	if gotText != wantText {
		t.Errorf("gotText = %q, want %q", gotText, wantText)
	}
	if len(mentions) != 1 {
		t.Fatalf("expected 1 mention, got %d", len(mentions))
	}
	if mentions[0].Pos != 0 || mentions[0].Len != utf16Len("@Bông") || mentions[0].UID != senderID {
		t.Errorf("mention span wrong: %+v", mentions[0])
	}
}

func TestPrepareGroupMentions_CaseInsensitive(t *testing.T) {
	body := "Chào @bông xinh gái!"
	senderID := "u-1"
	senderName := "Bông"

	gotText, mentions := prepareGroupMentions(body, senderID, senderName)
	if gotText != body {
		t.Errorf("gotText = %q, want %q", gotText, body)
	}
	if len(mentions) != 1 {
		t.Fatalf("expected 1 mention, got %d", len(mentions))
	}
	if mentions[0].Pos != utf16Len("Chào ") || mentions[0].Len != utf16Len("@bông") {
		t.Errorf("mention span wrong: %+v", mentions[0])
	}
}

func TestPrepareGroupMentions_IgnoresEmail(t *testing.T) {
	body := "Liên hệ test@bông.com nha"
	senderID := "u-1"
	senderName := "Bông"

	gotText, mentions := prepareGroupMentions(body, senderID, senderName)
	// Should treat as not containing mention, so prepends "@Bông "
	wantText := "@Bông Liên hệ test@bông.com nha"
	if gotText != wantText {
		t.Errorf("gotText = %q, want %q", gotText, wantText)
	}
	if len(mentions) != 1 || mentions[0].Pos != 0 {
		t.Errorf("mention span wrong: %+v", mentions)
	}
}

func TestPrepareGroupMentions_EmojiOffsetHandling(t *testing.T) {
	// 🐆 is a 4-byte UTF-8, 2 UTF-16 code units (surrogate pair)
	body := "🐆 Chào @Bông!"
	senderID := "u-1"
	senderName := "Bông"

	_, mentions := prepareGroupMentions(body, senderID, senderName)
	if len(mentions) != 1 {
		t.Fatalf("expected 1 mention, got %d", len(mentions))
	}
	// 🐆 (2) + ' ' (1) + 'C' (1) + 'h' (1) + 'à' (1) + 'o' (1) + ' ' (1) = 8
	expectedPos := 8
	if mentions[0].Pos != expectedPos {
		t.Errorf("Pos = %d, want %d", mentions[0].Pos, expectedPos)
	}
}

func TestFilterMentionsForChunk(t *testing.T) {
	chunk := "Hello @World"

	mentions := []protocol.Mention{
		{Pos: 6, Len: 6, UID: "1"},  // fits
		{Pos: 10, Len: 5, UID: "2"}, // exceeds chunkLen (12)
		{Pos: 20, Len: 5, UID: "3"}, // completely out
	}

	filtered := filterMentionsForChunk(mentions, chunk)
	if len(filtered) != 1 || filtered[0].UID != "1" {
		t.Errorf("filtered wrong: %+v", filtered)
	}
}
