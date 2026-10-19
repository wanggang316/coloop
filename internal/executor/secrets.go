package executor

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"

	"github.com/joho/godotenv"
	"github.com/wanggang316/coloop/internal/model"
)

// resolveEnv reads credentials only when this task references them. It does
// not change the daemon environment or the stored task definition.
func resolveEnv(env model.Env, file string) (map[string]string, []string, error) {
	values := env.Literals()
	var keys []string
	for name, value := range env {
		if value.Secret != "" {
			keys = append(keys, name)
		}
	}
	if len(keys) == 0 {
		return values, nil, nil
	}
	sort.Strings(keys)
	if file == "" {
		return nil, nil, fmt.Errorf("secrets.file is required")
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, nil, fmt.Errorf("read secrets.file: %w", err)
	}
	secrets, err := godotenv.Unmarshal(string(data))
	// Parser errors can contain file contents. Never include them in a log.
	if err != nil {
		return nil, nil, fmt.Errorf("invalid dotenv syntax in secrets.file")
	}
	var sensitive []string
	for _, name := range keys {
		key := env[name].Secret
		value, ok := secrets[key]
		if !ok || value == "" {
			return nil, nil, fmt.Errorf("env %q: secret %q is missing or empty", name, key)
		}
		if strings.ContainsRune(value, 0) {
			return nil, nil, fmt.Errorf("env %q: secret contains NUL", name)
		}
		values[name] = value
		sensitive = append(sensitive, value)
	}
	return values, sensitive, nil
}

// secretWriter masks exact credential values before output reaches disk or
// subscribers. A trailing possible match is held across Write boundaries.
// Encoded or transformed values are not covered by this filter.
type secretWriter struct {
	mu      sync.Mutex
	dst     io.Writer
	secrets [][]byte
	pending []byte
}

func newSecretWriter(dst io.Writer, secrets []string) *secretWriter {
	w := &secretWriter{dst: dst}
	for _, s := range secrets {
		if s != "" {
			w.secrets = append(w.secrets, []byte(s))
		}
	}
	sort.Slice(w.secrets, func(i, j int) bool { return len(w.secrets[i]) > len(w.secrets[j]) })
	return w
}

func (w *secretWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.pending = append(w.pending, p...)
	if err := w.drain(false); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (w *secretWriter) Flush() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.drain(true)
}

func (w *secretWriter) drain(final bool) error {
	var out bytes.Buffer
	for len(w.pending) > 0 {
		match, wait := 0, false
		for _, secret := range w.secrets {
			if bytes.HasPrefix(w.pending, secret) {
				match = len(secret)
				break
			}
			if !final && bytes.HasPrefix(secret, w.pending) {
				wait = true
				break
			}
		}
		if wait {
			break
		}
		if match > 0 {
			out.WriteString("[REDACTED]")
			w.pending = w.pending[match:]
		} else {
			out.WriteByte(w.pending[0])
			w.pending = w.pending[1:]
		}
	}
	if out.Len() > 0 {
		_, err := w.dst.Write(out.Bytes())
		return err
	}
	return nil
}
