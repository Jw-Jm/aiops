package httpapi

import (
	"context"
	"errors"
)

func generationRead(ctx context.Context, paged bool, read func() ([]byte, error)) ([]byte, error) {
	for attempt := 0; ; attempt++ {
		data, err := read()
		var conflict callError
		if err == nil || paged || attempt >= 2 || ctx.Err() != nil || !errors.As(err, &conflict) || conflict.Status != 409 || conflict.Code != "STALE_CONTEXT" {
			return data, err
		}
	}
}
