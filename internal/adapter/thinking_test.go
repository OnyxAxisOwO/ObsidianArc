package adapter

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// The stream hands flushContent the whole answer so far on every delta, so the
// splitter has to give the same answer as one that started from scratch —
// including when a tag arrives split across two of them, which is the entire
// reason the split is re-derived rather than done once.
func TestInlineThinkingMatchesAFreshSplitAtEveryDelta(t *testing.T) {
	cases := map[string]string{
		"no tag at all":             "a plain answer with no reasoning in it",
		"closed at the head":        "<think>weighing it up</think>the answer",
		"still open":                "<think>halfway through a thought",
		"a tag later on":            "some text first <think>then a thought</think> and more",
		"empty reasoning":           "<think></think>answer",
		"looks like a tag":          "the answer mentions <thin and <think-ish text",
		"two opens":                 "<think>first</think>middle<think>second</think>end",
		"close with no open":        "an answer that just says </think> for no reason",
		"literal tag, never closed": "Wrap it in `<think>` tags and retry",
		"nothing":                   "",
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

// The reasoning the stream reports has to add up to the reasoning the result
// keeps. A closing tag that arrives a byte at a time used to send its first
// half as reasoning, and the completed tag then left the result shorter than
// what the client had already been shown.
func TestStreamedReasoningMatchesTheFinalReasoning(t *testing.T) {
	const full = "<think>abc</think>answer"
	frame := func(content string) string {
		encoded, err := json.Marshal(map[string]any{"choices": []any{map[string]any{
			"delta": map[string]any{"content": content},
		}}})
		if err != nil {
			t.Fatal(err)
		}
		return string(encoded)
	}

	var frames []string
	for i := 0; i < len(full); i++ {
		frames = append(frames, frame(full[i:i+1]))
	}
	server := sseServer(t, frames, nil)

	var out collected
	result, err := testRegistry().Chat(context.Background(),
		Provider{Kind: KindOpenAI, BaseURL: server.URL, APIKey: "k"},
		ChatRequest{Model: testModel(), Stream: true, Messages: []Message{{Role: RoleUser, Parts: []Part{{Kind: PartText, Text: "hi"}}}}},
		out.sink)
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	if result.Reasoning != "abc" || result.Text != "answer" {
		t.Fatalf("result = %q / %q, want abc / answer", result.Reasoning, result.Text)
	}
	if got := out.reasoning.String(); got != result.Reasoning {
		t.Errorf("streamed reasoning = %q, want what the result keeps: %q", got, result.Reasoning)
	}
	if got := out.answer.String(); got != result.Text {
		t.Errorf("streamed answer = %q, want %q", got, result.Text)
	}
}

// A tag split across two deltas is held back until it resolves. Shown as
// answer text before it is known to be a tag, "<th" would be in what the
// client got and gone from the final answer.
func TestAPartialOpeningTagIsNotShownAsAnswer(t *testing.T) {
	server := sseServer(t, []string{
		`{"choices":[{"delta":{"content":"Hi <th"}}]}`,
		`{"choices":[{"delta":{"content":"ink>reason</think>There"}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
	}, nil)

	var out collected
	result, err := testRegistry().Chat(context.Background(),
		Provider{Kind: KindOpenAI, BaseURL: server.URL, APIKey: "k"},
		ChatRequest{Model: testModel(), Stream: true, Messages: []Message{{Role: RoleUser, Parts: []Part{{Kind: PartText, Text: "hi"}}}}},
		out.sink)
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	if result.Text != "Hi There" || result.Reasoning != "reason" {
		t.Errorf("result = %q / %q, want the answer and its reasoning", result.Text, result.Reasoning)
	}
	if got := out.answer.String(); got != "Hi There" {
		t.Errorf("streamed answer = %q, want what the final answer is", got)
	}
	if got := out.reasoning.String(); got != "reason" {
		t.Errorf("streamed reasoning = %q, want reason", got)
	}
}

// A literal tag that never closes is part of the answer. Read as the start of
// reasoning, everything the answer says after it would be kept out of the
// answer the client gets.
func TestALiteralTagThatNeverClosesStaysInTheAnswer(t *testing.T) {
	const full = "Wrap it in `<think>` tags and retry"
	encoded, err := json.Marshal(full)
	if err != nil {
		t.Fatal(err)
	}
	userMessage := []Message{{Role: RoleUser, Parts: []Part{{Kind: PartText, Text: "hi"}}}}

	t.Run("a whole response", func(t *testing.T) {
		server := jsonServer(t, `{"choices":[{"message":{"content":`+string(encoded)+`}}]}`, nil)
		var out collected
		result, err := testRegistry().Chat(context.Background(),
			Provider{Kind: KindOpenAI, BaseURL: server.URL, APIKey: "k"},
			ChatRequest{Model: testModel(), Messages: userMessage}, out.sink)
		if err != nil {
			t.Fatalf("chat: %v", err)
		}
		if result.Text != full || result.Reasoning != "" {
			t.Errorf("result = %q / %q, want the whole answer and no reasoning", result.Text, result.Reasoning)
		}
	})

	t.Run("a stream cut at every byte", func(t *testing.T) {
		var frames []string
		for i := 0; i < len(full); i++ {
			frames = append(frames, streamFrame(t, full[i:i+1]))
		}
		server := sseServer(t, frames, nil)

		var out collected
		result, err := testRegistry().Chat(context.Background(),
			Provider{Kind: KindOpenAI, BaseURL: server.URL, APIKey: "k"},
			ChatRequest{Model: testModel(), Stream: true, Messages: userMessage}, out.sink)
		if err != nil {
			t.Fatalf("chat: %v", err)
		}
		if result.Text != full || result.Reasoning != "" {
			t.Errorf("result = %q / %q, want the whole answer and no reasoning", result.Text, result.Reasoning)
		}
		if got := out.answer.String(); got != full {
			t.Errorf("streamed answer = %q, want %q", got, full)
		}
		if got := out.reasoning.String(); got != "" {
			t.Errorf("streamed reasoning = %q, want none", got)
		}
	})
}

// streamFrame is one chat-completions delta carrying content.
func streamFrame(t *testing.T, content string) string {
	t.Helper()
	encoded, err := json.Marshal(map[string]any{"choices": []any{map[string]any{
		"delta": map[string]any{"content": content},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

// The result's reasoning is the field's and the inline's, joined, and a stream
// that fails part-way still reports what arrived before the failure.
func TestReasoningFromBothFormsIsJoinedAndKeptOnFailure(t *testing.T) {
	userMessage := []Message{{Role: RoleUser, Parts: []Part{{Kind: PartText, Text: "hi"}}}}

	t.Run("a finished stream", func(t *testing.T) {
		server := sseServer(t, []string{
			`{"choices":[{"delta":{"reasoning_content":"field "}}]}`,
			`{"choices":[{"delta":{"content":"<think>inline</think>answer"}}]}`,
			`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
		}, nil)

		var out collected
		result, err := testRegistry().Chat(context.Background(),
			Provider{Kind: KindOpenAI, BaseURL: server.URL, APIKey: "k"},
			ChatRequest{Model: testModel(), Stream: true, Messages: userMessage}, out.sink)
		if err != nil {
			t.Fatalf("chat: %v", err)
		}
		if result.Reasoning != "field inline" || result.Text != "answer" {
			t.Errorf("result = %q / %q, want %q / %q", result.Reasoning, result.Text, "field inline", "answer")
		}
		if out.reasoning.String() != result.Reasoning {
			t.Errorf("streamed reasoning %q != final %q", out.reasoning.String(), result.Reasoning)
		}
	})

	t.Run("a stream that fails part-way", func(t *testing.T) {
		server := sseServer(t, []string{
			`{"choices":[{"delta":{"reasoning_content":"field "}}]}`,
			`{"choices":[{"delta":{"content":"<think>inline</think>answer"}}]}`,
			`{"error":{"message":"upstream broke"}}`,
		}, nil)

		var out collected
		result, err := testRegistry().Chat(context.Background(),
			Provider{Kind: KindOpenAI, BaseURL: server.URL, APIKey: "k"},
			ChatRequest{Model: testModel(), Stream: true, Messages: userMessage}, out.sink)
		if err == nil {
			t.Fatal("a stream that broke was not reported")
		}
		if result.Reasoning != "field inline" {
			t.Errorf("reasoning after the failure = %q, want what arrived before it", result.Reasoning)
		}
	})
}

// freshSplit is the rule as plainly as it can be written, kept here as the
// thing the resuming one has to agree with. A tag after visible text is
// reasoning only once it is closed; before that it is held back from the
// answer, and the stream releases it as answer if the close never comes.
func freshSplit(buffer string) (reasoning, answer string) {
	open := strings.Index(buffer, thinkOpen)
	if open < 0 {
		return "", buffer
	}
	rest := buffer[open+len(thinkOpen):]
	end := strings.Index(rest, thinkClose)
	if end < 0 {
		if strings.TrimSpace(buffer[:open]) != "" {
			return "", buffer[:open]
		}
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
