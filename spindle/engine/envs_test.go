package engine

import (
	"reflect"
	"testing"

	"tangled.sh/tangled.sh/core/api/tangled"
)

func TestConstructEnvs(t *testing.T) {
	tests := []struct {
		name string
		in   []*tangled.Pipeline_Step_Environment_Elem
		want EnvVars
	}{
		{
			name: "empty input",
			in:   []*tangled.Pipeline_Step_Environment_Elem{},
			want: EnvVars{},
		},
		{
			name: "single env var",
			in: []*tangled.Pipeline_Step_Environment_Elem{
				{Key: "FOO", Value: "bar"},
			},
			want: EnvVars{"FOO=bar"},
		},
		{
			name: "multiple env vars",
			in: []*tangled.Pipeline_Step_Environment_Elem{
				{Key: "FOO", Value: "bar"},
				{Key: "BAZ", Value: "qux"},
			},
			want: EnvVars{"FOO=bar", "BAZ=qux"},
		},
		{
			name: "nil entries are skipped",
			in: []*tangled.Pipeline_Step_Environment_Elem{
				nil,
				{Key: "FOO", Value: "bar"},
			},
			want: EnvVars{"FOO=bar"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ConstructEnvs(tt.in)

			if got == nil {
				got = EnvVars{}
			}

			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ConstructEnvs() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAddEnv(t *testing.T) {
	ev := EnvVars{}
	ev.AddEnv("FOO", "bar")
	ev.AddEnv("BAZ", "qux")

	want := EnvVars{"FOO=bar", "BAZ=qux"}
	if !reflect.DeepEqual(ev, want) {
		t.Errorf("AddEnv result = %v, want %v", ev, want)
	}
}
