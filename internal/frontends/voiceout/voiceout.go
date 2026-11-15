// Package voiceout speaks what the assistant marked for speech, and announces when travis is
// waiting on the human (DESIGN.md §5.11). Speaking is an observer: a failure is logged and the
// turn carries on.
package voiceout

import (
	"context"
	"sync"

	"github.com/Kzeezee/travis/internal/core"
)

// Speaker says one line aloud and returns when it has finished or ctx ends.
type Speaker interface {
	Speak(ctx context.Context, text string) error
}

// LineQueuer accepts speech without blocking the caller while it is played.
type LineQueuer interface {
	Say(text string)
}

const (
	// A model that ignores the prompt must not hold the speaker for minutes (§5.11 max_chars).
	maxSpokenRunes = 600
	// Lines beyond this are dropped, not queued: stale speech is worse than none.
	queuedLineLimit = 8

	approvalAlert = "Approval needed."
	questionAlert = "Travis has a question for you."
)

// Narrator speaks queued lines one at a time, so speech and alerts never talk over each other.
type Narrator struct {
	speaker Speaker
	logger  core.Logger
	lines   chan string
	cancel  context.CancelFunc
	done    chan struct{}
	once    sync.Once
}

// Output owns the optional narrator so voice can be turned on and off during a chat.
type Output struct {
	mu           sync.Mutex
	logger       core.Logger
	buildSpeaker func() (Speaker, error)
	narrator     *Narrator
}

// BuildOutput returns disabled voice output backed by this OS's speech program when enabled.
func BuildOutput(logger core.Logger) *Output {
	return &Output{logger: logger, buildSpeaker: BuildSystemSpeaker}
}

// SetEnabled starts or stops speech. Enabling is idempotent and reports a missing system speaker.
func (o *Output) SetEnabled(isEnabled bool) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if isEnabled == (o.narrator != nil) {
		return nil
	}
	if isEnabled {
		speaker, err := o.buildSpeaker()
		if err != nil {
			return err
		}
		o.narrator = StartNarrator(speaker, o.logger)
		return nil
	}
	o.narrator.Close()
	o.narrator = nil
	return nil
}

// ReportIsEnabled answers whether speech is active now.
func (o *Output) ReportIsEnabled() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.narrator != nil
}

// Say queues text when output is enabled and otherwise does nothing.
func (o *Output) Say(text string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.narrator != nil {
		o.narrator.Say(text)
	}
}

// SpeakRequestedSpeech says every SpeechRequested while output is enabled.
func (o *Output) SpeakRequestedSpeech(events <-chan core.Event) {
	for event := range events {
		if speech, ok := event.(core.SpeechRequested); ok {
			o.Say(speech.Text)
		}
	}
}

// Close stops current speech and disables output.
func (o *Output) Close() {
	_ = o.SetEnabled(false)
}

// StartNarrator starts speaking lines as they are queued, until Close.
func StartNarrator(speaker Speaker, logger core.Logger) *Narrator {
	ctx, cancel := context.WithCancel(context.Background()) //nolint:gosec // Close calls cancel; it outlives this function by design
	n := &Narrator{
		speaker: speaker,
		logger:  logger,
		lines:   make(chan string, queuedLineLimit),
		cancel:  cancel,
		done:    make(chan struct{}),
	}
	go n.speakQueuedLines(ctx)
	return n
}

// Say queues text without waiting for it to be spoken; it is dropped when the queue is full.
// ponytail: alerts queue behind speech instead of pre-empting it; add priority if that lags.
func (n *Narrator) Say(text string) {
	select {
	case n.lines <- truncateToRunes(text, maxSpokenRunes):
	default:
		n.logger.LogWarnMsg("speech dropped; the speaker is behind")
	}
}

// SpeakRequestedSpeech says every SpeechRequested on events until the channel closes.
func (n *Narrator) SpeakRequestedSpeech(events <-chan core.Event) {
	for event := range events {
		if speech, ok := event.(core.SpeechRequested); ok {
			n.Say(speech.Text)
		}
	}
}

// Close cuts off the line being spoken and waits for the narrator to stop.
func (n *Narrator) Close() {
	n.once.Do(n.cancel)
	<-n.done
}

func (n *Narrator) speakQueuedLines(ctx context.Context) {
	defer close(n.done)
	for {
		select {
		case <-ctx.Done():
			return
		case line := <-n.lines:
			if err := n.speaker.Speak(ctx, line); err != nil && ctx.Err() == nil {
				n.logger.LogWarnMsg("speech failed", "error", err.Error())
			}
		}
	}
}

func truncateToRunes(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit])
}

// WrapApprover announces every approval request aloud. The answer still comes only from the
// keyboard (D-10): this speaks, it never listens. A nil approver stays nil, so fail-closed holds.
func WrapApprover(next core.Approver, narrator LineQueuer) core.Approver {
	if next == nil {
		return nil
	}
	return alertingApprover{next: next, narrator: narrator}
}

type alertingApprover struct {
	next     core.Approver
	narrator LineQueuer
}

func (a alertingApprover) RequestApproval(ctx context.Context, op core.Operation) (core.ApprovalDecision, error) {
	a.narrator.Say(approvalAlert)
	return a.next.RequestApproval(ctx, op)
}

// WrapQuestioner announces every ask_user question aloud; the answer is still typed.
func WrapQuestioner(next core.Questioner, narrator LineQueuer) core.Questioner {
	if next == nil {
		return nil
	}
	return alertingQuestioner{next: next, narrator: narrator}
}

type alertingQuestioner struct {
	next     core.Questioner
	narrator LineQueuer
}

func (q alertingQuestioner) AskUser(ctx context.Context, question string) (string, error) {
	q.narrator.Say(questionAlert)
	return q.next.AskUser(ctx, question)
}
