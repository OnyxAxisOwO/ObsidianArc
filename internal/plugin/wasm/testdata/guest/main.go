// A backend written straight against the ABI, without the SDK, so the engine
// is tested by something that cannot share its bugs. Built by the tests with
// GOOS=wasip1 -buildmode=c-shared.
package main

import (
	"encoding/json"
	"os"
	"unsafe"
)

//go:wasmimport arc host
func hostCall(req unsafe.Pointer, reqLen uint32, buf unsafe.Pointer, bufCap uint32) int32

//go:wasmimport arc host_read
func hostRead(buf unsafe.Pointer, bufCap uint32) int32

var pinned = map[uint32][]byte{}

//go:wasmexport arc_alloc
func arcAlloc(n uint32) uint32 {
	if n == 0 {
		n = 1
	}
	b := make([]byte, n)
	p := uint32(uintptr(unsafe.Pointer(&b[0])))
	pinned[p] = b
	return p
}

func host(op string, arg any) (json.RawMessage, string) {
	raw, _ := json.Marshal(map[string]any{"op": op, "arg": arg})
	buf := make([]byte, 256)
	n := hostCall(unsafe.Pointer(&raw[0]), uint32(len(raw)), unsafe.Pointer(&buf[0]), uint32(len(buf)))
	if n < 0 {
		buf = make([]byte, -n)
		n = hostRead(unsafe.Pointer(&buf[0]), uint32(len(buf)))
	}
	var r struct {
		OK     bool            `json:"ok"`
		Result json.RawMessage `json:"result"`
		Error  struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(buf[:n], &r); err != nil {
		return nil, "bad host response: " + err.Error()
	}
	if !r.OK {
		return nil, r.Error.Code + ": " + r.Error.Message
	}
	return r.Result, ""
}

func ok(result any) []byte {
	out, _ := json.Marshal(map[string]any{"ok": true, "result": result})
	return out
}

func fail(code, message string) []byte {
	out, _ := json.Marshal(map[string]any{"ok": false, "error": map[string]string{"code": code, "message": message}})
	return out
}

func handle(kind string, ctx map[string]any, arg json.RawMessage) []byte {
	switch kind {
	case "echo":
		res, e := host("echo", map[string]any{"arg": arg, "plugin": ctx["plugin"]})
		if e != "" {
			return fail("host", e)
		}
		return ok(map[string]any{"arg": arg, "plugin": ctx["plugin"], "host": res})
	case "big":
		res, e := host("big", nil)
		if e != "" {
			return fail("host", e)
		}
		return ok(len(res))
	case "spin":
		for {
		}
	case "panic":
		panic("boom from the guest")
	case "memhog":
		var keep [][]byte
		for i := 0; i < 300; i++ {
			keep = append(keep, make([]byte, 1<<20))
			keep[i][0] = 1
		}
		return ok(len(keep))
	case "fs":
		_, err := os.ReadFile("/etc/passwd")
		if err != nil {
			return ok("denied")
		}
		return ok("read it")
	case "env":
		return ok(len(os.Environ()))
	case "guesterr":
		return fail("nope", "the guest says no")
	case "hosterr":
		_, e := host("fail", nil)
		return ok(e)
	case "twice":
		a, _ := host("count", nil)
		b, _ := host("count", nil)
		return ok([]json.RawMessage{a, b})
	}
	return fail("unknown_kind", kind)
}

//go:wasmexport arc_call
func arcCall(ptr, n uint32) uint64 {
	in := unsafe.Slice((*byte)(unsafe.Pointer(uintptr(ptr))), n)
	var req struct {
		Kind string          `json:"kind"`
		Ctx  map[string]any  `json:"ctx"`
		Arg  json.RawMessage `json:"arg"`
	}
	var out []byte
	if err := json.Unmarshal(in, &req); err != nil {
		out = fail("bad_request", err.Error())
	} else {
		out = handle(req.Kind, req.Ctx, req.Arg)
	}
	p := arcAlloc(uint32(len(out)))
	copy(pinned[p], out)
	return uint64(p)<<32 | uint64(len(out))
}

func main() {}
