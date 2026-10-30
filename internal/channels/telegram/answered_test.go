package telegram

import (
	"testing"

	"github.com/mymmrac/telego"
)

// TestAnsweredEdit_RoutesOnlyAnsweredEdits proves the edit-routing rule:
// edits of answered messages become follow-ups; everything else is skipped.
func TestAnsweredEdit_RoutesOnlyAnsweredEdits(t *testing.T) {
	c := &Channel{}
	mkUpdate := func(msgID int) telego.Update {
		return telego.Update{
			UpdateID:      1,
			EditedMessage: &telego.Message{MessageID: msgID, Chat: telego.Chat{ID: 7}},
		}
	}

	// Unanswered edit → nil (skip).
	if got := c.answeredEdit(mkUpdate(42)); got != nil {
		t.Fatalf("unanswered edit must be skipped, got %+v", got)
	}
	// Plain update without edits → nil.
	if got := c.answeredEdit(telego.Update{UpdateID: 2}); got != nil {
		t.Fatalf("non-edit update must be nil, got %+v", got)
	}

	c.markAnswered(7, 42)
	got := c.answeredEdit(mkUpdate(42))
	if got == nil || got.MessageID != 42 || got.Chat.ID != 7 {
		t.Fatalf("answered edit must route, got %+v", got)
	}

	// Same message ID in another chat → still skipped (key is chat-scoped).
	other := telego.Update{
		UpdateID:      3,
		EditedMessage: &telego.Message{MessageID: 42, Chat: telego.Chat{ID: 8}},
	}
	if got := c.answeredEdit(other); got != nil {
		t.Fatalf("cross-chat edit must be skipped, got %+v", got)
	}
}
