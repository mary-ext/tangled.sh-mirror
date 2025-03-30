package service

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"syscall"
)

// Mostly from charmbracelet/soft-serve and sosedoff/gitkit.

type ServiceCommand struct {
	Dir    string
	Stdin  io.Reader
	Stdout http.ResponseWriter
}

func (c *ServiceCommand) InfoRefs() error {
	cmd := exec.Command("git", []string{
		"upload-pack",
		"--stateless-rpc",
		"--advertise-refs",
		".",
	}...)

	cmd.Dir = c.Dir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdoutPipe, _ := cmd.StdoutPipe()
	cmd.Stderr = cmd.Stdout

	if err := cmd.Start(); err != nil {
		log.Printf("git: failed to start git-upload-pack (info/refs): %s", err)
		return err
	}

	if err := packLine(c.Stdout, "# service=git-upload-pack\n"); err != nil {
		log.Printf("git: failed to write pack line: %s", err)
		return err
	}

	if err := packFlush(c.Stdout); err != nil {
		log.Printf("git: failed to flush pack: %s", err)
		return err
	}

	buf := bytes.Buffer{}
	if _, err := io.Copy(&buf, stdoutPipe); err != nil {
		log.Printf("git: failed to copy stdout to tmp buffer: %s", err)
		return err
	}

	if err := cmd.Wait(); err != nil {
		out := strings.Builder{}
		_, _ = io.Copy(&out, &buf)
		log.Printf("git: failed to run git-upload-pack; err: %s; output: %s", err, out.String())
		return err
	}

	if _, err := io.Copy(c.Stdout, &buf); err != nil {
		log.Printf("git: failed to copy stdout: %s", err)
	}

	return nil
}

func (c *ServiceCommand) UploadPack() error {
	var stderr bytes.Buffer

	cmd := exec.Command("git", "-c", "uploadpack.allowFilter=true",
		"upload-pack", "--stateless-rpc", ".")
	cmd.Dir = c.Dir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	cmd.Stderr = &stderr

	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdin pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start git-upload-pack: %w", err)
	}

	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer stdinPipe.Close()
		io.Copy(stdinPipe, c.Stdin)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		io.Copy(newWriteFlusher(c.Stdout), stdoutPipe)
		stdoutPipe.Close()
	}()

	wg.Wait()

	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("git-upload-pack failed: %w, stderr: %s", err, stderr.String())
	}

	return nil
}

func packLine(w io.Writer, s string) error {
	_, err := fmt.Fprintf(w, "%04x%s", len(s)+4, s)
	return err
}

func packFlush(w io.Writer) error {
	_, err := fmt.Fprint(w, "0000")
	return err
}
