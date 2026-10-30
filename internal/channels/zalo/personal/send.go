package personal

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/nextlevelbuilder/goclaw/internal/bus"
	"github.com/nextlevelbuilder/goclaw/internal/channels"
	"github.com/nextlevelbuilder/goclaw/internal/channels/typing"
	"github.com/nextlevelbuilder/goclaw/internal/channels/zalo"
	"github.com/nextlevelbuilder/goclaw/internal/channels/zalo/personal/protocol"
	"log/slog"
)

const maxTextLength = 2000

const (
	// zaloChunkPacing spaces multi-bubble answers so they read (and deliver)
	// in order instead of arriving as an instant burst.
	zaloChunkPacing = 400 * time.Millisecond
	// zaloChunkRetryDelay waits before the single retry of a failed chunk.
	zaloChunkRetryDelay = time.Second
)

// Send delivers an outbound message to a Zalo chat.
func (c *Channel) Send(ctx context.Context, msg bus.OutboundMessage) error {
	sess := c.session()
	if !c.IsRunning() || sess == nil {
		return fmt.Errorf("zalo_personal channel not running")
	}

	// Strip markdown — Zalo does not support any markup rendering.
	msg.Content = zalo.StripMarkdown(msg.Content)

	// Stop typing indicator before sending response
	if ctrl, ok := c.typingCtrls.LoadAndDelete(msg.ChatID); ok {
		ctrl.(*typing.Controller).Stop()
	}

	// Strip raw technical leakage before anything user-facing is built.
	msg.Content = channels.SanitizeOutbound(msg.Content)

	// Inline remote images: a bare image URL alone on a line (QR codes,
	// screenshots, charts) is downloaded and sent as a real image so the
	// user sees it in-chat instead of tapping a link. Failures keep the URL
	// text untouched as fallback.
	rest, fetched := fetchInlineImages(ctx, msg.Content)
	msg.Content = rest
	for _, tmp := range fetched {
		msg.Media = append(msg.Media, bus.MediaAttachment{URL: tmp})
	}
	// Fetched temp files are created inside Send (after dispatch's temp-media
	// claiming ran), so release them here instead of leaking on disk.
	defer func() {
		for _, tmp := range fetched {
			os.Remove(tmp)
		}
	}()

	threadType := protocol.ThreadTypeUser
	if c.IsGroupApproved(msg.ChatID) {
		threadType = protocol.ThreadTypeGroup
	} else if msg.Metadata != nil {
		if _, ok := msg.Metadata["group_id"]; ok {
			threadType = protocol.ThreadTypeGroup
			c.MarkGroupApproved(msg.ChatID)
		}
	}

	// Send media attachments.
	for i := range msg.Media {
		media := &msg.Media[i]
		media.Caption = channels.SanitizeOutbound(media.Caption)
		if protocol.IsImageFile(media.URL) {
			if err := c.sendImage(ctx, sess, msg.ChatID, threadType, media.URL, media.Caption); err != nil {
				slog.Warn("zalo_personal: failed to send image", "path", media.URL, "error", err)
			}
		} else {
			if err := c.sendFile(ctx, sess, msg.ChatID, threadType, media.URL); err != nil {
				slog.Warn("zalo_personal: failed to send file", "path", media.URL, "error", err)
			}
		}
	}

	// Send text content (if any remains after media). The first chunk quotes
	// the triggering inbound message when known (threaded context); groups
	// additionally @-mention the asker.
	var quote *protocol.QuoteRef
	var mentions []protocol.Mention
	if ref, ok := c.loadInboundRef(msg.ChatID); ok {
		q := ref.quote
		quote = &q
		if threadType == protocol.ThreadTypeGroup && ref.senderID != "" {
			name := ref.senderName
			if name == "" {
				name = ref.senderID
			}
			// Mention span covers "@Name" only — the trailing space stays
			// outside the highlight so the reply text doesn't stick to it.
			tag := "@" + name
			msg.Content = tag + " " + msg.Content
			mentions = append(mentions, protocol.Mention{
				Pos: 0,
				Len: utf16Len(tag),
				UID: ref.senderID,
			})
		}
	}
	if msg.Content != "" {
		return c.sendChunkedText(ctx, sess, msg.ChatID, threadType, msg.Content, quote, mentions)
	}
	return nil
}

