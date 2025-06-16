package engine

import (
	"fmt"

	"tangled.sh/tangled.sh/core/api/tangled"
)

type EnvVars []string

// ConstructEnvs converts a tangled.Pipeline_Step_Environment_Elem.{Key,Value}
// representation into a docker-friendly []string{"KEY=value", ...} slice.
func ConstructEnvs(envs []*tangled.Pipeline_Step_Environment_Elem) EnvVars {
	var dockerEnvs EnvVars
	for _, env := range envs {
		if env != nil {
			ev := fmt.Sprintf("%s=%s", env.Key, env.Value)
			dockerEnvs = append(dockerEnvs, ev)
		}
	}
	return dockerEnvs
}

// Slice returns the EnvVar as a []string slice.
func (ev EnvVars) Slice() []string {
	return ev
}

// AddEnv adds a key=value string to the EnvVar.
func (ev *EnvVars) AddEnv(key, value string) {
	*ev = append(*ev, fmt.Sprintf("%s=%s", key, value))
}
