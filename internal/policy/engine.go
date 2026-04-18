package policy

import (
	"fmt"
	"strings"

	"agent-gateway/internal/config"
	airuntime "agent-gateway/internal/runtime"
)

type Engine struct {
	cfg config.RoutingConfig
}

type Decision struct {
	IntentClass      string
	IntentReasons    []string
	IntentConfidence float64
	RouteTier        string
	FinalModel       string
	EnableWebSearch  bool
	Reason           string
}

func NewEngine(cfg config.RoutingConfig) *Engine {
	return &Engine{cfg: cfg}
}

func (e *Engine) Evaluate(ctx *airuntime.RequestContext) Decision {
	decision := Decision{
		IntentClass:      ctx.IntentClass,
		IntentReasons:    append([]string(nil), ctx.IntentReasons...),
		IntentConfidence: ctx.IntentConfidence,
		RouteTier:        "",
		FinalModel:       ctx.OriginalModel,
		EnableWebSearch:  ctx.AllowWebSearch,
		Reason:           "kept original model",
	}

	if !e.cfg.Enabled {
		return decision
	}

	for _, rule := range e.cfg.Rules {
		if !matchesIntent(rule.IntentClass, ctx.IntentClass) {
			continue
		}
		if !matchesPattern(rule.ModelPattern, ctx.OriginalModel) {
			continue
		}
		if rule.MinInputChars > 0 && ctx.EstimatedInputChars < rule.MinInputChars {
			continue
		}

		if rule.RouteTier != "" {
			decision.RouteTier = rule.RouteTier
			if model := e.resolveTierModel(rule.RouteTier); model != "" {
				decision.FinalModel = model
			}
		}
		if rule.EnableWebSearch != nil {
			decision.EnableWebSearch = *rule.EnableWebSearch && ctx.AllowWebSearch
		}
		decision.Reason = fmt.Sprintf("matched routing rule %q", rule.Name)
		return decision
	}

	return decision
}

func (e *Engine) resolveTierModel(tier string) string {
	for _, item := range e.cfg.Tiers {
		if item.Name == tier {
			return item.Model
		}
	}
	return ""
}

func matchesIntent(pattern, value string) bool {
	if pattern == "" || pattern == "*" {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(pattern), strings.TrimSpace(value))
}

func matchesPattern(pattern, value string) bool {
	if pattern == "" || pattern == "*" {
		return true
	}
	if !strings.Contains(pattern, "*") {
		return pattern == value
	}
	prefix := strings.TrimSuffix(pattern, "*")
	return strings.HasPrefix(value, prefix)
}
