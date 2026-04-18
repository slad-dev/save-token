package runtime

type RequestContext struct {
	RequestID            string
	Endpoint             string
	UserID               uint
	APIKeyID             uint
	OriginalModel        string
	FinalModel           string
	EstimatedInputChars  int
	EstimatedInputTokens int
	Stream               bool
	AllowWebSearch       bool
	AllowCache           bool
	AllowCompression     bool
	IntentClass          string
	IntentConfidence     float64
	IntentReasons        []string
	RouteTier            string
	SearchApplied        bool
	CompressionApplied   bool
	CacheHit             bool
	DecisionReason       string
}
