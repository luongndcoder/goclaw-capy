package personal

import (
	"unicode/utf16"

	"github.com/nextlevelbuilder/goclaw/internal/channels/zalo/personal/protocol"
)

// inboundRef remembers the last inbound message of a chat so outbound replies
// can quote it (threaded context) and mention its sender (groups).
type inboundRef struct {
	senderID   string
	senderName string
	quote      protocol.QuoteRef
}

// rememberInboundRef stores the wire identifiers needed for quote-reply.
// Call alongside rememberReplyContext at every inbound handler.
func (c *Channel) rememberInboundRef(threadID, senderID, senderName string, msg protocol.TMessage, body string) {
	attach := ""
	if msg.Quote != nil && len(msg.Quote.Attach.Raw) > 0 {
		attach = string(msg.Quote.Attach.Raw)
	}
	c.inboundRefs.Store(threadID, inboundRef{
		senderID:   senderID,
		senderName: senderName,
		quote: protocol.QuoteRef{
			OwnerID:     senderID,
			GlobalMsgID: msg.GlobalMsgID,
			MsgID:       msg.MsgID,
			CliMsgID:    msg.CliMsgID,
			MsgType:     msg.MsgType,
			TS:          msg.TS,
			Msg:         body,
			AttachRaw:   attach,
			PropertyExt: string(msg.PropertyExt),
		},
	})
}

// loadInboundRef returns the last inbound ref for a chat, if any.
func (c *Channel) loadInboundRef(chatID string) (inboundRef, bool) {
	if v, ok := c.inboundRefs.Load(chatID); ok {
		if ref, ok := v.(inboundRef); ok {
			return ref, true
		}
	}
	return inboundRef{}, false
}

// utf16Len returns UTF-16 code-unit length (the unit Zalo mention offsets use).
func utf16Len(s string) int {
	return len(utf16.Encode([]rune(s)))
}
