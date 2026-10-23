package httpapi

import (
	"encoding/base64"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"ops-platform/internal/action"
)

// Pagination follows authorization filtering, so a cursor cannot select a hidden profile.
// shortcut: like the existing registry list, this loads the tenant catalog; use DB keyset paging when catalogs grow beyond configuration-scale lists.
func paginateExecutionProfiles(items []action.Profile, cursor string, limit int) ([]action.Profile, *string, error) {
	if limit < 1 || limit > 200 || len(cursor) > 128 {
		return nil, nil, action.ErrInvalid
	}
	start := 0
	if cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil {
			return nil, nil, action.ErrInvalid
		}
		parts := strings.Split(string(raw), "@")
		if len(parts) != 2 {
			return nil, nil, action.ErrInvalid
		}
		id, err := uuid.Parse(parts[0])
		if err != nil || id == uuid.Nil {
			return nil, nil, action.ErrInvalid
		}
		version, err := strconv.Atoi(parts[1])
		if err != nil || version < 1 {
			return nil, nil, action.ErrInvalid
		}
		found := false
		for i, item := range items {
			if item.ID == id && item.Version == version {
				start, found = i+1, true
				break
			}
		}
		if !found {
			return nil, nil, action.ErrInvalid
		}
	}
	end := min(start+limit, len(items))
	var next *string
	if end < len(items) {
		last := items[end-1]
		value := base64.RawURLEncoding.EncodeToString([]byte(last.ID.String() + "@" + strconv.Itoa(last.Version)))
		next = &value
	}
	return items[start:end], next, nil
}
