package protocol

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// QuoteRef carries the wire identifiers of an inbound message so an outbound
// reply can quote it. Field mapping follows the community spec (zca-js
// sendMessage / zcago send_message.go): qmsgOwner/qmsgId/qmsgCliId/qmsgType/
// qmsgTs/qmsg/qmsgTTL, qmsgAttach group-only.
type QuoteRef struct {
	OwnerID     string // uidFrom of the inbound message
	GlobalMsgID string // server-wide id (fallback when MsgID empty)
	MsgID       string // message id — the qmsgId wire value
	CliMsgID    string // client message id
	CliMsgType  int    // client message type (0 = derive from MsgType)
	MsgType     string // inbound msgType string (webchat, chat.photo, ...)
	TS          string // inbound ts
	Msg         string // inbound text (for text quotes)
	AttachRaw   string // original quote.attach JSON, passed through when present
	// PropertyExt is the inbound propertyExt JSON (text style metadata).
	// Group text quotes carry it as qmsgAttach, mirroring the reference client.
	PropertyExt string
	TTL         int
}

// Mention targets one user inside a group reply. Pos/Len are UTF-16 code
// units of msg, matching the wire format. Group-only; DMs ignore mentions.
type Mention struct {
	Pos int
	Len int
	UID string
}

// clientMessageType maps inbound msgType strings to wire qmsgType ints.
// Mirrors zca-js getClientMessageType; unknown types default to 1 (text).
func clientMessageType(msgType string) int {
	switch msgType {
	case "chat.voice":
		return 31
	case "chat.photo":
		return 32
	case "chat.sticker":
		return 36
	case "chat.doodle":
		return 37
	case "chat.recommended", "chat.link":
		return 38
	case "chat.location.new":
		return 43
	case "chat.video.msg":
		return 44
	case "share.file":
		return 46
	case "chat.gif":
		return 49
	default:
		return 1
	}
}

// quoteMsgID is the qmsgId wire value: the message id, with the server-wide
// id as fallback — matching the reference client's quote.msgId.
func (q *QuoteRef) quoteMsgID() string {
	if q.MsgID != "" {
		return q.MsgID
	}
	return q.GlobalMsgID
}

// quoteMsgType returns the explicit client type or derives it.
func (q *QuoteRef) quoteMsgType() int {
	if q.CliMsgType > 0 {
		return q.CliMsgType
	}
	return clientMessageType(q.MsgType)
}

// SendMessageWithQuote sends a text reply quoting an inbound message and
// optionally mentioning users (group-only). Endpoints: /api/message/quote
// (DM) and /api/group/quote (group). Callers should fall back to plain
// SendMessage on error so the reply is still delivered.
func SendMessageWithQuote(ctx context.Context, sess *Session, threadID string, threadType ThreadType, text string, quote *QuoteRef, mentions []Mention) (string, error) {
	if text == "" {
		return "", fmt.Errorf("zalo_personal: message text cannot be empty")
	}
	if quote == nil {
		return "", fmt.Errorf("zalo_personal: quote reference required")
	}

	serviceKey := "chat"
	apiPath := "/api/message/quote"
	if threadType == ThreadTypeGroup {
		serviceKey = "group"
		apiPath = "/api/group/quote"
	}

	baseURL := getServiceURL(sess, serviceKey)
	if baseURL == "" {
		return "", fmt.Errorf("zalo_personal: no service URL for %s", serviceKey)
	}

	payload := map[string]any{
		"message":   text,
		"clientId":  time.Now().UnixMilli(),
		"ttl":       0,
		"qmsgOwner": quote.OwnerID,
		"qmsgId":    quote.quoteMsgID(),
		"qmsgCliId": quote.CliMsgID,
		"qmsgType":  quote.quoteMsgType(),
		"qmsgTs":    quote.TS,
		"qmsg":      quote.Msg,
		"qmsgTTL":   quote.TTL,
	}
	// Group-only attachment: original attach passthrough first, else the
	// inbound propertyExt for text quotes (mirrors the reference client,
	// which always sends qmsgAttach on group quotes when available).
	if threadType == ThreadTypeGroup {
		if quote.AttachRaw != "" {
			payload["qmsgAttach"] = quote.AttachRaw
		} else if quote.PropertyExt != "" {
			payload["qmsgAttach"] = quote.PropertyExt
		}
	}
	if threadType == ThreadTypeGroup {
		payload["grid"] = threadID
		payload["visibility"] = 0
		if len(mentions) > 0 {
			mentionInfo, err := encodeMentionInfo(mentions)
			if err != nil {
				return "", err
			}
			payload["mentionInfo"] = mentionInfo
		}
	} else {
		payload["toid"] = threadID
		payload["imei"] = sess.IMEI
	}

	encData, err := encryptPayload(sess, payload)
	if err != nil {
		return "", fmt.Errorf("zalo_personal: encrypt quote payload: %w", err)
	}

	sendURL := makeURL(sess, baseURL+apiPath, map[string]any{"nretry": 0}, true)
	form := buildFormBody(map[string]string{"params": encData})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sendURL, form)
	if err != nil {
		return "", err
	}
	setDefaultHeaders(req, sess)

	resp, err := sess.Client.Do(req)
	if err != nil {
		return "", fmt.Errorf("zalo_personal: send quote: %w", err)
	}
	defer resp.Body.Close()

	var envelope Response[*string]
	if err := readJSON(resp, &envelope); err != nil {
		return "", fmt.Errorf("zalo_personal: parse quote response: %w", err)
	}
	if envelope.ErrorCode != 0 {
		return "", fmt.Errorf("zalo_personal: quote error code %d", envelope.ErrorCode)
	}
	if envelope.Data == nil {
		return "", nil
	}

	plain, err := decryptDataField(sess, *envelope.Data)
	if err != nil {
		return "", fmt.Errorf("zalo_personal: decrypt quote result: %w", err)
	}

	var result struct {
		MsgID json.Number `json:"msgId"`
	}
	if err := json.Unmarshal(plain, &result); err != nil {
		return "", fmt.Errorf("zalo_personal: parse quote result: %w", err)
	}

	return result.MsgID.String(), nil
}

