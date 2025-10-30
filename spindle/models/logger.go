package models

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type WorkflowLogger struct {
	file    *os.File
	encoder *json.Encoder
}

func NewWorkflowLogger(baseDir string, wid WorkflowId) (*WorkflowLogger, error) {
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

func LogFilePath(baseDir string, workflowID WorkflowId) string {
	logFilePath := filepath.Join(baseDir, fmt.Sprintf("%s.log", workflowID.String()))
	return logFilePath
}

func (l *WorkflowLogger) Close() error {
	return l.file.Close()
}

func (l *WorkflowLogger) DataWriter(idx int, stream string) io.Writer {
	return &dataWriter{
		logger: l,
		idx:    idx,
		stream: stream,
	}
}

func (l *WorkflowLogger) ControlWriter(idx int, step Step, stepStatus StepStatus) io.Writer {
	return &controlWriter{
		logger:     l,
		idx:        idx,
		step:       step,
		stepStatus: stepStatus,
	}
}

type dataWriter struct {
	logger *WorkflowLogger
	idx    int
	stream string
}

func (w *dataWriter) Write(p []byte) (int, error) {
	line := strings.TrimRight(string(p), "\r\n")
	entry := NewDataLogLine(w.idx, line, w.stream)
	if err := w.logger.encoder.Encode(entry); err != nil {
		return 0, err
	}
	return len(p), nil
}

type controlWriter struct {
	logger     *WorkflowLogger
	idx        int
	step       Step
	stepStatus StepStatus
}

func (w *controlWriter) Write(_ []byte) (int, error) {
	entry := NewControlLogLine(w.idx, w.step, w.stepStatus)
	if err := w.logger.encoder.Encode(entry); err != nil {
		return 0, err
	}
	return len(w.step.Name()), nil
}
