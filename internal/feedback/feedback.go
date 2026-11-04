// Package feedback is what the people using the instance have to say about
// it: a defect they hit, or something they would like it to do.
//
// Rows rather than a mail relay, for the same reason announcements are rows:
// "what have people told us, and what did we do about it" is worth being
// able to look up afterwards, and a report that arrives in one operator's
// mailbox is a report the next operator never sees.
//
// A report is a conversation: both sides reply, and both write Markdown. It
// is stored exactly as typed and rendered by `chat/markdown.ts`, which builds
// nodes and never assembles an HTML string — so there is nothing to sanitise
// on the way in, and a report written by a stranger is safe to render in the
// operator's own screen. That renderer also declines to emit <img>, which is
// what stops a reported "bug" from turning every operator who reads it into a
// hit on somebody's tracker.
package feedback

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/text"
)

// Kind is what the reader is telling us: something is broken, or something
// could be better. Two, because a third ("question") would be a support
// channel, and this is not one.
type Kind string

const (
	KindBug  Kind = "bug"
	KindIdea Kind = "idea"
)

var Kinds = []Kind{KindBug, KindIdea}

func (k Kind) Valid() bool {
	return slices.Contains(Kinds, k)
}

// Priority is the reporter's own estimate of how much it matters. It is
// deliberately theirs rather than the operator's: what an operator thinks
// belongs in Status, and the gap between the two is the interesting part.
type Priority string

const (
	PriorityLow    Priority = "low"
	PriorityMedium Priority = "medium"
	PriorityHigh   Priority = "high"
)

var Priorities = []Priority{PriorityLow, PriorityMedium, PriorityHigh}

func (p Priority) Valid() bool {
	return slices.Contains(Priorities, p)
}

// Status is the operator's half. Two values, not a workflow: a report has
// either been dealt with or it has not, and every extra state is a state
// somebody has to remember to move things out of.
type Status string

const (
	StatusOpen     Status = "open"
	StatusResolved Status = "resolved"
)

var Statuses = []Status{StatusOpen, StatusResolved}

func (s Status) Valid() bool {
	return slices.Contains(Statuses, s)
}

const (
	MaxTitleChars = 120
	// Long enough for reproduction steps and a stack trace pasted in, short
	// enough that one report cannot be a denial-of-service on the table.
	MaxBodyChars = 8000
	// Per account, per rolling day. High enough that nobody reporting real
	// defects will meet it, low enough that a bored account cannot fill the
	// operator's list faster than it can be read.
	MaxPerDay = 10
	// Per thread, both sides together. A conversation that has run this long
	// is one that should have become a defect somewhere else, and the cap is
	// what stops a thread growing without bound in a table everybody's list
	// query touches.
	MaxRepliesPerThread = 50
	// How many of an author's newest reports ListFor returns, beside the ones
	// still holding an unread answer. The account page counts the day's reports
	// from that list, so it has to reach MaxPerDay, or the count reads high.
	listedNewest = 50
)

// DayWindow is the span MaxPerDay is counted over.
const DayWindow = 24 * time.Hour

var (
	ErrNotFound      = errors.New("feedback: not found")
	ErrInvalidTitle  = fmt.Errorf("feedback: title must be 1-%d characters", MaxTitleChars)
	ErrInvalidBody   = fmt.Errorf("feedback: body must be 1-%d characters", MaxBodyChars)
	ErrInvalidKind   = errors.New("feedback: unknown kind")
	ErrInvalidPrio   = errors.New("feedback: unknown priority")
	ErrInvalidStatus = errors.New("feedback: unknown status")
	ErrTooMany       = errors.New("feedback: this account has sent its daily maximum")
	ErrThreadFull    = errors.New("feedback: this conversation has reached its maximum length")
)

type Feedback struct {
	ID        string   `json:"id"`
	UserID    string   `json:"user_id"`
	Kind      Kind     `json:"kind"`
	Priority  Priority `json:"priority"`
	Status    Status   `json:"status"`
	Title     string   `json:"title"`
	Body      string   `json:"body"`
	CreatedAt int64    `json:"created_at"`
	UpdatedAt int64    `json:"updated_at"`

	// How long the conversation is, and whether the side reading this list
	// has seen its last word. AuthorUnread is what puts a dot on the reader's
	// account menu; OperatorUnread is what marks a thread as waiting for an
	// answer in the backoffice.
	Replies        int  `json:"replies"`
	AuthorUnread   bool `json:"author_unread"`
	OperatorUnread bool `json:"operator_unread"`

	// Filled for the operator's listing, empty for the author's own — they
	// know who they are, and the join is not free.
	Username string `json:"username,omitempty"`
	Nickname string `json:"nickname,omitempty"`
}

// Reply is one turn in a thread. Both sides write Markdown, and it is stored
// exactly as typed: it is rendered by the same node-building renderer the
// transcript uses, which never assembles an HTML string, so there is nothing
// here to sanitise on the way in and nothing to regret on the way out.
type Reply struct {
	ID         string `json:"id"`
	FeedbackID string `json:"feedback_id"`
	UserID     string `json:"user_id"`
	FromStaff  bool   `json:"from_staff"`
	Body       string `json:"body"`
	CreatedAt  int64  `json:"created_at"`

	Username string `json:"username,omitempty"`
	Nickname string `json:"nickname,omitempty"`
}

// Thread is a report and everything said about it since.
type Thread struct {
	Feedback Feedback `json:"feedback"`
	Replies  []Reply  `json:"replies"`
}

type Input struct {
	Kind     Kind
	Priority Priority
	Title    string
	Body     string
}

// The text is cleaned before it is counted or stored: PostgreSQL refuses a NUL
// byte, and the insert would fail as a 500 with the author's words lost.
func validate(in Input) (Input, error) {
	in.Title = strings.TrimSpace(text.Clean(in.Title))
	if in.Title == "" || utf8.RuneCountInString(in.Title) > MaxTitleChars {
		return Input{}, ErrInvalidTitle
	}
	in.Body = strings.TrimSpace(text.Clean(in.Body))
	if in.Body == "" || utf8.RuneCountInString(in.Body) > MaxBodyChars {
		return Input{}, ErrInvalidBody
	}
	if in.Kind == "" {
		in.Kind = KindBug
	}
	if !in.Kind.Valid() {
		return Input{}, ErrInvalidKind
	}
	if in.Priority == "" {
		in.Priority = PriorityMedium
	}
	if !in.Priority.Valid() {
		return Input{}, ErrInvalidPrio
	}
	return in, nil
}
