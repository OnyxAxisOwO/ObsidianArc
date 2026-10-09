package compat

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

func TestLongToolHistoryReachesTheProvider(t *testing.T) {
	cases := []struct {
		path   string
		field  string
		call   string
		result string
	}{
		{
			path:   "/v1/chat/completions",
			field:  "messages",
			call:   `{"role":"assistant","tool_calls":[{"id":"call_%[1]d","type":"function","function":{"name":"read_file","arguments":"{\"index\":%[1]d}"}}]}`,
			result: `{"role":"tool","tool_call_id":"call_%[1]d","content":"result_%[1]d"}`,
		},
		{
			path:   "/v1/messages",
			field:  "messages",
			call:   `{"role":"assistant","content":[{"type":"tool_use","id":"call_%[1]d","name":"read_file","input":{"index":%[1]d}}]}`,
			result: `{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_%[1]d","content":"result_%[1]d"}]}`,
		},
		{
			path:   "/v1/responses",
			field:  "input",
			call:   `{"type":"function_call","call_id":"call_%[1]d","name":"read_file","arguments":"{\"index\":%[1]d}"}`,
			result: `{"type":"function_call_output","call_id":"call_%[1]d","output":"result_%[1]d"}`,
		},
	}
	for _, c := range cases {
		// Tool loops can cross the old 400-item boundary long before the
		// transcript fills the model's context or the request body allowance.
		for _, rounds := range []int{200, 500} {
			t.Run(fmt.Sprintf("%s/%d_items", c.path, 1+2*rounds), func(t *testing.T) {
				f := newFixture(t)
				f.upstream.reply(answer)

				items := []string{`{"role":"user","content":"start"}`}
				for i := 0; i < rounds; i++ {
					items = append(items, fmt.Sprintf(c.call, i), fmt.Sprintf(c.result, i))
				}
				body := fmt.Sprintf(`{"model":%q,"%s":[%s]}`, f.model.ID, c.field, strings.Join(items, ","))
				w := f.do(t, http.MethodPost, c.path, f.token, body)
				if w.Code != http.StatusOK {
					t.Fatalf("status = %d: %s", w.Code, w.Body.String())
				}

				sent, _ := f.upstream.received()["messages"].([]any)
				if len(sent) != len(items) {
					t.Fatalf("provider received %d messages, want %d", len(sent), len(items))
				}
				first, _ := sent[0].(map[string]any)
				if first["role"] != "user" || first["content"] != "start" {
					t.Fatalf("initial message = %v", first)
				}
				for i := 0; i < rounds; i++ {
					assistant, _ := sent[1+2*i].(map[string]any)
					calls, _ := assistant["tool_calls"].([]any)
					if assistant["role"] != "assistant" || len(calls) != 1 {
						t.Fatalf("round %d: assistant message = %v", i, assistant)
					}
					call, _ := calls[0].(map[string]any)
					function, _ := call["function"].(map[string]any)
					callID := fmt.Sprintf("call_%d", i)
					if call["id"] != callID || function["name"] != "read_file" ||
						function["arguments"] != fmt.Sprintf(`{"index":%d}`, i) {
						t.Fatalf("round %d: tool call = %v", i, call)
					}
					result, _ := sent[2+2*i].(map[string]any)
					if result["role"] != "tool" || result["tool_call_id"] != callID ||
						result["content"] != fmt.Sprintf("result_%d", i) {
						t.Fatalf("round %d: tool result = %v", i, result)
					}
				}

				if c.path == "/v1/messages" {
					count := f.do(t, http.MethodPost, "/v1/messages/count_tokens", f.token, body)
					if count.Code != http.StatusOK {
						t.Fatalf("count_tokens status = %d: %s", count.Code, count.Body.String())
					}
					tokens, _ := decodeJSON(t, count)["input_tokens"].(float64)
					if tokens <= 0 {
						t.Fatalf("count_tokens input_tokens = %v", tokens)
					}
				}
			})
		}
	}
}

