//go:build windows

package unityvhdxfixture

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/Kubonsang/testplay-runner/internal/storagehelper"
)

type HelperClient struct {
	command    *exec.Cmd
	stdin      io.WriteCloser
	stdout     *bufio.Reader
	protocol   *os.File
	stderrFile *os.File
	stderr     bytes.Buffer
	callMu     sync.Mutex
	waitOnce   sync.Once
	waitErr    error
	started    time.Time
}

func StartHelper(ctx context.Context, executable, artifactDir string) (*HelperClient, error) {
	if !filepath.IsAbs(executable) {
		return nil, fixtureError(CodeUnityRunFailed, "start-helper", executable, fmt.Errorf("absolute helper path required"))
	}
	if _, err := os.Stat(executable); err != nil {
		return nil, fixtureError(CodeUnityRunFailed, "start-helper", executable, err)
	}
	if err := os.MkdirAll(artifactDir, 0700); err != nil {
		return nil, err
	}
	protocol, err := os.Create(filepath.Join(artifactDir, "protocol.ndjson"))
	if err != nil {
		return nil, err
	}
	stderrFile, err := os.Create(filepath.Join(artifactDir, "stderr.log"))
	if err != nil {
		_ = protocol.Close()
		return nil, err
	}
	command := exec.CommandContext(ctx, executable)
	stdin, err := command.StdinPipe()
	if err != nil {
		_ = protocol.Close()
		_ = stderrFile.Close()
		return nil, err
	}
	stdoutPipe, err := command.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		_ = protocol.Close()
		_ = stderrFile.Close()
		return nil, err
	}
	client := &HelperClient{command: command, stdin: stdin, stdout: bufio.NewReader(stdoutPipe), protocol: protocol, stderrFile: stderrFile, started: time.Now()}
	command.Stderr = io.MultiWriter(stderrFile, &client.stderr)
	if err := command.Start(); err != nil {
		_ = stdin.Close()
		_ = protocol.Close()
		_ = stderrFile.Close()
		return nil, err
	}
	return client, nil
}

func (c *HelperClient) StartupMs() int64 { return time.Since(c.started).Milliseconds() }

func (c *HelperClient) Call(request storagehelper.Request) (storagehelper.Response, error) {
	c.callMu.Lock()
	defer c.callMu.Unlock()
	data, err := json.Marshal(request)
	if err != nil {
		return storagehelper.Response{}, err
	}
	if err := c.logProtocol("request", data); err != nil {
		return storagehelper.Response{}, err
	}
	if _, err := c.stdin.Write(append(data, '\n')); err != nil {
		return storagehelper.Response{}, err
	}
	line, err := c.stdout.ReadBytes('\n')
	if err != nil {
		return storagehelper.Response{}, fmt.Errorf("read helper response: %w: %s", err, c.stderr.String())
	}
	if err := c.logProtocol("response", bytes.TrimSpace(line)); err != nil {
		return storagehelper.Response{}, err
	}
	var response storagehelper.Response
	if err := json.Unmarshal(line, &response); err != nil {
		return response, fmt.Errorf("decode helper response: %w", err)
	}
	if !response.OK {
		return response, response.Error
	}
	return response, nil
}

func (c *HelperClient) logProtocol(direction string, payload []byte) error {
	var value any
	if err := json.Unmarshal(payload, &value); err != nil {
		return err
	}
	return json.NewEncoder(c.protocol).Encode(map[string]any{"direction": direction, "message": value})
}

func (c *HelperClient) CloseInput() error { return c.stdin.Close() }

func (c *HelperClient) Wait() error {
	c.waitOnce.Do(func() {
		c.waitErr = c.command.Wait()
		_ = c.protocol.Close()
		_ = c.stderrFile.Close()
	})
	if c.waitErr != nil {
		return fmt.Errorf("helper exit: %w: %s", c.waitErr, c.stderr.String())
	}
	return nil
}
