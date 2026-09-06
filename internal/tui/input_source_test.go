package tui

import "testing"

type fakeInputSourceAPI struct {
	currentSource uintptr
	asciiSource   uintptr
	selectStatus  int32
	selected      []uintptr
	released      []uintptr
}

func (f *fakeInputSourceAPI) current() uintptr      { return f.currentSource }
func (f *fakeInputSourceAPI) asciiCapable() uintptr { return f.asciiSource }
func (f *fakeInputSourceAPI) selectSource(source uintptr) int32 {
	f.selected = append(f.selected, source)
	return f.selectStatus
}
func (f *fakeInputSourceAPI) release(source uintptr) { f.released = append(f.released, source) }

func TestTemporaryASCIIInputSourceSwitchesAndRestoresOnce(t *testing.T) {
	api := &fakeInputSourceAPI{currentSource: 1, asciiSource: 2}
	restore := temporarilyUseASCIIInputSourceWith(api)
	if len(api.selected) != 1 || api.selected[0] != 2 {
		t.Fatalf("initial selection = %v, want ASCII source", api.selected)
	}
	if len(api.released) != 1 || api.released[0] != 2 {
		t.Fatalf("initial releases = %v, want temporary ASCII reference", api.released)
	}

	restore()
	restore()
	if len(api.selected) != 2 || api.selected[1] != 1 {
		t.Fatalf("restored selections = %v, want original source once", api.selected)
	}
	if len(api.released) != 2 || api.released[1] != 1 {
		t.Fatalf("restored releases = %v, want original reference once", api.released)
	}
}

func TestTemporaryASCIIInputSourceLeavesTUIUsableOnNativeFailure(t *testing.T) {
	api := &fakeInputSourceAPI{currentSource: 1, asciiSource: 2, selectStatus: -1}
	restore := temporarilyUseASCIIInputSourceWith(api)
	restore()
	if len(api.selected) != 1 {
		t.Fatalf("selection retried after failure: %v", api.selected)
	}
	if len(api.released) != 2 {
		t.Fatalf("failed switch leaked references: %v", api.released)
	}
}
