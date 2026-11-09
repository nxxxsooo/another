//go:build !darwin

package doubaoevents

import "context"

func (w *Watcher) watch(context.Context, func(context.Context, Event) error) error {
	return ErrUnsupported
}
