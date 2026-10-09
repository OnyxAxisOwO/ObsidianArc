package adapter

// Token estimates, for the two places a real count is not available.
//
// The first is a client asking before it sends: /v1/messages/count_tokens,
// which agent clients call to decide when to compact. The second is a
// provider that answered and never said what it cost. Several gateways in
// front of web products do exactly that for some of their models, and the
// ledger then carried a turn that plainly happened as zero tokens — which
// read, to an operator whose free models all sat on such a gateway, as
// "a price of zero makes the tokens zero".
//
// It is an estimate and cannot be anything else. A real count needs the
// tokeniser of the model that answered, which differs per family, and
// carrying one would be both a dependency this project does not take and a
// second thing to keep in step with every model an operator adds. Four bytes
// to the token is the usual approximation: within a fifth or so for English
// and for code, and for CJK it comes out at three quarters of a token per
// character, because each character is three bytes in UTF-8.

const (
	bytesPerToken = 4
	// A small allowance for the framing every message and every tool call
	// carries on the wire.
	perMessageTax  = 4
	perToolCallTax = 8
	// Rather than the bytes, which are base64 and say nothing about how the
	// model will see the picture.
	imageTokenGuess = 1600
)

// EstimatePrompt is roughly what a request costs to read.
func EstimatePrompt(request ChatRequest) int {
	bytes := len(request.System)

	for _, tool := range request.Tools {
		// An agent's tool definitions are often most of its prompt, so
		// leaving them out would understate the total badly.
		bytes += len(tool.Name) + len(tool.Description) + len(tool.Parameters)
	}

	tokens := 0
	for _, message := range request.Messages {
		tokens += perMessageTax
		for _, part := range message.Parts {
			switch part.Kind {
			case PartImage:
				tokens += imageTokenGuess
			case PartToolCall:
				bytes += len(part.ToolName) + len(part.ToolArgs)
				tokens += perToolCallTax
			default:
				bytes += len(part.Text) + len(part.ToolCallID)
			}
		}
	}

	return tokens + bytes/bytesPerToken
}

// fillUsage completes what a provider reported, field by field, and marks the
// result if it had to.
//
// Field by field because some gateways report half: an output count with an
// input of zero, or the other way round. A request that was sent was read,
// so an input of zero is never true; an answer that came back was written,
// so an output of zero beside actual text is not either. Whatever the
// provider did report is kept as it is.
//
// Reasoning is only estimated when output was missing too. Several providers
// count thinking inside the output figure and report no separate number, so
// estimating it beside a reported output would count the same text twice.
func fillUsage(request ChatRequest, result Result, reported Usage) Usage {
	out := reported
	if out.InputTokens == 0 {
		out.InputTokens = EstimatePrompt(request)
		out.Estimated = true
	}

	produced := result.Text != "" || result.Reasoning != "" || len(result.ToolCalls) > 0
	if out.OutputTokens == 0 && out.ReasoningTokens == 0 && produced {
		output := len(result.Text) / bytesPerToken
		for _, call := range result.ToolCalls {
			output += perToolCallTax + (len(call.Name)+len(call.Arguments))/bytesPerToken
		}
		out.OutputTokens = output
		out.ReasoningTokens = len(result.Reasoning) / bytesPerToken
		out.Estimated = true
	}
	return out
}

// written is how much of an answer streamed, in bytes, before its call ended.
type written struct {
	text, reasoning, calls int
}

// fillStopped is fillUsage for a call that did not finish: the reader
// stopped, the connection dropped, or the provider broke off. Nothing is
// charged for a call that wrote nothing, as before — a provider that refused
// outright did not bill either.
//
// What was reported mid-stream is a floor rather than the figure. Anthropic
// sends its output count at the start and again at the end, so a stop in
// between leaves the first one, which is a single token; OpenAI sends it only
// at the end, so a stop leaves nothing. Either way the text that reached the
// reader is the better measure, and the larger of the two is kept. Output and
// reasoning are compared as a sum for the reason fillUsage gives: several
// providers count thinking inside the output figure.
func fillStopped(request ChatRequest, streamed written, reported Usage) Usage {
	if streamed.text == 0 && streamed.reasoning == 0 && streamed.calls == 0 {
		return reported
	}
	out := reported
	if out.InputTokens == 0 {
		out.InputTokens = EstimatePrompt(request)
		out.Estimated = true
	}
	output := (streamed.text + streamed.calls) / bytesPerToken
	reasoning := streamed.reasoning / bytesPerToken
	if out.OutputTokens+out.ReasoningTokens < output+reasoning {
		out.OutputTokens = output
		out.ReasoningTokens = reasoning
		out.Estimated = true
	}
	return out
}
