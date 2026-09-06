//go:build darwin

package tui

import "github.com/ebitengine/purego"

const (
	hitoolboxPath      = "/System/Library/Frameworks/Carbon.framework/Versions/A/Frameworks/HIToolbox.framework/Versions/A/HIToolbox"
	coreFoundationPath = "/System/Library/Frameworks/CoreFoundation.framework/Versions/A/CoreFoundation"
)

type darwinInputSourceAPI struct {
	copyCurrent      func() uintptr
	copyASCIICapable func() uintptr
	selectInput      func(uintptr) int32
	cfRelease        func(uintptr)
}

func platformInputSourceAPI() (api inputSourceAPI) {
	// A missing framework or symbol must degrade to a no-op, not crash startup.
	defer func() {
		if recover() != nil {
			api = nil
		}
	}()

	hitoolbox, err := purego.Dlopen(hitoolboxPath, purego.RTLD_NOW|purego.RTLD_LOCAL)
	if err != nil {
		return nil
	}
	coreFoundation, err := purego.Dlopen(coreFoundationPath, purego.RTLD_NOW|purego.RTLD_LOCAL)
	if err != nil {
		return nil
	}

	native := &darwinInputSourceAPI{}
	purego.RegisterLibFunc(&native.copyCurrent, hitoolbox, "TISCopyCurrentKeyboardInputSource")
	purego.RegisterLibFunc(&native.copyASCIICapable, hitoolbox, "TISCopyCurrentASCIICapableKeyboardInputSource")
	purego.RegisterLibFunc(&native.selectInput, hitoolbox, "TISSelectInputSource")
	purego.RegisterLibFunc(&native.cfRelease, coreFoundation, "CFRelease")
	return native
}

func (a *darwinInputSourceAPI) current() uintptr                  { return a.copyCurrent() }
func (a *darwinInputSourceAPI) asciiCapable() uintptr             { return a.copyASCIICapable() }
func (a *darwinInputSourceAPI) selectSource(source uintptr) int32 { return a.selectInput(source) }
func (a *darwinInputSourceAPI) release(source uintptr)            { a.cfRelease(source) }
