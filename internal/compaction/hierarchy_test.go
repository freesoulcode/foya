package compaction

import (
	"strings"
	"testing"

	"github.com/freesoulcode/foya/internal/event"
)

func TestAppendSegmentAndRender(t *testing.T) {
	base := Segment{
		FromSeq:      1,
		ThroughSeq:   10,
		SourceDigest: "old",
		Summary:      validSummary,
	}
	previous := &Checkpoint{
		ThroughSeq:   10,
		SourceDigest: "old",
		Segments:     []Segment{base},
		Summary:      RenderSegments([]Segment{base}),
	}
	plan := Plan{
		HeadAnchorSeq:   0,
		ThroughSeq:      20,
		NewFromSeq:      11,
		NewSourceDigest: "new",
	}
	if !CanAppendSegment(previous, plan) {
		t.Fatal("expected appendable segment")
	}
	segments := AppendSegment(previous, plan, validSummary)
	if len(segments) != 2 {
		t.Fatalf("segments = %#v", segments)
	}
	rendered := RenderSegments(segments)
	if !strings.Contains(rendered, "<checkpoint_segments>") ||
		!strings.Contains(rendered, `through_seq="20"`) {
		t.Fatalf("rendered segments = %s", rendered)
	}
}

func TestSegmentLimitForcesConsolidation(t *testing.T) {
	segments := make([]Segment, MaxCheckpointSegments)
	for i := range segments {
		segments[i] = Segment{
			FromSeq:    eventSeqForTest(i*10 + 1),
			ThroughSeq: eventSeqForTest((i + 1) * 10),
			Summary:    validSummary,
		}
	}
	previous := &Checkpoint{
		ThroughSeq: 40,
		Segments:   segments,
		Summary:    RenderSegments(segments),
	}
	if CanAppendSegment(previous, Plan{ThroughSeq: 50}) {
		t.Fatal("segment limit should force consolidation")
	}
}

func eventSeqForTest(value int) event.Seq {
	return event.Seq(value)
}
