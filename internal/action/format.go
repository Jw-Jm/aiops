package action

import (
	"errors"
	"strconv"
)

var ErrCursorExpired = errors.New("command output cursor expired; use archived output")

func formatSeq(v int64) string { return strconv.FormatInt(v, 10) }