// encodeMentionInfo renders the wire mentionInfo JSON for group mentions.
func encodeMentionInfo(mentions []Mention) (string, error) {
	mi := make([]map[string]any, 0, len(mentions))
	for _, m := range mentions {
		typ := 0
		if m.UID == "-1" {
			typ = 1 // @all
		}
		mi = append(mi, map[string]any{
			"pos":  m.Pos,
			"uid":  m.UID,
			"len":  m.Len,
			"type": typ,
		})
	}
	raw, err := json.Marshal(mi)
	if err != nil {
		return "", fmt.Errorf("zalo_personal: encode mentionInfo: %w", err)
	}
	return string(raw), nil
}

// SendMessageWithMention sends a group text with @-mentions via the /mention
// endpoint. Used as the fallback when a quoted+mentioned send fails, so the
// mention survives even if quoting does not.
func SendMessageWithMention(ctx context.Context, sess *Session, threadID string, text string, mentions []Mention) (string, error) {
	if text == "" {
		return "", fmt.Errorf("zalo_personal: message text cannot be empty")
	}
	if len(mentions) == 0 {
		return "", fmt.Errorf("zalo_personal: mentions required")
	}

	baseURL := getServiceURL(sess, "group")
	if baseURL == "" {
		return "", fmt.Errorf("zalo_personal: no service URL for group")
	}

	mentionInfo, err := encodeMentionInfo(mentions)
	if err != nil {
		return "", err
	}
	payload := map[string]any{
		"message":     text,
		"clientId":    time.Now().UnixMilli(),
		"ttl":         0,
		"grid":        threadID,
		"visibility":  0,
		"mentionInfo": mentionInfo,
	}

	encData, err := encryptPayload(sess, payload)
	if err != nil {
		return "", fmt.Errorf("zalo_personal: encrypt mention payload: %w", err)
	}

	sendURL := makeURL(sess, baseURL+"/api/group/mention", map[string]any{"nretry": 0}, true)
	form := buildFormBody(map[string]string{"params": encData})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sendURL, form)
	if err != nil {
		return "", err
	}
	setDefaultHeaders(req, sess)

	resp, err := sess.Client.Do(req)
	if err != nil {
		return "", fmt.Errorf("zalo_personal: send mention: %w", err)
	}
	defer resp.Body.Close()

	var envelope Response[*string]
	if err := readJSON(resp, &envelope); err != nil {
		return "", fmt.Errorf("zalo_personal: parse mention response: %w", err)
	}
	if envelope.ErrorCode != 0 {
		return "", fmt.Errorf("zalo_personal: mention error code %d", envelope.ErrorCode)
	}
	if envelope.Data == nil {
		return "", nil
	}

	plain, err := decryptDataField(sess, *envelope.Data)
	if err != nil {
		return "", fmt.Errorf("zalo_personal: decrypt mention result: %w", err)
	}

	var result struct {
		MsgID json.Number `json:"msgId"`
	}
	if err := json.Unmarshal(plain, &result); err != nil {
		return "", fmt.Errorf("zalo_personal: parse mention result: %w", err)
	}

	return result.MsgID.String(), nil
}
