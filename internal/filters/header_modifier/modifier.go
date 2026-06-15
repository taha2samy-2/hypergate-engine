package header_modifier

import (
	"github.com/taha/myprog/internal/engine"
)

type HeaderOptions struct {
	Add      map[string]string `yaml:"add"`
	Override map[string]string `yaml:"override"`
	Remove   []string          `yaml:"remove"`
}

type HeaderModifierConfig struct {
	Upstream   HeaderOptions     `yaml:"upstream"`
	Downstream HeaderOptions     `yaml:"downstream"`
	Add        map[string]string `yaml:"add"`
	Override   map[string]string `yaml:"override"`
	Remove     []string          `yaml:"remove"`
}

type HeaderModifierFilter struct {
	config HeaderModifierConfig
}

func NewHeaderModifierFilter(config HeaderModifierConfig) *HeaderModifierFilter {
	return &HeaderModifierFilter{config: config}
}

func (f *HeaderModifierFilter) Execute(ctx *engine.RequestContext) error {
	for k, v := range f.config.Add {
		ctx.SetHeaderUpstream(k, v)
	}
	for k, v := range f.config.Override {
		ctx.SetHeaderUpstream(k, v)
	}
	for _, k := range f.config.Remove {
		ctx.RemoveHeaderUpstream(k)
	}

	for k, v := range f.config.Upstream.Add {
		ctx.SetHeaderUpstream(k, v)
	}
	for k, v := range f.config.Upstream.Override {
		ctx.SetHeaderUpstream(k, v)
	}
	for _, k := range f.config.Upstream.Remove {
		ctx.RemoveHeaderUpstream(k)
	}

	for k, v := range f.config.Downstream.Add {
		ctx.SetHeaderDownstream(k, v)
	}
	for k, v := range f.config.Downstream.Override {
		ctx.SetHeaderDownstream(k, v)
	}
	for _, k := range f.config.Downstream.Remove {
		ctx.RemoveHeaderDownstream(k)
	}

	return nil
}
