package personal

import (
	"strings"
	"unicode"
	"unicode/utf16"

	"github.com/nextlevelbuilder/goclaw/internal/channels/zalo/personal/protocol"
)

var commonHonorifics = map[string]struct{}{
	"anh":  {},
	"chị":  {},
	"chi":  {},
	"em":   {},
	"bác":  {},
	"bac":  {},
	"chú":  {},
	"chu":  {},
	"cô":   {},
	"co":   {},
	"thầy": {},
	"thay": {},
	"sếp":  {},
	"sep":  {},
	"bạn":  {},
	"ban":  {},
	"mr":   {},
	"mrs":  {},
	"ms":   {},
	"dr":   {},
}

// prepareGroupMentions inspects outbound group text and ensures the target user is @mentioned:
// 1. If text already contains "@<name>" or "@<honorific> <name>" (case-insensitive at word boundaries),
//    all occurrences are converted into protocol.Mention spans with accurate UTF-16 offsets without prepending.
// 2. If text starts with an '@' mention tag (e.g. "@all", "@everyone", or another mention),
//    we avoid prepending a duplicate or conflicting "@<name> " prefix.
// 3. Otherwise (no mention of user in text), "@<name> " is prepended at index 0 and a
//    mention span is registered at Pos: 0.
func prepareGroupMentions(content, senderID, senderName string) (string, []protocol.Mention) {
	if senderID == "" || content == "" {
		return content, nil
	}
	senderName = strings.TrimSpace(strings.TrimPrefix(senderName, "@"))
	if senderName == "" {
		senderName = senderID
	}

	mentions := findMentions(content, senderID, senderName)
	if len(mentions) > 0 {
		return content, mentions
	}

	trimmed := strings.TrimLeft(content, " \t\r\n")
	if strings.HasPrefix(trimmed, "@") {
		return content, nil
	}

	tag := "@" + senderName
	newContent := tag + " " + content
	return newContent, []protocol.Mention{
		{
			Pos: 0,
			Len: utf16Len(tag),
			UID: senderID,
		},
	}
}

// findMentions locates all occurrences of "@<senderName>" or "@<honorific> <senderName>"
// in content and returns their protocol.Mention representations with UTF-16 Pos and Len.
func findMentions(content, senderID, senderName string) []protocol.Mention {
	if senderID == "" || content == "" {
		return nil
	}
	senderName = strings.TrimSpace(strings.TrimPrefix(senderName, "@"))
	if senderName == "" {
		return nil
	}

	contentRunes := []rune(content)
	nameRunes := []rune(senderName)
	if len(nameRunes) == 0 {
		return nil
	}

	var mentions []protocol.Mention

	// Precompute cumulative UTF-16 code-unit offsets for each rune index.
	// utf16Offsets[i] is the UTF-16 offset of contentRunes[i].
	utf16Offsets := make([]int, len(contentRunes)+1)
	currentU16 := 0
	for i, r := range contentRunes {
		utf16Offsets[i] = currentU16
		currentU16 += utf16RuneLen(r)
	}
	utf16Offsets[len(contentRunes)] = currentU16

	for i := 0; i < len(contentRunes); i++ {
		// Must start with '@'
		if contentRunes[i] != '@' {
			continue
		}

		// Boundary check before '@': start of string or delimiter (not a word character)
		if i > 0 && isWordRune(contentRunes[i-1]) {
			continue
		}

		matchedEndRune := -1

		// 1. Exact match check: "@<senderName>"
		tagLenRunes := 1 + len(nameRunes)
		if i+tagLenRunes <= len(contentRunes) {
			candidate := contentRunes[i+1 : i+tagLenRunes]
			if strings.EqualFold(string(candidate), senderName) {
				afterIdx := i + tagLenRunes
				if afterIdx == len(contentRunes) || !isWordRune(contentRunes[afterIdx]) {
					matchedEndRune = afterIdx
				}
			}
		}

		// 2. If exact match not found, check honorific: "@<honorific> <senderName>"
		if matchedEndRune == -1 {
			j := i + 1
			for j < len(contentRunes) && isWordRune(contentRunes[j]) {
				j++
			}
			if j > i+1 {
				word := strings.ToLower(string(contentRunes[i+1 : j]))
				if _, isHonorific := commonHonorifics[word]; isHonorific {
					hasSep := false
					hEnd := j
					if hEnd < len(contentRunes) && contentRunes[hEnd] == '.' {
						hEnd++
						hasSep = true
					}
					k := hEnd
					for k < len(contentRunes) && (contentRunes[k] == ' ' || contentRunes[k] == '\t') {
						k++
						hasSep = true
					}
					if hasSep && k+len(nameRunes) <= len(contentRunes) {
						candidate := contentRunes[k : k+len(nameRunes)]
						if strings.EqualFold(string(candidate), senderName) {
							afterIdx := k + len(nameRunes)
							if afterIdx == len(contentRunes) || !isWordRune(contentRunes[afterIdx]) {
								matchedEndRune = afterIdx
							}
						}
					}
				}
			}
		}

		if matchedEndRune != -1 {
			pos := utf16Offsets[i]
			tagU16Len := utf16Offsets[matchedEndRune] - pos

			mentions = append(mentions, protocol.Mention{
				Pos: pos,
				Len: tagU16Len,
				UID: senderID,
			})

			// Advance past this match (for loop will i++)
			i = matchedEndRune - 1
		}
	}

	return mentions
}

// filterMentionsForChunk returns only mentions that fall completely within chunk's UTF-16 length.
func filterMentionsForChunk(mentions []protocol.Mention, chunk string) []protocol.Mention {
	if len(mentions) == 0 {
		return nil
	}
	chunkU16Len := utf16Len(chunk)
	var filtered []protocol.Mention
	for _, m := range mentions {
		if m.Pos >= 0 && m.Pos+m.Len <= chunkU16Len {
			filtered = append(filtered, m)
		}
	}
	return filtered
}

func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}

func utf16RuneLen(r rune) int {
	return utf16.RuneLen(r)
}
