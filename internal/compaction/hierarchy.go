package compaction

import (
	"fmt"
	"strings"

	"github.com/freesoulcode/foya/internal/event"
)

const MaxCheckpointSegments = 4

// ExistingSegments returns a defensive copy of a versioned checkpoint's segments.
func ExistingSegments(checkpoint *Checkpoint) []Segment {
	if checkpoint == nil {
		return nil
	}
	return append([]Segment(nil), checkpoint.Segments...)
}

// CanAppendSegment reports whether a rolling checkpoint can preserve its
// existing summaries and add one summary for only the newly covered source.
func CanAppendSegment(previous *Checkpoint, plan Plan) bool {
	if previous == nil ||
		previous.ThroughSeq >= plan.ThroughSeq ||
		previous.HeadAnchorSeq != plan.HeadAnchorSeq {
		return false
	}
	return len(ExistingSegments(previous)) < MaxCheckpointSegments
}

// AppendSegment creates the bounded segmented representation for a rolling fold.
func AppendSegment(previous *Checkpoint, plan Plan, summary string) []Segment {
	segments := ExistingSegments(previous)
	return append(segments, Segment{
		FromSeq:      plan.NewFromSeq,
		ThroughSeq:   plan.ThroughSeq,
		SourceDigest: plan.NewSourceDigest,
		Summary:      strings.TrimSpace(summary),
	})
}

// ConsolidatedSegment describes one summary over the entire covered prefix.
func ConsolidatedSegment(plan Plan, summary string) []Segment {
	return []Segment{{
		FromSeq:      plan.CoveredFromSeq,
		ThroughSeq:   plan.ThroughSeq,
		SourceDigest: plan.SourceDigest,
		Summary:      strings.TrimSpace(summary),
	}}
}

// RenderSegments produces the portable text projection sent to providers.
func RenderSegments(segments []Segment) string {
	if len(segments) == 0 {
		return ""
	}
	if len(segments) == 1 {
		return strings.TrimSpace(segments[0].Summary)
	}
	var out strings.Builder
	out.WriteString("<checkpoint_segments>\n")
	for i, segment := range segments {
		_, _ = fmt.Fprintf(
			&out,
			"<segment index=\"%d\" from_seq=\"%d\" through_seq=\"%d\">\n%s\n</segment>\n",
			i+1,
			segment.FromSeq,
			segment.ThroughSeq,
			strings.TrimSpace(segment.Summary),
		)
	}
	out.WriteString("</checkpoint_segments>")
	return out.String()
}

func validSegments(checkpoint Checkpoint) bool {
	if len(checkpoint.Segments) == 0 {
		return true
	}
	var previousThrough event.Seq
	for i, segment := range checkpoint.Segments {
		if strings.TrimSpace(segment.Summary) == "" ||
			segment.ThroughSeq == 0 ||
			(i > 0 && segment.FromSeq <= previousThrough) ||
			segment.FromSeq > segment.ThroughSeq {
			return false
		}
		previousThrough = segment.ThroughSeq
	}
	return checkpoint.Segments[len(checkpoint.Segments)-1].ThroughSeq == checkpoint.ThroughSeq &&
		RenderSegments(checkpoint.Segments) == checkpoint.Summary
}
