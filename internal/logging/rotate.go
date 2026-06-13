package logging

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

type rotatingWriter struct {
	mu       sync.Mutex
	path     string
	maxBytes int64
	history  int
	file     *os.File
	size     int64
}

func newRotatingWriter(path string, maxBytes int64, history int) (*rotatingWriter, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	return &rotatingWriter{path: path, maxBytes: maxBytes, history: history, file: file, size: info.Size()}, nil
}

func (w *rotatingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.maxBytes > 0 && w.size > 0 && w.size+int64(len(p)) > w.maxBytes {
		if err := w.rotate(); err != nil {
			return 0, err
		}
	}
	n, err := w.file.Write(p)
	w.size += int64(n)
	return n, err
}

func (w *rotatingWriter) rotate() error {
	if err := w.file.Close(); err != nil {
		return err
	}
	if w.history == 0 {
		_ = os.Remove(w.path)
	} else {
		_ = os.Remove(fmt.Sprintf("%s.%d", w.path, w.history))
		for i := w.history - 1; i >= 1; i-- {
			oldPath := fmt.Sprintf("%s.%d", w.path, i)
			newPath := fmt.Sprintf("%s.%d", w.path, i+1)
			if _, err := os.Stat(oldPath); err == nil {
				_ = os.Rename(oldPath, newPath)
			}
		}
		if _, err := os.Stat(w.path); err == nil {
			_ = os.Rename(w.path, fmt.Sprintf("%s.1", w.path))
		}
	}
	file, err := os.OpenFile(w.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	w.file = file
	w.size = 0
	return nil
}
