// Package logmeta writes bounded metadata logs independently of the audit chain.
package logmeta

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

type Writer struct {
	mu    sync.Mutex
	path  string
	max   int64
	files int
	f     *os.File
	size  int64
}

func Open(path string, max int64, files int) (*Writer, error) {
	if max < 1 || files < 1 {
		return nil, fmt.Errorf("invalid log limits")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	w := &Writer{path: path, max: max, files: files}
	if err := w.open(); err != nil {
		return nil, err
	}
	return w, nil
}
func (w *Writer) open() error {
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	w.f = f
	w.size = info.Size()
	return nil
}
func (w *Writer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if int64(len(p)) > w.max {
		return 0, fmt.Errorf("log record too large")
	}
	if w.size+int64(len(p)) > w.max {
		if err := w.f.Close(); err != nil {
			return 0, err
		}
		if w.files == 1 {
			if err := os.Remove(w.path); err != nil {
				return 0, err
			}
		} else {
			if err := os.Remove(fmt.Sprintf("%s.%d", w.path, w.files-1)); err != nil && !os.IsNotExist(err) {
				return 0, err
			}
			for i := w.files - 2; i >= 1; i-- {
				if err := os.Rename(fmt.Sprintf("%s.%d", w.path, i), fmt.Sprintf("%s.%d", w.path, i+1)); err != nil && !os.IsNotExist(err) {
					return 0, err
				}
			}
			if err := os.Rename(w.path, w.path+".1"); err != nil {
				return 0, err
			}
		}
		if err := w.open(); err != nil {
			return 0, err
		}
	}
	n, err := w.f.Write(p)
	w.size += int64(n)
	return n, err
}
func (w *Writer) Close() error { w.mu.Lock(); defer w.mu.Unlock(); return w.f.Close() }
