package personal

import (
	"sync"
	"unicode/utf16"

	"github.com/nextlevelbuilder/goclaw/internal/channels/zalo/personal/protocol"
)

const defaultMaxInboundRefs = 512

// inboundRefKey uniquely identifies an inbound message within a chat thread.
type inboundRefKey struct {
	chatID string
	msgID  string
}

// inboundRef remembers the inbound message of a chat so outbound replies
// can quote it (threaded context) and mention its sender (groups).
type inboundRef struct {
	senderID   string
	senderName string
	quote      protocol.QuoteRef
}

// inboundRefStore maintains a thread-safe, bounded FIFO cache of inbound
// references keyed by (chatID, msgID).
type inboundRefStore struct {
	mu      sync.Mutex
	maxRefs int
	items   map[inboundRefKey]inboundRef
	order   []inboundRefKey
}

func newInboundRefStore(maxRefs int) *inboundRefStore {
	if maxRefs <= 0 {
		maxRefs = defaultMaxInboundRefs
	}
	return &inboundRefStore{
		maxRefs: maxRefs,
		items:   make(map[inboundRefKey]inboundRef, maxRefs),
		order:   make([]inboundRefKey, 0, maxRefs),
	}
}

func (s *inboundRefStore) store(key inboundRefKey, ref inboundRef) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.items == nil {
		s.items = make(map[inboundRefKey]inboundRef)
	}
	if s.maxRefs <= 0 {
		s.maxRefs = defaultMaxInboundRefs
	}

	if _, exists := s.items[key]; exists {
		s.items[key] = ref
		return
	}

	for len(s.order) >= s.maxRefs {
		oldest := s.order[0]
		delete(s.items, oldest)
		copy(s.order, s.order[1:])
		s.order = s.order[:len(s.order)-1]
	}

	s.order = append(s.order, key)
	s.items[key] = ref
}

func (s *inboundRefStore) load(key inboundRefKey) (inboundRef, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.items == nil {
		return inboundRef{}, false
	}
	ref, ok := s.items[key]
	return ref, ok
}

func (s *inboundRefStore) clear() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.items = make(map[inboundRefKey]inboundRef)
	s.order = s.order[:0]
}

func (s *inboundRefStore) len() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return len(s.items)
}

func (c *Channel) getInboundRefs() *inboundRefStore {
	c.mu.RLock()
	if c.inboundRefs != nil {
		defer c.mu.RUnlock()
		return c.inboundRefs
	}
	c.mu.RUnlock()

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.inboundRefs == nil {
		c.inboundRefs = newInboundRefStore(defaultMaxInboundRefs)
	}
	return c.inboundRefs
}

// rememberInboundRef stores the wire identifiers needed for quote-reply.
// It stores by (threadID, msg.MsgID). If threadID or msg.MsgID is empty, it does nothing.
func (c *Channel) rememberInboundRef(threadID, senderID, senderName string, msg protocol.TMessage, body string) {
	if threadID == "" || msg.MsgID == "" {
		return
	}
	attach := ""
	if msg.Quote != nil && len(msg.Quote.Attach.Raw) > 0 {
		attach = string(msg.Quote.Attach.Raw)
	}
	c.getInboundRefs().store(inboundRefKey{chatID: threadID, msgID: msg.MsgID}, inboundRef{
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

// loadInboundRef returns the inbound ref for (chatID, msgID), if any.
// If chatID or msgID is empty, it returns (inboundRef{}, false) immediately.
func (c *Channel) loadInboundRef(chatID, msgID string) (inboundRef, bool) {
	if chatID == "" || msgID == "" {
		return inboundRef{}, false
	}
	return c.getInboundRefs().load(inboundRefKey{chatID: chatID, msgID: msgID})
}

// resolveReplyMsgID extracts the reply target message ID from message metadata.
// It prioritizes reply_to_message_id, falling back to zalo_reply_msg_id.
func resolveReplyMsgID(meta map[string]string) string {
	if meta == nil {
		return ""
	}
	if id := meta["reply_to_message_id"]; id != "" {
		return id
	}
	return meta["zalo_reply_msg_id"]
}

// utf16Len returns UTF-16 code-unit length (the unit Zalo mention offsets use).
func utf16Len(s string) int {
	return len(utf16.Encode([]rune(s)))
}
