//go:build wasip1

package arc

import (
	"encoding/json"
	"errors"
	"unsafe"
)

//go:wasmimport arc host
func rawHost(req unsafe.Pointer, reqLen uint32, buf unsafe.Pointer, bufCap uint32) int32

//go:wasmimport arc host_read
func rawHostRead(buf unsafe.Pointer, bufCap uint32) int32

// Buffers the server has been given the address of, kept reachable so the
// collector cannot take one while the server is still writing into it. Go's
// wasm collector does not move objects, but nothing about the ABI should
// depend on that.
var pinned = map[uintptr][]byte{}

//go:wasmexport arc_alloc
func arcAlloc(n uint32) unsafe.Pointer {
	if n == 0 {
		n = 1
	}
	b := make([]byte, n)
	p := unsafe.Pointer(&b[0])
	pinned[uintptr(p)] = b
	return p
}

//go:wasmexport arc_call
func arcCall(ptr unsafe.Pointer, n uint32) uint64 {
	in := make([]byte, n)
	copy(in, unsafe.Slice((*byte)(ptr), n))
	out := Serve(in)
	p := arcAlloc(uint32(len(out)))
	copy(pinned[uintptr(p)], out)
	return uint64(uintptr(p))<<32 | uint64(len(out))
}

func init() { hostCall = wasmHostCall }

// wasmHostCall is one call to the server: the request goes out, and the
// answer comes back in a buffer this side owns.
func wasmHostCall(op string, arg any) (json.RawMessage, error) {
	req, err := json.Marshal(map[string]any{"op": op, "arg": arg})
	if err != nil {
		return nil, err
	}
	buf := make([]byte, 4096)
	n := rawHost(unsafe.Pointer(&req[0]), uint32(len(req)), unsafe.Pointer(&buf[0]), uint32(len(buf)))
	if n < 0 {
		buf = make([]byte, -n)
		n = rawHostRead(unsafe.Pointer(&buf[0]), uint32(len(buf)))
		if n < 0 {
			return nil, errors.New("arc: the server's answer could not be read")
		}
	}
	var reply struct {
		OK     bool            `json:"ok"`
		Result json.RawMessage `json:"result"`
		Error  struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(buf[:n], &reply); err != nil {
		return nil, err
	}
	if !reply.OK {
		return nil, &HostError{Code: reply.Error.Code, Message: reply.Error.Message}
	}
	return reply.Result, nil
}
