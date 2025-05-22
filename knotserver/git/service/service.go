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
	GitProtocol string
	Dir         string
	Stdin       io.Reader
	Stdout      http.ResponseWriter
}

func (c *ServiceCommand) RunService(cmd *exec.Cmd) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Dir = c.Dir
	cmd.Env = append(cmd.Env, fmt.Sprintf("GIT_PROTOCOL=%s", c.GitProtocol))

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdin pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start '%s': %w", cmd.String(), err)
	}

	var wg sync.WaitGroup

	if c.Stdin != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer stdinPipe.Close()
			io.Copy(stdinPipe, c.Stdin)
		}()
	}

	if c.Stdout != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			io.Copy(newWriteFlusher(c.Stdout), stdoutPipe)
			stdoutPipe.Close()
		}()
	}

	wg.Wait()

	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("'%s' failed: %w, stderr: %s", cmd.String(), err, stderr.String())
	}

	return nil
}

func (c *ServiceCommand) InfoRefs() error {
	cmd := exec.Command("git", []string{
		"upload-pack",
		"--stateless-rpc",
		"--http-backend-info-refs",
		".",
	}...)

	if !strings.Contains(c.GitProtocol, "version=2") {
		if err := packLine(c.Stdout, "# service=git-upload-pack\n"); err != nil {
			log.Printf("git: failed to write pack line: %s", err)
			return err
		}

		if err := packFlush(c.Stdout); err != nil {
			log.Printf("git: failed to flush pack: %s", err)
			return err
		}
	}

	return c.RunService(cmd)
}

func (c *ServiceCommand) UploadPack() error {
	cmd := exec.Command("git", []string{
		"-c", "uploadpack.allowFilter=true",
		"upload-pack",
		"--stateless-rpc",
		".",
	}...)

	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Env = append(cmd.Env, fmt.Sprintf("GIT_PROTOCOL=%s", c.GitProtocol))
	cmd.Dir = c.Dir

	return c.RunService(cmd)
}

func packLine(w io.Writer, s string) error {
	_, err := fmt.Fprintf(w, "%04x%s", len(s)+4, s)
	return err
}

func packFlush(w io.Writer) error {
	_, err := fmt.Fprint(w, "0000")
	return err
}
