// Package flight merges concurrent calls for the same key into one: the first
// caller runs the function and everyone who arrives while it runs gets its
// result.
//
// It exists so that the four places that need this do not need a fifth direct
// dependency for it. The behaviour is golang.org/x/sync/singleflight's, on
// purpose and in detail, because the callers were written against that:
// results and errors are shared, the key is free again the moment the call
// returns, a panic in the function reaches every caller of Do, and under
// DoChan it is made unrecoverable rather than leaving the channels waiting
// for ever.
package flight

import (
	"bytes"
	"errors"
	"fmt"
	"runtime"
	"runtime/debug"
	"sync"
)

// errGoexit stands for a function that called runtime.Goexit: there is no
// result to share, and the callers waiting in Do leave the same way.
var errGoexit = errors.New("flight: runtime.Goexit was called")

// panicError carries a panic out of the function so that it can be raised
// again in each caller, with the stack of the goroutine where it happened.
type panicError struct {
	value any
	stack []byte
}

func (p *panicError) Error() string { return fmt.Sprintf("%v\n\n%s", p.value, p.stack) }

func (p *panicError) Unwrap() error {
	err, _ := p.value.(error)
	return err
}

func newPanicError(v any) error {
	stack := debug.Stack()
	// The first line names the goroutine and its state, which is no longer
	// true by the time another caller raises it.
	if line := bytes.IndexByte(stack, '\n'); line >= 0 {
		stack = stack[line+1:]
	}
	return &panicError{value: v, stack: stack}
}

// Result is what DoChan delivers.
type Result struct {
	Val    any
	Err    error
	Shared bool
}

type call struct {
	done sync.WaitGroup
	// Written once before done is released, read only after.
	val any
	err error
	// Guarded by the group's mutex until done is released.
	dups  int
	chans []chan<- Result
}

// Group is one namespace of keys. The zero value is ready to use.
type Group struct {
	mu    sync.Mutex
	calls map[string]*call
}

// Do runs fn once for every caller that asks for key while it runs, and
// returns its result to all of them; shared says whether more than one did.
func (g *Group) Do(key string, fn func() (any, error)) (v any, err error, shared bool) {
	g.mu.Lock()
	if g.calls == nil {
		g.calls = make(map[string]*call)
	}
	if c, ok := g.calls[key]; ok {
		c.dups++
		g.mu.Unlock()
		c.done.Wait()
		if p, ok := c.err.(*panicError); ok {
			panic(p)
		}
		if c.err == errGoexit {
			runtime.Goexit()
		}
		return c.val, c.err, true
	}
	c := new(call)
	c.done.Add(1)
	g.calls[key] = c
	g.mu.Unlock()

	g.run(c, key, fn)
	return c.val, c.err, c.dups > 0
}

// DoChan is Do without the wait: the result arrives on the channel, which has
// room for it, so a caller that stops listening leaves nothing blocked. The
// channel is never closed.
func (g *Group) DoChan(key string, fn func() (any, error)) <-chan Result {
	ch := make(chan Result, 1)
	g.mu.Lock()
	if g.calls == nil {
		g.calls = make(map[string]*call)
	}
	if c, ok := g.calls[key]; ok {
		c.dups++
		c.chans = append(c.chans, ch)
		g.mu.Unlock()
		return ch
	}
	c := &call{chans: []chan<- Result{ch}}
	c.done.Add(1)
	g.calls[key] = c
	g.mu.Unlock()

	go g.run(c, key, fn)
	return ch
}

func (g *Group) run(c *call, key string, fn func() (any, error)) {
	returned, recovered := false, false

	// Two defers, because a panic and a runtime.Goexit both skip the line
	// after fn and only a recover that stops the unwinding tells them apart.
	defer func() {
		if !returned && !recovered {
			c.err = errGoexit
		}

		g.mu.Lock()
		defer g.mu.Unlock()
		c.done.Done()
		if g.calls[key] == c {
			delete(g.calls, key)
		}

		if p, ok := c.err.(*panicError); ok {
			if len(c.chans) > 0 {
				// A recovered panic would leave every channel waiting for a
				// result that never comes, so it is raised where nothing can
				// recover it, and this goroutine stays for the crash dump.
				go panic(p)
				select {}
			}
			panic(p)
		}
		if c.err == errGoexit {
			return
		}
		for _, ch := range c.chans {
			ch <- Result{c.val, c.err, c.dups > 0}
		}
	}()

	func() {
		defer func() {
			if !returned {
				if r := recover(); r != nil {
					c.err = newPanicError(r)
				}
			}
		}()
		c.val, c.err = fn()
		returned = true
	}()

	if !returned {
		recovered = true
	}
}
