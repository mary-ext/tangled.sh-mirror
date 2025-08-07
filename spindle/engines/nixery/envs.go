package nixery

import (
	"fmt"
)

type EnvVars []string

// ConstructEnvs converts a tangled.Pipeline_Step_Environment_Elem.{Key,Value}
// representation into a docker-friendly []string{"KEY=value", ...} slice.
func ConstructEnvs(envs map[string]string) EnvVars {
	var dockerEnvs EnvVars
	for k, v := range envs {
		ev := fmt.Sprintf("%s=%s", k, v)
		dockerEnvs = append(dockerEnvs, ev)
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
