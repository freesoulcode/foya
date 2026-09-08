package conversation

import (
	"context"
	"testing"
	"time"
)

func TestResolveFileReviewClearsPendingProjection(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	before := []byte("before\n")
	after := []byte("after\n")
	changeSeq, err := store.Append(ctx, Event{
		Kind:    KindMessageEnd,
		Session: "session-1",
		Time:    time.Now(),
		Payload: Message{
			Role: RoleTool,
			FileChange: &FileChange{
				Path:            "/workspace/file.txt",
				BeforeExists:    true,
				BeforeMode:      0o644,
				AfterMode:       0o644,
				BeforeBlob:      fileBlobHash(before),
				AfterBlob:       fileBlobHash(after),
				BeforeContent:   before,
				AfterContent:    after,
				ContentCaptured: true,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	review, err := store.PendingFileReview(ctx, "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if review.ThroughSeq != changeSeq ||
		len(review.Changes) != 1 ||
		review.Changes[0].EventSeq != changeSeq {
		t.Fatalf("pending review = %#v", review)
	}

	resolved, err := store.ResolveFileReview(
		ctx,
		"session-1",
		changeSeq,
		FileReviewKept,
		[]RewindFileResult{{
			Path:   "/workspace/file.txt",
			Action: RewindFileKept,
		}},
		"",
	)
	if err != nil {
		t.Fatal(err)
	}
	payload, ok := resolved.Payload.(FileReviewResolved)
	if resolved.Kind != KindFileReviewResolved ||
		!ok ||
		payload.Action != FileReviewKept ||
		payload.ThroughSeq != changeSeq {
		t.Fatalf("resolved event = %#v", resolved)
	}

	review, err = store.PendingFileReview(ctx, "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(review.Changes) != 0 || review.ThroughSeq != 0 {
		t.Fatalf("review remained pending: %#v", review)
	}
}

func TestResolveFileReviewRejectsStalePendingSet(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	appendChange := func(path, before, after string) Seq {
		t.Helper()
		seq, err := store.Append(ctx, Event{
			Kind:    KindMessageEnd,
			Session: "session-1",
			Time:    time.Now(),
			Payload: Message{
				Role: RoleTool,
				FileChange: &FileChange{
					Path:            path,
					BeforeExists:    true,
					BeforeMode:      0o644,
					AfterMode:       0o644,
					BeforeBlob:      fileBlobHash([]byte(before)),
					AfterBlob:       fileBlobHash([]byte(after)),
					BeforeContent:   []byte(before),
					AfterContent:    []byte(after),
					ContentCaptured: true,
				},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		return seq
	}

	first := appendChange("/workspace/first.txt", "a\n", "b\n")
	appendChange("/workspace/second.txt", "c\n", "d\n")
	_, err := store.ResolveFileReview(
		ctx,
		"session-1",
		first,
		FileReviewKept,
		nil,
		"",
	)
	if err != ErrFileReviewChanged {
		t.Fatalf("stale review error = %v", err)
	}
}
