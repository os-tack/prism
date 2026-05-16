package engine

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
)

const debounceInterval = 100 * time.Millisecond

func watch(opts Options) error {
	if opts.Root == "" {
		return fmt.Errorf("engine: Options.Root is required")
	}
	agentsDir := filepath.Join(opts.Root, ".agents")
	if _, err := os.Stat(agentsDir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrNoAgentsDir
		}
		return fmt.Errorf("engine: stat .agents/: %w", err)
	}

	// Serialize compile() across initial-run, debounced fires, and races.
	var mu sync.Mutex
	doCompile := func(reason string) {
		mu.Lock()
		defer mu.Unlock()
		rep, err := compile(opts)
		if err != nil {
			if !opts.Quiet {
				fmt.Fprintf(os.Stderr, "watch: %s compile failed: %v\n", reason, err)
			}
			return
		}
		if !opts.Quiet {
			fmt.Printf("watch: %s — compiled (changed=%d unchanged=%d removed=%d)\n",
				reason, rep.Changed, rep.Unchanged, rep.Removed)
		}
	}

	doCompile("initial")

	w, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("engine: fsnotify: %w", err)
	}
	defer w.Close()

	if err := addRecursive(w, agentsDir); err != nil {
		return err
	}

	// Signal handler: clean exit on SIGINT/SIGTERM.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	var timer *time.Timer
	for {
		select {
		case <-sigCh:
			if !opts.Quiet {
				fmt.Println("watch: interrupted")
			}
			if timer != nil {
				timer.Stop()
			}
			return nil
		case ev, ok := <-w.Events:
			if !ok {
				return nil
			}
			if ev.Op&fsnotify.Create != 0 {
				if fi, err := os.Stat(ev.Name); err == nil && fi.IsDir() {
					_ = addRecursive(w, ev.Name)
				}
			}
			if timer != nil {
				timer.Stop()
			}
			timer = time.AfterFunc(debounceInterval, func() {
				doCompile("change")
			})
		case err, ok := <-w.Errors:
			if !ok {
				return nil
			}
			return fmt.Errorf("engine: fsnotify error: %w", err)
		}
	}
}

func addRecursive(w *fsnotify.Watcher, root string) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !d.IsDir() {
			return nil
		}
		if err := w.Add(path); err != nil {
			return fmt.Errorf("engine: watch %s: %w", path, err)
		}
		return nil
	})
}
