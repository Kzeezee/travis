package loop

import (
	"reflect"
	"strings"
	"testing"
)

func runSpeechExtractor(chunks ...string) (shown string, speeches []string) {
	var extractor speechExtractor
	var out strings.Builder
	for _, chunk := range chunks {
		text, spoken := extractor.feed(chunk)
		out.WriteString(text)
		speeches = append(speeches, spoken...)
	}
	text, spoken := extractor.flush()
	out.WriteString(text)
	return out.String(), append(speeches, spoken...)
}

func TestSpeechExtractorSpeaksOnlyTaggedText(t *testing.T) {
	shown, speeches := runSpeechExtractor("Here it is. <speak>Three tests fail.</speak> diff follows")

	if shown != "Here it is. Three tests fail. diff follows" {
		t.Fatalf("shown = %q", shown)
	}
	if !reflect.DeepEqual(speeches, []string{"Three tests fail."}) {
		t.Fatalf("speeches = %q", speeches)
	}
}

func TestSpeechExtractorDoesNotShowASpanThatRepeatsShownText(t *testing.T) {
	shown, speeches := runSpeechExtractor("The capital is Paris.\n\n<speak>The capital  is Paris.</speak>")

	if strings.TrimSpace(shown) != "The capital is Paris." {
		t.Fatalf("shown = %q, want the answer once", shown)
	}
	if !reflect.DeepEqual(speeches, []string{"The capital  is Paris."}) {
		t.Fatalf("speeches = %q", speeches)
	}
}

func TestSpeechExtractorKeepsUntaggedTextSilent(t *testing.T) {
	shown, speeches := runSpeechExtractor("no tags ", "at all")

	if shown != "no tags at all" || len(speeches) != 0 {
		t.Fatalf("shown = %q, speeches = %q", shown, speeches)
	}
}

func TestSpeechExtractorHandlesTagsSplitAcrossChunks(t *testing.T) {
	chunks := []string{"a <sp", "eak>one ", "two</spe", "ak> b <", "speak>three</speak>"}

	shown, speeches := runSpeechExtractor(chunks...)

	if shown != "a one two b three" {
		t.Fatalf("shown = %q", shown)
	}
	if !reflect.DeepEqual(speeches, []string{"one two", "three"}) {
		t.Fatalf("speeches = %q", speeches)
	}
}

func TestSpeechExtractorNeverShowsAHalfTag(t *testing.T) {
	var extractor speechExtractor

	shown, _ := extractor.feed("done <spe")

	if shown != "done " {
		t.Fatalf("shown = %q, want the possible tag start held back", shown)
	}
}

func TestSpeechExtractorShowsAnUnclosedSpanAtTheEnd(t *testing.T) {
	shown, _ := runSpeechExtractor("a <speak>cut off")

	if shown != "a cut off" {
		t.Fatalf("shown = %q", shown)
	}
}

func TestSpeechExtractorReleasesAHeldBackFalseStart(t *testing.T) {
	shown, speeches := runSpeechExtractor("x <", "b> y", " 1 <")

	if shown != "x <b> y 1 <" || len(speeches) != 0 {
		t.Fatalf("shown = %q, speeches = %q", shown, speeches)
	}
}

func TestSpeechExtractorSpeaksAnUnclosedSpanAtTheEnd(t *testing.T) {
	_, speeches := runSpeechExtractor("<speak>cut off mid")

	if !reflect.DeepEqual(speeches, []string{"cut off mid"}) {
		t.Fatalf("speeches = %q", speeches)
	}
}

func TestSpeechExtractorSkipsBlankSpans(t *testing.T) {
	_, speeches := runSpeechExtractor("<speak>  </speak>")

	if len(speeches) != 0 {
		t.Fatalf("speeches = %q, want none", speeches)
	}
}
