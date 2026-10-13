package assistant

import (
	"testing"
	"time"
)

func TestCalendarWindowUsesServerAnchorAndTimezone(t *testing.T) {
	anchor := time.Date(2026, 3, 9, 16, 0, 0, 0, time.UTC)
	window, err := ResolveTimeWindow("昨天的告警", anchor, "America/New_York")
	if err != nil || window.To.Sub(window.From) != 23*time.Hour {
		t.Fatal(window, err)
	}
	window, err = ResolveTimeWindow("过去七天的告警", anchor, "Asia/Shanghai")
	if err != nil || window.To.Sub(window.From) != 7*24*time.Hour || !window.To.Equal(anchor) {
		t.Fatal(window, err)
	}
	if window, err = ResolveTimeWindow("昨天与今天比较", anchor, "UTC"); err != nil || window != nil {
		t.Fatal("comparison silently chose one window")
	}
}
