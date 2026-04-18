package observe

import (
	"context"
	"strings"

	"agent-gateway/internal/auth"
	"agent-gateway/internal/store"
)

type RouteTraceService struct {
	store *store.SQLiteStore
}

type RecordRouteTraceInput struct {
	RequestID            string
	Principal            *auth.Principal
	Endpoint             string
	OriginalModel        string
	FinalModel           string
	RouteTier            string
	IntentClass          string
	IntentConfidence     float64
	EstimatedInputChars  int
	EstimatedInputTokens int
	SearchApplied        bool
	CompressionApplied   bool
	CacheHit             bool
	DecisionReason       string
	IntentReasons        []string
}

func NewRouteTraceService(st *store.SQLiteStore) *RouteTraceService {
	return &RouteTraceService{store: st}
}

func (s *RouteTraceService) Record(ctx context.Context, input RecordRouteTraceInput) error {
	if s == nil || s.store == nil || input.Principal == nil {
		return nil
	}

	trace := store.RouteTrace{
		RequestID:            input.RequestID,
		UserID:               input.Principal.User.ID,
		APIKeyID:             input.Principal.APIKey.ID,
		Endpoint:             input.Endpoint,
		OriginalModel:        input.OriginalModel,
		FinalModel:           input.FinalModel,
		RouteTier:            input.RouteTier,
		IntentClass:          input.IntentClass,
		IntentConfidence:     input.IntentConfidence,
		EstimatedInputChars:  input.EstimatedInputChars,
		EstimatedInputTokens: input.EstimatedInputTokens,
		SearchApplied:        input.SearchApplied,
		CompressionApplied:   input.CompressionApplied,
		CacheHit:             input.CacheHit,
		DecisionReason:       input.DecisionReason,
		IntentReasons:        strings.Join(input.IntentReasons, ","),
	}

	return s.store.CreateRouteTrace(ctx, trace)
}