func TestTranscriptBodySizeIsStillBounded(t *testing.T) {
	for _, path := range []string{
		"/v1/chat/completions",
		"/v1/messages",
		"/v1/messages/count_tokens",
		"/v1/responses",
	} {
		t.Run(path, func(t *testing.T) {
			f := newFixture(t)
			field := "messages"
			if path == "/v1/responses" {
				field = "input"
			}
			body := fmt.Sprintf(`{"model":%q,"%s":[{"role":"user","content":"%s"}]}`,
				f.model.ID, field, strings.Repeat("x", maxBodyBytes))
			w := f.do(t, http.MethodPost, path, f.token, body)
			if w.Code != http.StatusRequestEntityTooLarge {
				t.Fatalf("status = %d, want 413: %s", w.Code, w.Body.String())
			}
			if f.upstream.received() != nil {
				t.Fatal("oversized request reached the provider")
			}
		})
	}
}

// The tool-count ceiling, across all three protocols.
//
// This is the one that reached users: five different people were refused in a
// week by a limit of 256, which a coding agent with a dozen MCP servers passes
// without doing anything unusual. The ceiling is still here — an unbounded
// tools array is a real way to make a body expensive — so what these pin down
// is that it sits somewhere a normal agent does not reach, that it is the same
// number on all three surfaces, and that the refusal says enough to act on.
func TestToolCeilingIsTheSameOnEverySurface(t *testing.T) {
	cases := []struct {
		path string
		tool string // one tool, %d for its index
	}{
		{"/v1/chat/completions", `{"type":"function","function":{"name":"t%d","parameters":{"type":"object"}}}`},
		{"/v1/messages", `{"name":"t%d","input_schema":{"type":"object"}}`},
		{"/v1/responses", `{"type":"function","name":"t%d","parameters":{"type":"object"}}`},
	}
	for _, c := range cases {
		build := func(n int) string {
			tools := make([]string, n)
			for i := range tools {
				tools[i] = fmt.Sprintf(c.tool, i)
			}
			return strings.Join(tools, ",")
		}

		t.Run(c.path+"/at_the_ceiling", func(t *testing.T) {
			f := newFixture(t)
			f.upstream.reply(answer)
			body := fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"hi"}],"input":[{"role":"user","content":"hi"}],"tools":[%s]}`,
				f.model.ID, build(maxTools))
			w := f.do(t, http.MethodPost, c.path, f.token, body)
			if w.Code != http.StatusOK {
				t.Fatalf("exactly maxTools was refused: status = %d: %s", w.Code, w.Body.String())
			}
			sent, _ := f.upstream.received()["tools"].([]any)
			if len(sent) != maxTools {
				t.Fatalf("provider received %d tools, want %d", len(sent), maxTools)
			}
		})

		t.Run(c.path+"/one_over", func(t *testing.T) {
			f := newFixture(t)
			f.upstream.reply(answer)
			body := fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"hi"}],"input":[{"role":"user","content":"hi"}],"tools":[%s]}`,
				f.model.ID, build(maxTools+1))
			w := f.do(t, http.MethodPost, c.path, f.token, body)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", w.Code, w.Body.String())
			}
			// Both numbers, or the person reading it cannot tell 1025 from
			// 5000 and the operator has to ask them to count.
			got := w.Body.String()
			for _, want := range []string{strconv.Itoa(maxTools + 1), strconv.Itoa(maxTools)} {
				if !strings.Contains(got, want) {
					t.Fatalf("refusal does not name %q: %s", want, got)
				}
			}
		})
	}
}

// Namespaces are flattened before the count, so what matters is how many
// functions the model ends up being offered, not how many entries the client
// typed. Codex sends its sub-agent tools this way.
func TestResponsesCountsFlattenedToolsAgainstTheCeiling(t *testing.T) {
	f := newFixture(t)
	f.upstream.reply(answer)

	// One namespace, maxTools+1 functions inside it: a single top-level entry
	// that is still over the line.
	inner := make([]string, maxTools+1)
	for i := range inner {
		inner[i] = fmt.Sprintf(`{"type":"function","name":"t%d","parameters":{"type":"object"}}`, i)
	}
	body := fmt.Sprintf(
		`{"model":%q,"input":[{"role":"user","content":"hi"}],"tools":[{"type":"namespace","name":"agents","tools":[%s]}]}`,
		f.model.ID, strings.Join(inner, ","))

	w := f.do(t, http.MethodPost, "/v1/responses", f.token, body)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), strconv.Itoa(maxTools+1)) {
		t.Fatalf("refusal counted the namespace rather than what is inside it: %s", w.Body.String())
	}
}

