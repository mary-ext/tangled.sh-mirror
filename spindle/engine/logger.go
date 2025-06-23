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
	path := LogFilePath(baseDir, wid)

	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
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

func LogFilePath(baseDir string, workflowID models.WorkflowId) string {
	logFilePath := filepath.Join(baseDir, fmt.Sprintf("%s.log", workflowID.String()))
	return logFilePath
}

func (l *WorkflowLogger) Writer(stream string, stepId int) io.Writer {
	return &jsonWriter{logger: l, stream: stream, stepId: stepId}
}

type jsonWriter struct {
	logger *WorkflowLogger
	stream string
	stepId int
}

func (w *jsonWriter) Write(p []byte) (int, error) {
	line := strings.TrimRight(string(p), "\r\n")

	entry := models.LogLine{
		Stream: w.stream,
		Data:   line,
		StepId: w.stepId,
	}

	if err := w.logger.encoder.Encode(entry); err != nil {
		return 0, err
	}

	return len(p), nil
}
