package observability

import (
	"testing"
	"time"

	"ecs/internal/protocol"
)

func TestMetricsCountsRunsAndVerification(t *testing.T) {
	metrics := NewMetrics()
	metrics.ObserveReply(protocol.ReplyResponse{
		ToolRuns: []protocol.ToolRun{
			{Name: "clock"}, {Name: "meeting_fact_extract"},
		},
		SkillRuns: []protocol.SkillRun{{
			Name:     "meeting.fact.extract",
			ToolRuns: []protocol.ToolRun{{Name: "meeting_fact_extract"}},
		}},
		Verification: &protocol.Verification{Validation: "accepted"},
	})
	value := metrics.Snapshot(time.Now().Add(time.Second))
	if value.Requests != 1 || value.ToolRuns["clock"] != 1 ||
		value.ToolRuns["meeting_fact_extract"] != 1 ||
		value.SkillRuns["meeting.fact.extract"] != 1 ||
		value.Verifications["accepted"] != 1 {
		t.Fatalf("unexpected metrics: %#v", value)
	}
}
