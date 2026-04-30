package outpost

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type RequestLog struct {
	Time     time.Time `json:"time"`
	Method   string    `json:"method"`
	Path     string    `json:"path"`
	KeyID    string    `json:"key_id,omitempty"`
	Model    string    `json:"model,omitempty"`
	Backend  string    `json:"backend"`
	Status   int       `json:"status"`
	Bytes    int64     `json:"bytes"`
	Duration string    `json:"duration"`
	Error    string    `json:"error,omitempty"`
}

type RequestLogger struct {
	path string
	mu   sync.Mutex
}

func NewRequestLogger(path string) *RequestLogger {
	return &RequestLogger{path: path}
}

func (l *RequestLogger) Write(entry RequestLog) error {
	if l == nil || l.path == "" {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(l.path), 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(l.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()

	encoded, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	_, err = file.Write(append(encoded, '\n'))
	return err
}

func TailLogs(path string, out io.Writer) error {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		fmt.Fprintln(out, "No logs yet:", path)
		for {
			time.Sleep(500 * time.Millisecond)
			file, err = os.Open(path)
			if err == nil {
				break
			}
			if !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
	} else if err != nil {
		return err
	}
	defer file.Close()

	if _, err := file.Seek(0, io.SeekEnd); err != nil {
		return err
	}
	reader := bufio.NewReader(file)
	for {
		line, err := reader.ReadString('\n')
		if line != "" {
			fmt.Fprint(out, line)
		}
		if err == nil {
			continue
		}
		if errors.Is(err, io.EOF) {
			time.Sleep(500 * time.Millisecond)
			continue
		}
		return err
	}
}

func SearchLogs(path string, query string, out io.Writer) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	query = strings.ToLower(query)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(strings.ToLower(line), query) {
			fmt.Fprintln(out, line)
		}
	}
	return scanner.Err()
}
