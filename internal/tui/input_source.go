package tui

import "sync"

// inputSourceAPI is the small native seam needed to keep browser shortcuts in
// ASCII while another owns the terminal. Each copied source is retained and
// must be released exactly once.
type inputSourceAPI interface {
	current() uintptr
	asciiCapable() uintptr
	selectSource(uintptr) int32
	release(uintptr)
}

// temporarilyUseASCIIInputSource switches before Bubble Tea starts reading
// keys and returns an idempotent restoration function. The operation is best
// effort: unsupported platforms and unavailable system APIs keep their normal
// input-source behavior rather than preventing the TUI from opening.
func temporarilyUseASCIIInputSource() func() {
	return temporarilyUseASCIIInputSourceWith(platformInputSourceAPI())
}

func temporarilyUseASCIIInputSourceWith(api inputSourceAPI) func() {
	noop := func() {}
	if api == nil {
		return noop
	}

	original := api.current()
	if original == 0 {
		return noop
	}
	ascii := api.asciiCapable()
	if ascii == 0 {
		api.release(original)
		return noop
	}
	if status := api.selectSource(ascii); status != 0 {
		api.release(ascii)
		api.release(original)
		return noop
	}
	api.release(ascii)

	var once sync.Once
	return func() {
		once.Do(func() {
			api.selectSource(original)
			api.release(original)
		})
	}
}
