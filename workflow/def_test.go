package workflow

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestUnmarshalWorkflow(t *testing.T) {
	yamlData := `
when:
  - event: ["push", "pull_request"]
    branch: ["main", "develop"]

dependencies:
  nixpkgs:
    - go
    - git
    - curl

steps:
  - name: "Test"
    command: |
       go test ./...`

	wf, err := FromFile("test.yml", []byte(yamlData))
	assert.NoError(t, err, "YAML should unmarshal without error")

	assert.Len(t, wf.When, 1, "Should have one constraint")
	assert.ElementsMatch(t, []string{"main", "develop"}, wf.When[0].Branch)
	assert.ElementsMatch(t, []string{"push", "pull_request"}, wf.When[0].Event)

	assert.Len(t, wf.Steps, 1)
	assert.Equal(t, "Test", wf.Steps[0].Name)
	assert.Equal(t, "go test ./...", wf.Steps[0].Command)

	pkgs, ok := wf.Dependencies["nixpkgs"]
	assert.True(t, ok, "`nixpkgs` should be present in dependencies")
	assert.ElementsMatch(t, []string{"go", "git", "curl"}, pkgs)

	assert.False(t, wf.CloneOpts.Skip, "Skip should default to false")
}

func TestUnmarshalCustomRegistry(t *testing.T) {
	yamlData := `
when:
  - event: push
    branch: main

dependencies:
  git+https://tangled.sh/@oppi.li/tbsp:
    - tbsp
  git+https://git.peppe.rs/languages/statix:
    - statix

steps:
  - name: "Check"
    command: |
       statix check`

	wf, err := FromFile("test.yml", []byte(yamlData))
	assert.NoError(t, err, "YAML should unmarshal without error")

	assert.ElementsMatch(t, []string{"push"}, wf.When[0].Event)
	assert.ElementsMatch(t, []string{"main"}, wf.When[0].Branch)

	assert.ElementsMatch(t, []string{"tbsp"}, wf.Dependencies["git+https://tangled.sh/@oppi.li/tbsp"])
	assert.ElementsMatch(t, []string{"statix"}, wf.Dependencies["git+https://git.peppe.rs/languages/statix"])
}

func TestUnmarshalCloneFalse(t *testing.T) {
	yamlData := `
when:
  - event: pull_request_close

clone:
  skip: true

dependencies:
  nixpkgs:
    - python3

steps:
  - name: Notify
    command: |
      python3 ./notify.py
`

	wf, err := FromFile("test.yml", []byte(yamlData))
	assert.NoError(t, err)

	assert.ElementsMatch(t, []string{"pull_request_close"}, wf.When[0].Event)

	assert.True(t, wf.CloneOpts.Skip, "Skip should be false")
}

func TestUnmarshalEnv(t *testing.T) {
	yamlData := `
when:
  - event: ["pull_request_close"]

clone:
  skip: false

environment:
  HOME: /home/foo bar/baz
  CGO_ENABLED: 1

steps:
  - name: Something
    command: echo "hello"
    environment:
      FOO: bar
      BAZ: qux
`

	wf, err := FromFile("test.yml", []byte(yamlData))
	assert.NoError(t, err)

	assert.Len(t, wf.Environment, 2)
	assert.Equal(t, "/home/foo bar/baz", wf.Environment["HOME"])
	assert.Equal(t, "1", wf.Environment["CGO_ENABLED"])
	assert.Equal(t, "bar", wf.Steps[0].Environment["FOO"])
	assert.Equal(t, "qux", wf.Steps[0].Environment["BAZ"])
}
