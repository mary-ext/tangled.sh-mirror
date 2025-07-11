package engine

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestConstructEnvs(t *testing.T) {
	tests := []struct {
		name string
		in   map[string]string
		want EnvVars
	}{
		{
			name: "empty input",
			in:   make(map[string]string),
			want: EnvVars{},
		},
		{
			name: "single env var",
			in:   map[string]string{"FOO": "bar"},
			want: EnvVars{"FOO=bar"},
		},
		{
			name: "multiple env vars",
			in:   map[string]string{"FOO": "bar", "BAZ": "qux"},
			want: EnvVars{"FOO=bar", "BAZ=qux"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ConstructEnvs(tt.in)
			if got == nil {
				got = EnvVars{}
			}
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestAddEnv(t *testing.T) {
	ev := EnvVars{}
	ev.AddEnv("FOO", "bar")
	ev.AddEnv("BAZ", "qux")
	want := EnvVars{"FOO=bar", "BAZ=qux"}
	assert.ElementsMatch(t, want, ev)
}
