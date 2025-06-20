package engine

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"tangled.sh/tangled.sh/core/spindle/models"
)

type WorkflowLogger struct {
	file    *os.File
	encoder *json.Encoder
}

func NewWorkflowLogger(baseDir string, wid models.WorkflowId) (*WorkflowLogger, error) {
	dir := filepath.Join(baseDir, wid.String())
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("creating log dir: %w", err)
	}

	path := LogFilePath(baseDir, wid)

	file, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("creating log file: %w", err)
	}

	return &WorkflowLogger{
		file:    file,
		encoder: json.NewEncoder(file),
	}, nil
}

func (l *WorkflowLogger) Write(p []byte) (n int, err error) {
	return l.file.Write(p)
}

func (l *WorkflowLogger) Close() error {
	return l.file.Close()
}

func OpenLogFile(baseDir string, workflowID models.WorkflowId) (*os.File, error) {
	logPath := LogFilePath(baseDir, workflowID)

	file, err := os.Open(logPath)
	if err != nil {
		return nil, fmt.Errorf("error opening log file: %w", err)
	}

	return file, nil
}

func LogFilePath(baseDir string, workflowID models.WorkflowId) string {
	logFilePath := filepath.Join(baseDir, fmt.Sprintf("%s.log", workflowID.String()))
	return logFilePath
}

func (l *WorkflowLogger) Stdout() io.Writer {
	return &jsonWriter{logger: l, stream: "stdout"}
}

func (l *WorkflowLogger) Stderr() io.Writer {
	return &jsonWriter{logger: l, stream: "stderr"}
}

type jsonWriter struct {
	logger *WorkflowLogger
	stream string
}

func (w *jsonWriter) Write(p []byte) (int, error) {
	line := strings.TrimRight(string(p), "\r\n")

	entry := models.LogLine{
		Stream: w.stream,
		Data:   line,
	}

	if err := w.logger.encoder.Encode(entry); err != nil {
		return 0, err
	}

	return len(p), nil
}
