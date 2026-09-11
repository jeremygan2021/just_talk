//go:build darwin

package clipboard

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework AppKit
extern void *jt_pb_init(int *);
extern int jt_pb_poll_change(int *, char **);
extern void jt_pb_free(char *);
*/
import "C"
import (
	"context"
	"errors"
	"time"
	"unsafe"
)

func startDarwinWatcher(ctx context.Context, w *Watcher) error {
	var count C.int
	if C.jt_pb_init(&count) == nil {
		return errors.New("darwin: pasteboard init failed")
	}
	go func() {
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			var length C.int
			var ptr *C.char
			if C.jt_pb_poll_change(&length, &ptr) == 1 {
				var text string
				if length > 0 && ptr != nil {
					text = C.GoStringN(ptr, length)
					C.jt_pb_free(ptr)
				}
				if w.OnChange != nil {
					w.OnChange(text)
				}
			}
		}
	}()
	_ = unsafe.Pointer(nil)
	return nil
}
