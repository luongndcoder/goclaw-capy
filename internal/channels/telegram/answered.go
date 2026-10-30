package telegram

import (
	"fmt"

	lru "github.com/hashicorp/golang-lru/v2"
	"github.com/mymmrac/telego"
)

// answeredCap bounds the answered-message-ID set used to route edited
// messages. Edits keep Telegram's original message_id, so a bounded
// recently-answered set is sufficient.
const answeredCap = 1000

// markAnswered records a chat+message ID as answered by the agent turn.
// Lazily initializes the set; a failed init degrades to no dedup.
func (c *Channel) markAnswered(chatID int64, msgID int) {
	key := fmt.Sprintf("%d:%d", chatID, msgID)
	c.answeredMu.Lock()
	defer c.answeredMu.Unlock()
	if c.answeredIDs == nil {
		if cache, err := lru.New[string, struct{}](answeredCap); err == nil {
			c.answeredIDs = cache
		} else {
			return
		}
	}
	c.answeredIDs.Add(key, struct{}{})
}

// wasAnswered reports whether this exact chat message already got an agent
// turn — the condition for routing an edit of it as a follow-up.
func (c *Channel) wasAnswered(chatID int64, msgID int) bool {
	key := fmt.Sprintf("%d:%d", chatID, msgID)
	c.answeredMu.Lock()
	defer c.answeredMu.Unlock()
	if c.answeredIDs == nil {
		return false
	}
	return c.answeredIDs.Contains(key)
}

// answeredEdit returns the edited message if it edits a chat message that
// already got an agent turn, else nil. Edits of unanswered messages stay
// skipped — nothing was promised yet, and answering every keystroke-edit
// would double-respond.
func (c *Channel) answeredEdit(update telego.Update) *telego.Message {
	var m *telego.Message
	switch {
	case update.EditedMessage != nil:
		m = update.EditedMessage
	case update.EditedChannelPost != nil:
		m = update.EditedChannelPost
	default:
		return nil
	}
	if m == nil || !c.wasAnswered(m.Chat.ID, m.MessageID) {
		return nil
	}
	return m
}
