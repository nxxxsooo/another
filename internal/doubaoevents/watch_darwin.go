//go:build darwin

package doubaoevents

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/sys/unix"
)

func (w *Watcher) watch(ctx context.Context, callback func(context.Context, Event) error) error {
	l, err := loadLedger(w.options.StatePath)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	events := make(chan Event)
	readerDone := make(chan error, 1)
	go func() { readerDone <- w.readEvents(ctx, events); close(events) }()
	defer func() { cancel(); <-readerDone }()
	jobs := make(chan Event)
	results := make(chan error, 1)
	var worker sync.WaitGroup
	worker.Add(1)
	go func() {
		defer worker.Done()
		for e := range jobs {
			results <- callback(ctx, e)
		}
	}()
	defer func() { cancel(); close(jobs); worker.Wait() }()
	var queue []Event
	pending := make(map[string]bool)
	var active *Event
	for {
		var send chan Event
		var next Event
		if active == nil && len(queue) > 0 {
			send = jobs
			next = queue[0]
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case e, ok := <-events:
			if !ok {
				// Return the reader error without consuming readerDone (the defer
				// owns the join). Keep its buffered result available.
				err := <-readerDone
				readerDone <- err
				return err
			}
			key := eventKey(e)
			if l.contains(key) || pending[key] {
				continue
			}
			if len(queue) >= maxPending {
				return errors.New("doubao event queue capacity exceeded")
			}
			pending[key] = true
			queue = append(queue, e)
		case send <- next:
			active = &next
			queue[0] = Event{}
			queue = queue[1:]
		case err := <-results:
			if err != nil {
				if w.options.OnError != nil {
					w.options.OnError(*active, err)
					delete(pending, eventKey(*active))
					active = nil
					continue
				}
				return fmt.Errorf("doubao event callback (%s): %w", active.Source, err)
			}
			key := eventKey(*active)
			if err := l.save(w.options.StatePath, key); err != nil {
				return fmt.Errorf("persist Doubao event success: %w", err)
			}
			delete(pending, key)
			active = nil
		}
	}
}

type watched struct {
	path   string
	source Source
	dir    bool
	tail   tail
	info   os.FileInfo
}

type eventReader struct {
	fd      int
	entries map[string]*watched
	ctx     context.Context
	output  chan<- Event
	retired []*watched
}

func (r *eventReader) emit(e *watched) error {
	return e.tail.readChecked(func(line []byte) error {
		if err := r.ctx.Err(); err != nil {
			return err
		}
		if event, ok := parse(line, e.source); ok {
			select {
			case r.output <- event:
			case <-r.ctx.Done():
				return r.ctx.Err()
			}
		}
		return nil
	}, r.ctx.Err)
}

var errTopologyChanged = errors.New("doubao log directory changed during registration")

// Leave headroom below the recommended LaunchAgent 16,384 descriptor limit.
const maxWatches = 12000

func (r *eventReader) remove(p string) {
	if e := r.entries[p]; e != nil {
		_ = e.tail.f.Close()
		if !e.dir {
			r.retired = append(r.retired, e)
			if len(r.retired) > 256 {
				r.retired = r.retired[1:]
			}
		}
		delete(r.entries, p)
	}
}

func (r *eventReader) add(p string, source Source, dir, baseline bool) error {
	if old := r.entries[p]; old != nil {
		info, err := os.Stat(p)
		if err == nil && os.SameFile(old.info, info) {
			return nil
		}
		if !old.dir {
			if err := r.emit(old); err != nil {
				return err
			}
		}
		r.remove(p)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	// O_NOFOLLOW prevents symlink log files from making the watcher read
	// unrelated files; directories are resolved by the explicit configured path.
	if len(r.entries) >= maxWatches {
		return fmt.Errorf("doubao native watch limit exceeded (%d files/directories)", maxWatches)
	}
	flags := unix.O_RDONLY | unix.O_CLOEXEC | unix.O_NONBLOCK | unix.O_NOFOLLOW
	fd, err := unix.Open(p, flags, 0)
	if errors.Is(err, os.ErrNotExist) {
		if dir {
			return errTopologyChanged
		}
		return nil
	}
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(fd), p)
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return err
	}
	if info.IsDir() != dir || (!dir && !info.Mode().IsRegular()) {
		_ = f.Close()
		return nil
	}
	change := unix.Kevent_t{Ident: uint64(fd), Filter: unix.EVFILT_VNODE, Flags: unix.EV_ADD | unix.EV_CLEAR, Fflags: unix.NOTE_WRITE | unix.NOTE_EXTEND | unix.NOTE_ATTRIB | unix.NOTE_RENAME | unix.NOTE_DELETE | unix.NOTE_REVOKE}
	if _, err := unix.Kevent(r.fd, []unix.Kevent_t{change}, nil, nil); err != nil {
		_ = f.Close()
		return err
	}
	e := &watched{path: p, source: source, dir: dir, tail: tail{f: f}, info: info}
	if !dir {
		// A rename changes the path, not the cursor. Never replay the old
		// contents merely because rotation gives the inode another log name.
		for _, old := range r.entries {
			if !old.dir && os.SameFile(old.info, info) {
				e.tail = old.tail
				e.tail.f = f
				break
			}
		}
		for _, old := range r.retired {
			if os.SameFile(old.info, info) {
				e.tail = old.tail
				e.tail.f = f
				break
			}
		}
	}
	r.entries[p] = e
	if !dir {
		if baseline {
			return e.tail.baseline()
		}
		return r.emit(e)
	}
	return nil
}

