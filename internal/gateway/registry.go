package gateway

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"agent-gateway/internal/store"
)

var ErrNoUpstreamAvailable = errors.New("no upstreams available")

type Upstream struct {
	ID              int64
	Name            string
	BaseURL         string
	APIKey          string
	Weight          int
	SupportedModels []string
}

type ModelRoute struct {
	ModelPattern string
	Strategy     string
	Upstreams    []string
}

type Registry struct {
	mu            sync.RWMutex
	upstreams     map[string]Upstream
	ordered       []Upstream
	routes        []ModelRoute
	routeCounters sync.Map
	globalCounter atomic.Uint64
}

func NewRegistry() *Registry {
	return &Registry{
		upstreams: make(map[string]Upstream),
	}
}

func (r *Registry) Load(ctx context.Context, st *store.SQLiteStore) error {
	upstreamRecords, err := st.LoadUpstreams(ctx)
	if err != nil {
		return err
	}

	routeRecords, err := st.LoadModelRoutes(ctx)
	if err != nil {
		return err
	}

	upstreams := make(map[string]Upstream, len(upstreamRecords))
	weighted := make([]Upstream, 0)
	for _, record := range upstreamRecords {
		upstream := Upstream{
			ID:              record.ID,
			Name:            record.Name,
			BaseURL:         record.BaseURL,
			APIKey:          record.APIKey,
			Weight:          record.Weight,
			SupportedModels: append([]string(nil), record.SupportedModels...),
		}
		if upstream.Weight <= 0 {
			upstream.Weight = 1
		}
		upstreams[upstream.Name] = upstream
		for i := 0; i < upstream.Weight; i++ {
			weighted = append(weighted, upstream)
		}
	}

	routes := make([]ModelRoute, 0, len(routeRecords))
	for _, record := range routeRecords {
		routes = append(routes, ModelRoute{
			ModelPattern: record.ModelPattern,
			Strategy:     normalizeStrategy(record.Strategy),
			Upstreams:    append([]string(nil), record.Upstreams...),
		})
	}

	sort.SliceStable(routes, func(i, j int) bool {
		return len(routes[i].ModelPattern) > len(routes[j].ModelPattern)
	})

	r.mu.Lock()
	r.upstreams = upstreams
	r.ordered = weighted
	r.routes = routes
	r.mu.Unlock()

	return nil
}

func (r *Registry) Candidates(model string) ([]Upstream, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if len(r.ordered) == 0 {
		return nil, ErrNoUpstreamAvailable
	}

	route := r.matchRoute(model)
	if route == nil {
		return r.defaultCandidates(model), nil
	}

	switch route.Strategy {
	case "fixed":
		for _, name := range route.Upstreams {
			if upstream, ok := r.upstreams[name]; ok && supportsModel(upstream, model) {
				return []Upstream{upstream}, nil
			}
		}
		return nil, fmt.Errorf("no upstream matches route for model %q", model)
	default:
		return r.routeCandidates(model, *route)
	}
}

func (r *Registry) Models() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	seen := make(map[string]struct{})
	var models []string

	for _, route := range r.routes {
		if strings.Contains(route.ModelPattern, "*") {
			continue
		}
		seen[route.ModelPattern] = struct{}{}
		models = append(models, route.ModelPattern)
	}

	for _, upstream := range r.upstreams {
		for _, model := range upstream.SupportedModels {
			if _, ok := seen[model]; ok {
				continue
			}
			seen[model] = struct{}{}
			models = append(models, model)
		}
	}

	sort.Strings(models)
	return models
}

func (r *Registry) matchRoute(model string) *ModelRoute {
	for _, route := range r.routes {
		if matchesPattern(route.ModelPattern, model) {
			copied := route
			return &copied
		}
	}
	return nil
}

func (r *Registry) defaultCandidates(model string) []Upstream {
	counter := r.globalCounter.Add(1)
	total := len(r.ordered)
	candidates := make([]Upstream, 0, total)
	for offset := 0; offset < total; offset++ {
		upstream := r.ordered[(int(counter)-1+offset)%total]
		if supportsModel(upstream, model) {
			candidates = appendUniqueUpstream(candidates, upstream)
		}
	}
	return candidates
}

func (r *Registry) routeCandidates(model string, route ModelRoute) ([]Upstream, error) {
	pool := make([]Upstream, 0)
	for _, name := range route.Upstreams {
		upstream, ok := r.upstreams[name]
		if !ok || !supportsModel(upstream, model) {
			continue
		}
		weight := upstream.Weight
		if weight <= 0 {
			weight = 1
		}
		for i := 0; i < weight; i++ {
			pool = append(pool, upstream)
		}
	}

	if len(pool) == 0 {
		return nil, fmt.Errorf("no upstream available for model %q", model)
	}

	value, _ := r.routeCounters.LoadOrStore(route.ModelPattern, &atomic.Uint64{})
	counter := value.(*atomic.Uint64).Add(1)

	candidates := make([]Upstream, 0, len(pool))
	for offset := 0; offset < len(pool); offset++ {
		upstream := pool[(int(counter)-1+offset)%len(pool)]
		candidates = appendUniqueUpstream(candidates, upstream)
	}

	return candidates, nil
}

func appendUniqueUpstream(list []Upstream, candidate Upstream) []Upstream {
	for _, existing := range list {
		if existing.Name == candidate.Name {
			return list
		}
	}
	return append(list, candidate)
}

func normalizeStrategy(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "fixed":
		return "fixed"
	default:
		return "round_robin"
	}
}

func matchesPattern(pattern, model string) bool {
	if pattern == "*" {
		return true
	}
	if !strings.Contains(pattern, "*") {
		return pattern == model
	}
	prefix := strings.TrimSuffix(pattern, "*")
	return strings.HasPrefix(model, prefix)
}

func supportsModel(upstream Upstream, model string) bool {
	if len(upstream.SupportedModels) == 0 {
		return true
	}
	for _, pattern := range upstream.SupportedModels {
		if matchesPattern(pattern, model) {
			return true
		}
	}
	return false
}