// A tool_result holds text and images. A result inside one, or a call, is
// refused before any level below it is read. Both paths read content through
// the same function, and count_tokens is the one that never reserves, so
// nothing in front of it bounds how much a request can make the server hold.
func TestNestedToolResultsAreRefusedOnBothPaths(t *testing.T) {
	cases := []struct {
		name    string
		content string
	}{
		{"result inside a result", resultChain(2)},
		{"call inside a result", `[{"type":"tool_result","tool_use_id":"outer",` +
			`"content":[{"type":"tool_use","id":"inner","name":"read_file","input":{}}]}]`},
		{"chain 200 levels deep", resultChain(200)},
	}
	for _, path := range []string{"/v1/messages", "/v1/messages/count_tokens"} {
		for _, c := range cases {
			t.Run(path+"/"+c.name, func(t *testing.T) {
				f := newFixture(t)
				f.upstream.reply(answer)

				w := f.do(t, http.MethodPost, path, f.token, userMessageBody(f.model.ID, c.content))
				if w.Code != http.StatusBadRequest {
					t.Fatalf("status = %d, want 400: %s", w.Code, w.Body.String())
				}
				envelope, _ := decodeJSON(t, w)["error"].(map[string]any)
				if envelope["type"] != "invalid_request_error" {
					t.Errorf("error type = %v, want invalid_request_error", envelope["type"])
				}
				// Matched on the wording as well: a body that fails to parse is a
				// 400 too, and would pass this test for the wrong reason.
				if message, _ := envelope["message"].(string); !strings.Contains(message, "tool_result") {
					t.Errorf("refusal does not say what was wrong: %q", message)
				}
				if f.upstream.received() != nil {
					t.Error("a refused request reached the provider")
				}
				if turns := f.turns(); len(turns) != 0 {
					t.Errorf("a refused request recorded %d turns", len(turns))
				}
			})
		}
	}
}

// The refusal is about what nests, not about results: a tool that answers with
// text and a picture is still answered on both paths.
func TestToolResultWithTextAndImageIsStillAccepted(t *testing.T) {
	f := newFixture(t)
	f.upstream.reply(answer)

	body := `{"model":"` + f.model.ID + `","max_tokens":16,"messages":[` +
		`{"role":"user","content":"what is on screen?"},` +
		`{"role":"assistant","content":[{"type":"tool_use","id":"toolu_x","name":"screenshot","input":{}}]},` +
		`{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_x","content":[` +
		`{"type":"text","text":"screen captured"},` +
		`{"type":"image","source":{"type":"base64","media_type":"image/png","data":"` + testPNG + `"}}]}]}]}`

	w := f.do(t, http.MethodPost, "/v1/messages", f.token, body)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	sent, _ := f.upstream.received()["messages"].([]any)
	if len(sent) != 3 {
		t.Fatalf("provider received %v, want three messages", f.upstream.received()["messages"])
	}
	result, _ := sent[2].(map[string]any)
	if result["role"] != "tool" || result["content"] != "screen captured" {
		t.Errorf("result reached the provider as %v", result)
	}

	count := f.do(t, http.MethodPost, "/v1/messages/count_tokens", f.token, body)
	if count.Code != http.StatusOK {
		t.Fatalf("count_tokens status = %d: %s", count.Code, count.Body.String())
	}
	if tokens, _ := decodeJSON(t, count)["input_tokens"].(float64); tokens <= 0 {
		t.Errorf("count_tokens input_tokens = %v", decodeJSON(t, count)["input_tokens"])
	}
}

// resultChain is a tool_result nested depth levels deep, with the payload at the
// bottom. That is the shape that multiplies memory in a decoder which copies
// its children at every level.
func resultChain(depth int) string {
	var b strings.Builder
	b.WriteString("[")
	for i := 0; i < depth; i++ {
		fmt.Fprintf(&b, `{"type":"tool_result","tool_use_id":"call_%d","content":[`, i)
	}
	b.WriteString(`{"type":"text","text":"payload"}`)
	for i := 0; i < depth; i++ {
		b.WriteString(`]}`)
	}
	b.WriteString("]")
	return b.String()
}

func userMessageBody(modelID, content string) string {
	return `{"model":"` + modelID + `","max_tokens":16,"messages":[{"role":"user","content":` +
		content + `}]}`
}
