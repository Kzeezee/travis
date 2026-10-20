package loop

import (
	"context"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/Kzeezee/travis/internal/core"
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

// speechFallbackPrompt asks for what tier 2 failed to get: a reply the model marked nothing in
// still deserves one spoken line (§5.11 tier 3).
const speechFallbackPrompt = "Say in one short spoken sentence what this reply tells the user. " +
	"Plain words only: no code, paths, lists, or markup."

// A local model can take a while; past this the moment for the line has gone.
const speechFallbackTimeout = 30 * time.Second

// startSpeechFallback asks the provider for a one-line spoken summary of reply and publishes it.
// It runs after the turn has returned, so the user can type meanwhile; it offers no tools, and
// speech is an observer, so a failure just stays silent.
func (c *AssistantConversation) startSpeechFallback(ctx context.Context, reply string) {
	header, provider := c.buildEventHeader(), c.provider
	request := core.ProviderRequest{
		Model:    c.model,
		System:   speechFallbackPrompt,
		Messages: []core.Message{{Role: core.RoleUser, Parts: []core.Part{core.TextPart{Text: reply}}}},
		// A summary of text that already exists needs no thinking; with it a local model takes ~10x longer.
		ShouldSkipReasoning: true,
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), speechFallbackTimeout)
	go func() {
		defer cancel()
		summary, err := collectStreamText(ctx, provider, request)
		if errors.Is(err, core.ErrInvalid) {
			// An endpoint may reject the skip-reasoning switch; slower speech beats none.
			request.ShouldSkipReasoning = false
			summary, err = collectStreamText(ctx, provider, request)
		}
		if err != nil {
			return
		}
		if text := extractSpokenText(summary); text != "" {
			c.events.Publish(ctx, core.SpeechRequested{EventHeader: header, Text: text})
		}
	}()
}

func collectStreamText(ctx context.Context, provider core.Provider, request core.ProviderRequest) (string, error) {
	stream, err := provider.Chat(ctx, request)
	if err != nil {
		return "", err
	}
	defer func() { _ = stream.Close() }()
	var text strings.Builder
	for {
		chunk, err := stream.Next()
		if errors.Is(err, io.EOF) {
			return text.String(), nil
		}
		if err != nil {
			return "", err
		}
		text.WriteString(chunk.Text)
	}
}

// extractSpokenText keeps only the tagged part when the model tagged its summary anyway.
func extractSpokenText(summary string) string {
	var extractor speechExtractor
	shown, speeches := extractor.feed(summary)
	rest, more := extractor.flush()
	if speeches = append(speeches, more...); len(speeches) > 0 {
		return strings.Join(speeches, " ")
	}
	return strings.TrimSpace(shown + rest)
}
