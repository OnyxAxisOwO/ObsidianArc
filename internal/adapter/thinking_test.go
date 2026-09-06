package adapter

import (
	"strings"
	"testing"
)

// The stream hands flushContent the whole answer so far on every delta, so the
// splitter has to give the same answer as one that started from scratch —
// including when a tag arrives split across two of them, which is the entire
// reason the split is re-derived rather than done once.
func TestInlineThinkingMatchesAFreshSplitAtEveryDelta(t *testing.T) {
	cases := map[string]string{
		"no tag at all":      "a plain answer with no reasoning in it",
		"closed at the head": "<think>weighing it up</think>the answer",
		"still open":         "<think>halfway through a thought",
		"a tag later on":     "some text first <think>then a thought</think> and more",
		"empty reasoning":    "<think></think>answer",
		"looks like a tag":   "the answer mentions <thin and <think-ish text",
		"two opens":          "<think>first</think>middle<think>second</think>end",
		"close with no open": "an answer that just says </think> for no reason",
		"nothing":            "",
	}

	// One byte at a time is the worst case for a straddling tag: every tag in
	// every case above is split across as many deltas as it has bytes.
	for name, full := range cases {
		t.Run(name, func(t *testing.T) {
			var streamed inlineThinking
			var buffer strings.Builder
			for i := 0; i < len(full); i++ {
				buffer.WriteByte(full[i])
				gotReasoning, gotAnswer := streamed.split(buffer.String())
				wantReasoning, wantAnswer := freshSplit(buffer.String())
				if gotReasoning != wantReasoning || gotAnswer != wantAnswer {
					t.Fatalf("after %d bytes (%q):\n  streamed = %q / %q\n  fresh    = %q / %q",
						i+1, buffer.String(), gotReasoning, gotAnswer, wantReasoning, wantAnswer)
				}
			}
		})
	}
}

// freshSplit is the original implementation, kept here as the thing the
// resuming one has to agree with.
func freshSplit(buffer string) (reasoning, answer string) {
	open := strings.Index(buffer, thinkOpen)
	if open < 0 {
		return "", buffer
	}
	rest := buffer[open+len(thinkOpen):]
	end := strings.Index(rest, thinkClose)
	if end < 0 {
		return rest, buffer[:open]
	}
	return rest[:end], buffer[:open] + rest[end+len(thinkClose):]
}

// --- what it costs -------------------------------------------------------------
//
// The stream calls split once per delta with everything received so far. A
// splitter that starts over each time has to read the whole buffer to rule a
// tag out, which makes the answer with no tag in it — the ordinary one — the
// expensive case.

func benchmarkThinking(b *testing.B, answerBytes, deltaBytes int, prefix string) {
	delta := strings.Repeat("a", deltaBytes)
	deltas := answerBytes / deltaBytes

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		var splitter inlineThinking
		var content strings.Builder
		content.WriteString(prefix)
		for range deltas {
			content.WriteString(delta)
			_, _ = splitter.split(content.String())
		}
	}
}

const someReasoning = "<think>" + "rrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrr" + "</think>"

func BenchmarkThinkingNoTag256kB(b *testing.B)   { benchmarkThinking(b, 256*1024, 4, "") }
func BenchmarkThinkingWithTag256kB(b *testing.B) { benchmarkThinking(b, 256*1024, 4, someReasoning) }
