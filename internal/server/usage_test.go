package server

import (
	"net/http"
	"testing"

	"github.com/freesoulcode/foya/internal/backend"
)

func TestUsageStatisticsRoute(t *testing.T) {
	handler, _ := newQueueTestServer(t)

	var statistics backend.UsageStatistics
	if code := requestJSON(
		t,
		handler,
		http.MethodGet,
		"/usage?days=7",
		nil,
		&statistics,
	); code != http.StatusOK {
		t.Fatalf("usage statistics status = %d", code)
	}
	if statistics.RangeDays != 7 || len(statistics.Activity) != 365 {
		t.Fatalf("usage statistics = %#v", statistics)
	}

	if code := requestJSON(
		t,
		handler,
		http.MethodGet,
		"/usage?days=14",
		nil,
		nil,
	); code != http.StatusBadRequest {
		t.Fatalf("invalid usage range status = %d", code)
	}
}
