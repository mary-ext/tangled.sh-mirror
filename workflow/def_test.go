package workflow

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestUnmarshalWorkflow(t *testing.T) {
	yamlData := `
when:
  - event: ["push", "pull_request"]
    branch: ["main", "develop"]`

	wf, err := FromFile("test.yml", []byte(yamlData))
	assert.NoError(t, err, "YAML should unmarshal without error")

	assert.Len(t, wf.When, 1, "Should have one constraint")
	assert.ElementsMatch(t, []string{"main", "develop"}, wf.When[0].Branch)
	assert.ElementsMatch(t, []string{"push", "pull_request"}, wf.When[0].Event)

	assert.False(t, wf.CloneOpts.Skip, "Skip should default to false")
}

func TestUnmarshalCloneFalse(t *testing.T) {
	yamlData := `
when:
  - event: pull_request_close

clone:
  skip: true
`

	wf, err := FromFile("test.yml", []byte(yamlData))
	assert.NoError(t, err)

	assert.ElementsMatch(t, []string{"pull_request_close"}, wf.When[0].Event)

	assert.True(t, wf.CloneOpts.Skip, "Skip should be false")
}
