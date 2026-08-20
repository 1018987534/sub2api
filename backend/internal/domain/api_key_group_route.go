package domain

// APIKeyGroupRoute is one ordered group candidate configured for an API key.
// MaxRateMultiplier nil means the customer accepts any effective multiplier.
type APIKeyGroupRoute struct {
	GroupID           int64    `json:"group_id"`
	MaxRateMultiplier *float64 `json:"max_rate_multiplier,omitempty"`
}
