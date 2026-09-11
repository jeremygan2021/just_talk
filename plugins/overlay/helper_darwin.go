//go:build darwin && cgo

package overlay

// #cgo CFLAGS: -fblocks
// #cgo LDFLAGS: -framework AppKit -framework Foundation
// #include <stdlib.h>
// #include "overlay_darwin.h"
import "C"

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"runtime"
	"unsafe"
)

type helperCommand struct {
	Cmd   string `json:"cmd"`
	Label string `json:"label,omitempty"`
	R     uint16 `json:"r,omitempty"`
	G     uint16 `json:"g,omitempty"`
	B     uint16 `json:"b,omitempty"`
	// Bars is a JSON array of animated waveform bar heights (0..1,
	// left to right). Non-waveform states pass nil so the helper keeps
	// the compact capsule layout.
	Bars []float32 `json:"bars,omitempty"`
	// Phase is the animation clock in seconds; the helper uses it for
	// the breathing blue glow.
	Phase float64 `json:"phase,omitempty"`
}

func RunHelper(position string, scale float64, input io.Reader) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	pos := C.CString(position)
	defer C.free(unsafe.Pointer(pos))

	C.jt_overlay_helper_init(pos, C.double(scale))
	go readHelperCommands(input)
	C.jt_overlay_helper_run_app()
	return nil
}

func readHelperCommands(input io.Reader) {
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		var cmd helperCommand
		if err := json.Unmarshal(scanner.Bytes(), &cmd); err != nil {
			fmt.Fprintf(os.Stderr, "overlay helper command parse error: %v\n", err)
			continue
		}
		switch cmd.Cmd {
		case "show":
			if len(cmd.Bars) == 0 {
				label := C.CString(cmd.Label)
				C.jt_overlay_helper_show(label, C.ushort(cmd.R), C.ushort(cmd.G), C.ushort(cmd.B))
				C.free(unsafe.Pointer(label))
			} else {
				barsPtr, n := helperLevelPtr(cmd.Bars)
				label := C.CString(cmd.Label)
				C.jt_overlay_helper_show_wide(label,
					C.ushort(cmd.R), C.ushort(cmd.G), C.ushort(cmd.B),
					barsPtr, C.int(n), C.double(cmd.Phase))
				C.free(unsafe.Pointer(label))
				if barsPtr != nil {
					C.free(unsafe.Pointer(barsPtr))
				}
			}
		case "hide":
			C.jt_overlay_helper_hide()
		case "close":
			C.jt_overlay_helper_close()
			return
		}
	}
	C.jt_overlay_helper_close()
}

// helperLevelPtr copies the Go slice to a C-owned float buffer and
// returns the pointer + length. The C function copies the data before
// returning, so the caller frees the buffer right after the call.
func helperLevelPtr(levels []float32) (*C.float, C.int) {
	if len(levels) == 0 {
		return nil, 0
	}
	buf := C.calloc(C.size_t(len(levels)), C.sizeof_float)
	if buf == nil {
		return nil, 0
	}
	dst := (*[1 << 30]C.float)(buf)
	for i, v := range levels {
		dst[i] = C.float(v)
	}
	return (*C.float)(buf), C.int(len(levels))
}
