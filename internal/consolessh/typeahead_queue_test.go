package consolessh

import "testing"

// The queue's rules on their own, without a session: keys up to the line bound
// are kept in order, the drop notice is owed once per command, and a refusal is
// owed only once the truncated line is the one left in the editor.
func TestTypeAheadQueueDropsPastTheBoundAndOwesOneRefusal(t *testing.T) {
	var q typeAheadQueue
	for i := 0; i < maxLineRunes; i++ {
		if q.push('a') {
			t.Fatalf("push %d reported a drop below the bound", i)
		}
	}
	if !q.push('b') {
		t.Fatal("the first key past the bound was not reported")
	}
	if q.push('c') {
		t.Fatal("a second dropped key reported the notice again")
	}
	if q.takeLost() {
		t.Fatal("refusal owed while keys are still queued")
	}
	n := 0
	for {
		r, ok := q.pop()
		if !ok {
			break
		}
		if r != 'a' {
			t.Fatalf("popped %q, want only the kept keys", r)
		}
		n++
	}
	if n != maxLineRunes {
		t.Fatalf("popped %d keys, want %d", n, maxLineRunes)
	}
	if !q.takeLost() {
		t.Fatal("no refusal owed once the truncated line was reached")
	}
	if q.takeLost() {
		t.Fatal("the refusal was owed twice")
	}

	q.commandEnded()
	for i := 0; i < maxLineRunes; i++ {
		q.push('a')
	}
	if !q.push('a') {
		t.Fatal("the next command's overflow did not owe the notice again")
	}
}

// Ctrl-C drops what was typed ahead and the refusal with it, but the notice
// already printed for this command is not printed again.
func TestTypeAheadQueueClearForgetsTheLostLine(t *testing.T) {
	var q typeAheadQueue
	for i := 0; i <= maxLineRunes; i++ {
		q.push('a')
	}
	q.clear()
	if _, ok := q.pop(); ok {
		t.Fatal("keys survived Ctrl-C")
	}
	if q.takeLost() {
		t.Fatal("a refusal survived Ctrl-C")
	}
	for i := 0; i <= maxLineRunes; i++ {
		if q.push('a') {
			t.Fatal("the drop notice was owed again within the same command")
		}
	}
}
