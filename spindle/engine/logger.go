package engine

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type StepLogger struct {
	stderr *os.File
	stdout *os.File
}

func NewStepLogger(baseDir, workflowID string, stepIdx int) (*StepLogger, error) {
	dir := filepath.Join(baseDir, workflowID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("creating log dir: %w", err)
	}

	stdoutPath := logFilePath(baseDir, workflowID, "stdout", stepIdx)
	stderrPath := logFilePath(baseDir, workflowID, "stderr", stepIdx)

	stdoutFile, err := os.Create(stdoutPath)
	if err != nil {
		return nil, fmt.Errorf("creating stdout log file: %w", err)
	}

	stderrFile, err := os.Create(stderrPath)
	if err != nil {
		stdoutFile.Close()
		return nil, fmt.Errorf("creating stderr log file: %w", err)
	}

	return &StepLogger{
		stdout: stdoutFile,
		stderr: stderrFile,
	}, nil
}

func (l *StepLogger) Stdout() io.Writer {
	return l.stdout
}

func (l *StepLogger) Stderr() io.Writer {
	return l.stderr
}

func (l *StepLogger) Close() error {
	err1 := l.stdout.Close()
	err2 := l.stderr.Close()
	if err1 != nil {
		return err1
	}
	return err2
}

func ReadStepLog(baseDir, workflowID, stream string, stepIdx int) (string, error) {
	logPath := logFilePath(baseDir, workflowID, stream, stepIdx)

	data, err := os.ReadFile(logPath)
	if err != nil {
		return "", fmt.Errorf("error reading log file: %w", err)
	}

	return string(data), nil
}

func logFilePath(baseDir, workflowID, stream string, stepIdx int) string {
	logFilePath := filepath.Join(baseDir, workflowID, fmt.Sprintf("%d-%s.log", stepIdx, stream))
	return logFilePath
}