func nearestDir(p string) (string, error) {
	for {
		s, err := os.Stat(p)
		if err == nil {
			if !s.IsDir() {
				return "", errors.New("doubao log ancestor is not a directory")
			}
			return p, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(p)
		if parent == p {
			return "", err
		}
		p = parent
	}
}

func (r *eventReader) reconcile(options Options, baseline bool) error {
	for {
		if err := r.ctx.Err(); err != nil {
			return err
		}
		err := r.reconcileOnce(options, baseline)
		if !errors.Is(err, errTopologyChanged) {
			return err
		}
	}
}

func (r *eventReader) reconcileOnce(options Options, baseline bool) error {
	desired := make(map[string]bool)
	for _, target := range []struct {
		path   string
		source Source
	}{{options.DesktopDir, Desktop}, {options.CLIDir, CLI}} {
		// Register the nearest existing ancestor before descending. If a missing
		// chain appears during this pass, its creation is already queued by kqueue.
		ancestor, err := nearestDir(filepath.Dir(target.path))
		if err != nil {
			return err
		}
		desired[ancestor] = true
		if err := r.add(ancestor, target.source, true, false); err != nil {
			return err
		}
		nearest, err := nearestDir(target.path)
		if err != nil {
			return err
		}
		desired[nearest] = true
		if err := r.add(nearest, target.source, true, false); err != nil {
			return err
		}
		if nearest != target.path {
			continue
		}
		files, err := os.ReadDir(target.path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		for _, file := range files {
			if file.IsDir() || file.Type()&os.ModeSymlink != 0 || !strings.HasSuffix(file.Name(), ".log") ||
				strings.HasSuffix(file.Name(), ".stdio.log") {
				continue
			}
			if target.source == Desktop && !strings.HasPrefix(file.Name(), "saman_frontend_") {
				continue
			}
			if target.source == CLI && !strings.HasPrefix(file.Name(), "doubao-") {
				continue
			}
			p := filepath.Join(target.path, file.Name())
			desired[p] = true
			if err := r.add(p, target.source, false, baseline); err != nil {
				return err
			}
		}
	}
	for p, e := range r.entries {
		if desired[p] {
			continue
		}
		if !e.dir {
			if err := r.emit(e); err != nil {
				return err
			}
		}
		r.remove(p)
	}
	return nil
}

func (w *Watcher) readEvents(ctx context.Context, output chan<- Event) error {
	fd, err := unix.Kqueue()
	if err != nil {
		return err
	}
	defer func() { _ = unix.Close(fd) }()
	unix.CloseOnExec(fd)
	var pipe [2]int
	if err := unix.Pipe(pipe[:]); err != nil {
		return err
	}
	defer func() { _ = unix.Close(pipe[0]) }()
	defer func() { _ = unix.Close(pipe[1]) }()
	unix.CloseOnExec(pipe[0])
	unix.CloseOnExec(pipe[1])
	wake := unix.Kevent_t{Ident: uint64(pipe[0]), Filter: unix.EVFILT_READ, Flags: unix.EV_ADD}
	if _, err := unix.Kevent(fd, []unix.Kevent_t{wake}, nil, nil); err != nil {
		return err
	}
	stop := make(chan struct{})
	woken := make(chan struct{})
	go func() {
		defer close(woken)
		select {
		case <-ctx.Done():
			_, _ = unix.Write(pipe[1], []byte{1})
		case <-stop:
		}
	}()
	defer func() { close(stop); <-woken }()
	r := &eventReader{fd: fd, entries: make(map[string]*watched), ctx: ctx, output: output}
	defer func() {
		for p := range r.entries {
			r.remove(p)
		}
	}()
	if err := r.reconcile(w.options, true); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if w.options.Ready != nil {
		w.options.Ready()
	}
	var notifications [128]unix.Kevent_t
	for {
		n, err := unix.Kevent(fd, nil, notifications[:], nil)
		if err == unix.EINTR {
			continue
		}
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rescan := false
		for _, notification := range notifications[:n] {
			if notification.Flags&unix.EV_ERROR != 0 {
				return fmt.Errorf("doubao kqueue: %w", unix.Errno(notification.Data))
			}
			for _, e := range r.entries {
				if uint64(e.tail.f.Fd()) != notification.Ident {
					continue
				}
				if e.dir {
					rescan = true
				} else if err := r.emit(e); err != nil {
					return err
				}
				if notification.Fflags&(unix.NOTE_DELETE|unix.NOTE_RENAME|unix.NOTE_REVOKE) != 0 {
					rescan = true
				}
				break
			}
		}
		if rescan {
			if err := r.reconcile(w.options, false); err != nil {
				return err
			}
		}
	}
}
