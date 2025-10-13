package pages

import (
	"html/template"
	"log/slog"
	"testing"

	"tangled.org/core/appview/config"
	"tangled.org/core/idresolver"
)

func TestPages_funcMap(t *testing.T) {
	tests := []struct {
		name string // description of this test case
		// Named input parameters for receiver constructor.
		config *config.Config
		res    *idresolver.Resolver
		l      *slog.Logger
		want   template.FuncMap
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewPages(tt.config, tt.res, tt.l)
			got := p.funcMap()
			// TODO: update the condition below to compare got with tt.want.
			if true {
				t.Errorf("funcMap() = %v, want %v", got, tt.want)
			}
		})
	}
}
