package channels

import "testing"

func TestCopyRoutingMeta_PreservesPancakeCommentRouting(t *testing.T) {
	src := map[string]string{
		"pancake_mode":        "comment",
		"reply_to_comment_id": "msg-123",
		"sender_id":           "user-123",
	}

	got := copyRoutingMeta(src)

	for key, want := range map[string]string{
		"pancake_mode":        "comment",
		"reply_to_comment_id": "msg-123",
		"sender_id":           "user-123",
	} {
		if got[key] != want {
			t.Fatalf("copyRoutingMeta()[%q] = %q, want %q", key, got[key], want)
		}
	}
}

func TestCopyFinalRoutingMeta_PreservesPlaceholderAndPancakeMode(t *testing.T) {
	src := map[string]string{
		"placeholder_key": "placeholder-123",
		"pancake_mode":    "comment",
	}

	got := CopyFinalRoutingMeta(src)

	if got["placeholder_key"] != "placeholder-123" {
		t.Fatalf("CopyFinalRoutingMeta()[%q] = %q, want %q", "placeholder_key", got["placeholder_key"], "placeholder-123")
	}
	if got["pancake_mode"] != "comment" {
		t.Fatalf("CopyFinalRoutingMeta()[%q] = %q, want %q", "pancake_mode", got["pancake_mode"], "comment")
	}
}

// TestCopyRoutingMeta_PreservesPancakePrivateReplyKeys verifies the metadata
// keys used by the private_reply DM (post_id, display_name, sender_id)
// survive inbound→outbound copy.
func TestCopyRoutingMeta_PreservesPancakePrivateReplyKeys(t *testing.T) {
	src := map[string]string{
		"post_id":      "post-42",
		"display_name": "Tuấn",
		"sender_id":    "user-1",
	}

	got := copyRoutingMeta(src)
	for k, want := range src {
		if got[k] != want {
			t.Fatalf("copyRoutingMeta()[%q] = %q, want %q", k, got[k], want)
		}
	}

	final := CopyFinalRoutingMeta(src)
	for k, want := range src {
		if final[k] != want {
			t.Fatalf("CopyFinalRoutingMeta()[%q] = %q, want %q", k, final[k], want)
		}
	}
}

// TestCopyRoutingMeta_PreservesBitrixVisibility guards the whisper-leak
// regression: intermediate replies (quick-ack, progress, reasoning bubbles,
// block replies) must carry bitrix_visibility so a whisper-mode turn does not
// leak its FIRST (intermediate) message to the external connector while only
// the final answer is whispered. copyRoutingMeta is the path used by every
// intermediate publish site; CopyFinalRoutingMeta must keep it too.
func TestCopyRoutingMeta_PreservesBitrixVisibility(t *testing.T) {
	src := map[string]string{"bitrix_visibility": "whisper"}

	if got := copyRoutingMeta(src); got["bitrix_visibility"] != "whisper" {
		t.Fatalf("copyRoutingMeta()[%q] = %q, want %q", "bitrix_visibility", got["bitrix_visibility"], "whisper")
	}
	if got := CopyFinalRoutingMeta(src); got["bitrix_visibility"] != "whisper" {
		t.Fatalf("CopyFinalRoutingMeta()[%q] = %q, want %q", "bitrix_visibility", got["bitrix_visibility"], "whisper")
	}
}

// TestCopyFinalRoutingMeta_ZaloReplyMsgIDPreservedOnlyInFinal verifies that
// zalo_reply_msg_id survives into the first final reply (CopyFinalRoutingMeta)
// so the initial answer bubble quotes & mentions the sender, but is stripped
// from intermediate block replies and followup splits (CopyFollowupRoutingMeta).
func TestCopyFinalRoutingMeta_ZaloReplyMsgIDPreservedOnlyInFinal(t *testing.T) {
	src := map[string]string{
		"zalo_reply_msg_id": "zalo-msg-12345",
		"group_id":          "zalo-group-999",
	}

	final := CopyFinalRoutingMeta(src)
	if final["zalo_reply_msg_id"] != "zalo-msg-12345" {
		t.Fatalf("CopyFinalRoutingMeta()[%q] = %q, want %q", "zalo_reply_msg_id", final["zalo_reply_msg_id"], "zalo-msg-12345")
	}
	if final["group_id"] != "zalo-group-999" {
		t.Fatalf("CopyFinalRoutingMeta()[%q] = %q, want %q", "group_id", final["group_id"], "zalo-group-999")
	}

	followup := CopyFollowupRoutingMeta(src)
	if v, exists := followup["zalo_reply_msg_id"]; exists && v != "" {
		t.Fatalf("CopyFollowupRoutingMeta() unexpectedly kept zalo_reply_msg_id: %q", v)
	}
	if followup["group_id"] != "zalo-group-999" {
		t.Fatalf("CopyFollowupRoutingMeta()[%q] = %q, want %q", "group_id", followup["group_id"], "zalo-group-999")
	}

	intermediate := copyRoutingMeta(src)
	if v, exists := intermediate["zalo_reply_msg_id"]; exists && v != "" {
		t.Fatalf("copyRoutingMeta() unexpectedly kept zalo_reply_msg_id: %q", v)
	}
}
