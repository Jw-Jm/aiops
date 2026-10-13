package assistant

import (
	"ops-platform/internal/analytics"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var relativeWindow = regexp.MustCompile(`(?:最近|过去)([0-9]{1,3}|一|二|两|三|四|五|六|七|八|九|十|三十)(分钟|小时|天)`)

// Calendar boundaries follow the frozen IANA timezone, including DST days.
func ResolveTimeWindow(message string, anchor time.Time, timezone string) (*analytics.QueryTimeRange, error) {
	location, err := time.LoadLocation(timezone)
	if err != nil || anchor.IsZero() || timezone == "Local" {
		return nil, analytics.ErrInvalid
	}
	matches := relativeWindow.FindAllStringSubmatch(message, -1)
	calendar := []string{}
	for _, word := range []string{"今天", "昨天", "前天"} {
		if strings.Contains(message, word) {
			calendar = append(calendar, word)
		}
	}
	if len(matches)+len(calendar) == 0 {
		return nil, nil
	}
	// Two periods may mean comparison: leave clarification to a bounded query,
	// never silently choose one of them as this turn's authoritative interval.
	if len(matches)+len(calendar) > 1 {
		return nil, nil
	}
	anchor = anchor.Truncate(time.Second)
	from, to := anchor.UTC(), anchor.UTC()
	if len(matches) == 1 {
		value := matches[0][1]
		n, err := strconv.Atoi(value)
		if err != nil {
			n = map[string]int{"一": 1, "二": 2, "两": 2, "三": 3, "四": 4, "五": 5, "六": 6, "七": 7, "八": 8, "九": 9, "十": 10, "三十": 30}[value]
		}
		if n < 1 {
			return nil, analytics.ErrInvalid
		}
		unit := map[string]time.Duration{"分钟": time.Minute, "小时": time.Hour, "天": 24 * time.Hour}[matches[0][2]]
		duration := time.Duration(n) * unit
		if duration > 31*24*time.Hour {
			return nil, analytics.ErrInvalid
		}
		from = to.Add(-duration)
	} else {
		local := anchor.In(location)
		day := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, location)
		switch calendar[0] {
		case "今天":
			from = day.UTC()
		case "昨天":
			from = day.AddDate(0, 0, -1).UTC()
			to = day.UTC()
		case "前天":
			from = day.AddDate(0, 0, -2).UTC()
			to = day.AddDate(0, 0, -1).UTC()
		}
	}
	if !to.After(from) {
		return nil, analytics.ErrInvalid
	}
	return &analytics.QueryTimeRange{From: from, To: to, Timezone: timezone}, nil
}
