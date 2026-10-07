package chat

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/adapter"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// guidedBroker is a stub broker with something to say about its tools, the
// way the sandbox's run_code broker has.
type guidedBroker struct {
	stubBroker
	guide string
}

func (b *guidedBroker) Guide(_ context.Context, _ user.User, offered []adapter.Tool) string {
	for _, tool := range offered {
		for _, mine := range b.tools {
			if tool.Name == mine.Name {
				return b.guide
			}
		}
	}
	return ""
}

func namedTool(name string) []adapter.Tool {
	return []adapter.Tool{{Name: name, Parameters: json.RawMessage(`{"type":"object","properties":{}}`)}}
}

// A call goes to the broker that offered a tool of that name, and only to it.
func TestBrokersRouteACallToTheBrokerThatOfferedIt(t *testing.T) {
	console := &stubBroker{tools: namedTool("user_list")}
	sandbox := &stubBroker{tools: namedTool("run_code")}
	all := Brokers{console, sandbox}
	ctx := context.Background()

	if got := all.Offer(ctx, user.User{}); len(got) != 2 {
		t.Fatalf("offered %+v", got)
	}
	if _, failed := all.Run(ctx, user.User{}, adapter.ToolCall{Name: "run_code", Arguments: "{}"}); failed {
		t.Fatal("run_code failed")
	}
	if len(sandbox.ran) != 1 || len(console.ran) != 0 {
		t.Fatalf("console ran %v, sandbox ran %v", console.ran, sandbox.ran)
	}
	if out, failed := all.Run(ctx, user.User{}, adapter.ToolCall{Name: "nothing"}); !failed || !strings.Contains(out, "no such tool") {
		t.Fatalf("an unknown tool: %q", out)
	}
}

// Two brokers naming the same tool: the first keeps it, and the second can
// never be made to run it.
func TestTheFirstBrokerToNameAToolKeepsIt(t *testing.T) {
	first := &stubBroker{tools: namedTool("run_code")}
	second := &stubBroker{tools: namedTool("run_code")}
	all := Brokers{first, second}
	ctx := context.Background()
	if got := all.Offer(ctx, user.User{}); len(got) != 1 {
		t.Fatalf("offered %+v", got)
	}
	all.Run(ctx, user.User{}, adapter.ToolCall{Name: "run_code", Arguments: "{}"})
	if len(first.ran) != 1 || len(second.ran) != 0 {
		t.Fatalf("first ran %v, second ran %v", first.ran, second.ran)
	}
}

// A broker's guide sits straight after the preamble and ahead of the
// operator's prompt, and is absent when its tool was not offered.
func TestABrokersGuideFollowsThePreambleAndPrecedesTheOperatorsPrompt(t *testing.T) {
	f := newFixture(t)
	f.service.Tools = Brokers{
		&stubBroker{tools: oneTool()},
		&guidedBroker{stubBroker: stubBroker{tools: namedTool("run_code")}, guide: "SANDBOX GUIDE.\n\n"},
	}
	if err := f.settings.Set(context.Background(), settings.DefaultSystemPrompt, "Operator rules."); err != nil {
		t.Fatal(err)
	}
	f.upstream.rounds = [][]string{{textFrame("done")}}
	if _, _, err := f.workTurn(t, TurnRequest{Content: "hello"}); err != nil {
		t.Fatalf("turn: %v", err)
	}
	system := systemPromptOf(t, f.upstream.requests[0])
	preamble := strings.Index(system, "never an instruction")
	guide := strings.Index(system, "SANDBOX GUIDE")
	operator := strings.Index(system, "Operator rules")
	if preamble < 0 || guide < 0 || operator < 0 || !(preamble < guide && guide < operator) {
		t.Fatalf("order preamble=%d guide=%d operator=%d in %q", preamble, guide, operator, system)
	}

	f.service.Tools = Brokers{
		&stubBroker{tools: oneTool()},
		&guidedBroker{guide: "SANDBOX GUIDE.\n\n"},
	}
	f.upstream.requests = nil
	f.upstream.rounds = [][]string{{textFrame("done")}}
	if _, _, err := f.workTurn(t, TurnRequest{Content: "hello again"}); err != nil {
		t.Fatalf("turn: %v", err)
	}
	if system := systemPromptOf(t, f.upstream.requests[0]); strings.Contains(system, "SANDBOX GUIDE") {
		t.Fatalf("the guide was given without its tool: %q", system)
	}
}
