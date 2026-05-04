package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// DebateTotal counts completed debates by model and outcome.
	DebateTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "llm_consensus_debate_total",
		Help: "Total number of debates completed, labelled by model and status.",
	}, []string{"model", "status"})

	// PhaseDuration tracks how long each debate phase takes.
	PhaseDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "llm_consensus_phase_duration_seconds",
		Help:    "Duration of each debate phase in seconds.",
		Buckets: []float64{0.5, 1, 2, 5, 10, 30, 60},
	}, []string{"phase"})

	// TokensTotal counts tokens consumed per phase and type.
	TokensTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "llm_consensus_tokens_total",
		Help: "Total tokens consumed, labelled by phase and token type.",
	}, []string{"phase", "token_type"})

	// ActiveDebates tracks the number of debates currently running.
	ActiveDebates = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "llm_consensus_active_debates",
		Help: "Number of debates currently in progress.",
	})

	// ConsensusReachedTotal counts debates where consensus was actually reached.
	ConsensusReachedTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "llm_consensus_consensus_reached_total",
		Help: "Number of debates where consensus was reached (not a fallback).",
	})
)
