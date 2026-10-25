package loop

import (
	"regexp"
	"strings"
)

const (
	speakOpenTag  = "<speak>"
	speakCloseTag = "</speak>"
)

// speechExtractor splits streamed assistant text into what is shown and what is spoken
// (§5.11 tier 2). Tags are removed from the shown text but their contents stay in it; only
// the text inside them is spoken. A tag split across chunks is held back until the next
// chunk settles it, so no half-tag ever reaches the screen. A spoken span is held until it
// closes, and is not shown at all when it repeats text already shown: small models often
// answer, then repeat the answer inside the tags as a spoken summary.
type speechExtractor struct {
	pending    string
	shown      strings.Builder
	spoken     strings.Builder
	isSpeaking bool
}

// feed returns the text to show for chunk and every speech span the chunk closed.
func (e *speechExtractor) feed(chunk string) (shown string, speeches []string) {
	var out strings.Builder
	rest := e.pending + chunk
	e.pending = ""
	for {
		tag := speakOpenTag
		if e.isSpeaking {
			tag = speakCloseTag
		}
		at := strings.Index(rest, tag)
		if at < 0 {
			held := measurePartialTagSuffix(rest, tag)
			e.emit(&out, rest[:len(rest)-held])
			e.pending = rest[len(rest)-held:]
			return out.String(), speeches
		}
		e.emit(&out, rest[:at])
		rest = rest[at+len(tag):]
		if e.isSpeaking {
			speeches = e.closeSpan(&out, speeches)
		}
		e.isSpeaking = !e.isSpeaking
	}
}

// flush ends the stream: held-back text is shown, and a span the model never closed is
// still spoken rather than lost.
func (e *speechExtractor) flush() (shown string, speeches []string) {
	var out strings.Builder
	e.emit(&out, e.pending)
	if e.isSpeaking {
		speeches = e.closeSpan(&out, speeches)
	}
	*e = speechExtractor{}
	return out.String(), speeches
}

func (e *speechExtractor) emit(out *strings.Builder, text string) {
	if e.isSpeaking {
		e.spoken.WriteString(text)
		return
	}
	out.WriteString(text)
	e.shown.WriteString(text)
}

// closeSpan shows the finished span unless it repeats shown text, and queues it to be spoken.
func (e *speechExtractor) closeSpan(out *strings.Builder, speeches []string) []string {
	span := e.spoken.String()
	e.spoken.Reset()
	if !strings.Contains(collapseSpaces(e.shown.String()), collapseSpaces(span)) {
		out.WriteString(span)
		e.shown.WriteString(span)
	}
	if text := strings.TrimSpace(span); text != "" {
		speeches = append(speeches, text)
	}
	return speeches
}

func collapseSpaces(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

// measurePartialTagSuffix reports how many trailing bytes of text could be the start of tag.
func measurePartialTagSuffix(text, tag string) int {
	for n := min(len(text), len(tag)-1); n > 0; n-- {
		if strings.HasSuffix(text, tag[:n]) {
			return n
		}
	}
	return 0
}

var (
	lineMarkerPattern   = regexp.MustCompile(`^(#{1,6}|>|[-*+]|\d+[.)])\s+`)
	sentenceEndPattern  = regexp.MustCompile(`[.!?](\s|$)`)
	inlineCodePattern   = regexp.MustCompile("^`[^`]+`[.!?:]?$")
	markdownLinkPattern = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	emphasisRemover     = strings.NewReplacer("**", "", "__", "", "~~", "", "*", "", "`", "")
	speakTagRemover     = strings.NewReplacer(speakOpenTag, "", speakCloseTag, "")
)

// pickSpokenSentence chooses what to say for a reply the model marked nothing in (§5.11 tier
// 3): its first displayed sentence as plain words, or nothing when that is code or a path.
// A second model call was tried here and dropped: small models answer the reply instead of
// summarising it.
// ponytail: a line or ". " ends a sentence, so "e.g. this" is cut short and hard-wrapped text
// is spoken only up to the wrap.
func pickSpokenSentence(reply string) string {
	for _, line := range strings.Split(speakTagRemover.Replace(reply), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if strings.HasPrefix(line, "    ") || strings.HasPrefix(line, "\t") || strings.HasPrefix(strings.TrimSpace(line), "```") {
			return ""
		}
		sentence := lineMarkerPattern.ReplaceAllString(strings.TrimSpace(line), "")
		if end := sentenceEndPattern.FindStringIndex(sentence); end != nil {
			sentence = sentence[:end[0]+1]
		}
		if inlineCodePattern.MatchString(sentence) {
			return ""
		}
		sentence = strings.TrimSpace(emphasisRemover.Replace(markdownLinkPattern.ReplaceAllString(sentence, "$1")))
		if !strings.ContainsAny(sentence, " \t") && strings.ContainsAny(sentence, "/\\") {
			return ""
		}
		return sentence
	}
	return ""
}
