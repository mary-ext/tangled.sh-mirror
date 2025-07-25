// heavily inspired by gitea's model

package hook

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-git/go-git/v5"
)

var ErrNoGitRepo = errors.New("not a git repo")
var ErrCreatingHookDir = errors.New("failed to create hooks directory")
var ErrCreatingHook = errors.New("failed to create hook")
var ErrCreatingDelegate = errors.New("failed to create delegate hook")

type config struct {
	scanPath    string
	internalApi string
}

type setupOpt func(*config)

func WithScanPath(scanPath string) setupOpt {
	return func(c *config) {
		c.scanPath = scanPath
	}
}

func WithInternalApi(api string) setupOpt {
	return func(c *config) {
		c.internalApi = api
	}
}

func Config(opts ...setupOpt) config {
	config := config{}
	for _, o := range opts {
		o(&config)
	}
	return config
}

// setup hooks for all users
//
// directory structure is typically like so:
//
//	did:plc:foobar/repo1
//	did:plc:foobar/repo2
//	did:web:barbaz/repo1
func Setup(config config) error {
	// iterate over all directories in current directory:
	userDirs, err := os.ReadDir(config.scanPath)
	if err != nil {
		return err
	}

	for _, user := range userDirs {
		if !user.IsDir() {
			continue
		}

		did := user.Name()
		if !strings.HasPrefix(did, "did:") {
			continue
		}

		userPath := filepath.Join(config.scanPath, did)
		if err := SetupUser(config, userPath); err != nil {
			return err
		}
	}

	return nil
}

// setup hooks in /scanpath/did:plc:user
func SetupUser(config config, userPath string) error {
	repos, err := os.ReadDir(userPath)
	if err != nil {
		return err
	}

	for _, repo := range repos {
		if !repo.IsDir() {
			continue
		}

		path := filepath.Join(userPath, repo.Name())
		if err := SetupRepo(config, path); err != nil {
			if errors.Is(err, ErrNoGitRepo) {
				continue
			}
			return err
		}
	}

	return nil
}

// setup hook in /scanpath/did:plc:user/repo
func SetupRepo(config config, path string) error {
	if _, err := git.PlainOpen(path); err != nil {
		return fmt.Errorf("%s: %w", path, ErrNoGitRepo)
	}

	preReceiveD := filepath.Join(path, "hooks", "post-receive.d")
	if err := os.MkdirAll(preReceiveD, 0755); err != nil {
		return fmt.Errorf("%s: %w", preReceiveD, ErrCreatingHookDir)
	}

	notify := filepath.Join(preReceiveD, "40-notify.sh")
	if err := mkHook(config, notify); err != nil {
		return fmt.Errorf("%s: %w", notify, ErrCreatingHook)
	}

	delegate := filepath.Join(path, "hooks", "post-receive")
	if err := mkDelegate(delegate); err != nil {
		return fmt.Errorf("%s: %w", delegate, ErrCreatingDelegate)
	}

	return nil
}

func mkHook(config config, hookPath string) error {
	executablePath, err := os.Executable()
	if err != nil {
		return err
	}

	hookContent := fmt.Sprintf(`#!/usr/bin/env bash
# AUTO GENERATED BY KNOT, DO NOT MODIFY
%s hook -git-dir "$GIT_DIR" -user-did "$GIT_USER_DID" -user-handle "$GIT_USER_HANDLE" -internal-api "%s" post-recieve
	`, executablePath, config.internalApi)

	return os.WriteFile(hookPath, []byte(hookContent), 0755)
}

func mkDelegate(path string) error {
	content := fmt.Sprintf(`#!/usr/bin/env bash
# AUTO GENERATED BY KNOT, DO NOT MODIFY
data=$(cat)
exitcodes=""
hookname=$(basename $0)
GIT_DIR="$PWD"

for hook in ${GIT_DIR}/hooks/${hookname}.d/*; do
  test -x "${hook}" && test -f "${hook}" || continue
  echo "${data}" | "${hook}"
  exitcodes="${exitcodes} $?"
done

for i in ${exitcodes}; do
  [ ${i} -eq 0 ] || exit ${i}
done
	`)

	return os.WriteFile(path, []byte(content), 0755)
}