// sendImage uploads and sends an image file to a Zalo thread.
func (c *Channel) sendImage(ctx context.Context, sess *protocol.Session, chatID string, threadType protocol.ThreadType, filePath, caption string) error {
	upload, err := protocol.UploadImage(ctx, sess, chatID, threadType, filePath)
	if err != nil {
		return fmt.Errorf("upload: %w", err)
	}

	_, err = protocol.SendImage(ctx, sess, chatID, threadType, upload, caption)
	return err
}

// sendFile uploads and sends a file to a Zalo thread.
func (c *Channel) sendFile(ctx context.Context, sess *protocol.Session, chatID string, threadType protocol.ThreadType, filePath string) error {
	ln := c.getListener()
	if ln == nil {
		return fmt.Errorf("listener not available for file upload")
	}
	upload, err := protocol.UploadFile(ctx, sess, ln, chatID, threadType, filePath)
	if err != nil {
		return fmt.Errorf("upload: %w", err)
	}

	_, err = protocol.SendFile(ctx, sess, chatID, threadType, upload)
	return err
}

func (c *Channel) sendChunkedText(ctx context.Context, sess *protocol.Session, chatID string, threadType protocol.ThreadType, text string, quote *protocol.QuoteRef, mentions []protocol.Mention) error {
	chunks := channels.ChunkMarkdown(text, maxTextLength)
	for i, chunk := range chunks {
		// Pacing: rapid-fire bubbles feel robotic and risk tripping Zalo's
		// unofficial-API rate limits. First chunk goes immediately.
		if i > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(zaloChunkPacing):
			}
		}
		// Only the first chunk carries the quote/mention; the rest are plain
		// continuations. Delivery degrades gracefully: quote → mention-only
		// (group) → plain, so the reply is never lost to a malformed
		// quote/mention payload.
		var err error
		if i == 0 && quote != nil {
			err = c.sendChunkWithRetryQuote(ctx, sess, chatID, threadType, chunk, quote, mentions)
			if err != nil {
				slog.Warn("zalo_personal: quote send failed, degrading",
					"chat_id", chatID, "error", err)
				if threadType == protocol.ThreadTypeGroup && len(mentions) > 0 {
					err = c.sendChunkWithRetryMention(ctx, sess, chatID, chunk, mentions)
					if err != nil {
						slog.Warn("zalo_personal: mention send failed, falling back to plain",
							"chat_id", chatID, "error", err)
					}
				}
			}
		}
		if err != nil || i > 0 || quote == nil {
			if qerr := c.sendChunkWithRetry(ctx, sess, chatID, threadType, chunk); qerr != nil {
				return qerr
			}
			continue
		}
	}
	return nil
}

// sendChunkWithRetryQuote delivers one chunk via the quote endpoint.
func (c *Channel) sendChunkWithRetryQuote(ctx context.Context, sess *protocol.Session, chatID string, threadType protocol.ThreadType, chunk string, quote *protocol.QuoteRef, mentions []protocol.Mention) error {
	var err error
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(zaloChunkRetryDelay):
			}
		}
		if _, err = protocol.SendMessageWithQuote(ctx, sess, chatID, threadType, chunk, quote, mentions); err == nil {
			return nil
		}
		slog.Warn("zalo_personal: quote chunk send failed, retrying",
			"chat_id", chatID, "attempt", attempt+1, "error", err)
	}
	return err
}

// sendChunkWithRetryMention delivers one group chunk via the /mention
// endpoint (mention without quote).
func (c *Channel) sendChunkWithRetryMention(ctx context.Context, sess *protocol.Session, chatID, chunk string, mentions []protocol.Mention) error {
	var err error
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(zaloChunkRetryDelay):
			}
		}
		if _, err = protocol.SendMessageWithMention(ctx, sess, chatID, chunk, mentions); err == nil {
			return nil
		}
		slog.Warn("zalo_personal: mention chunk send failed, retrying",
			"chat_id", chatID, "attempt", attempt+1, "error", err)
	}
	return err
}

// sendChunkWithRetry delivers one text chunk with a single retry: the
// unofficial Zalo API flakes transiently, and losing a middle chunk garbles
// the whole answer.
func (c *Channel) sendChunkWithRetry(ctx context.Context, sess *protocol.Session, chatID string, threadType protocol.ThreadType, chunk string) error {
	var err error
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(zaloChunkRetryDelay):
			}
		}
		if _, err = protocol.SendMessage(ctx, sess, chatID, threadType, chunk); err == nil {
			return nil
		}
		slog.Warn("zalo_personal: chunk send failed, retrying",
			"chat_id", chatID, "attempt", attempt+1, "error", err)
	}
	return err
}
