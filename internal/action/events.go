package action

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/auth"
	"time"
)

type Event struct {
	EventID     string          `json:"eventId"`
	EventSeq    int64           `json:"-"`
	ExecutionID uuid.UUID       `json:"executionId"`
	EventType   string          `json:"eventType"`
	OccurredAt  time.Time       `json:"occurredAt"`
	Payload     json.RawMessage `json:"payload"`
}

func (s Service) Events(ctx context.Context, a auth.RequestContext, id uuid.UUID, after int64) ([]Event, error) {
	out := []Event{}
	if after < 0 {
		return nil, ErrInvalid
	}
	err := s.with(ctx, a, func(tx pgx.Tx) error {
		e, err := executionTx(ctx, tx, a.TenantID, id)
		if err != nil {
			return err
		}
		if _, err = effectiveRead(ctx, tx, a, e.Binding); err != nil {
			return err
		}
		var latest int64
		if err = tx.QueryRow(ctx, `SELECT event_seq FROM action.executions WHERE tenant_id=$1 AND execution_id=$2`, a.TenantID, id).Scan(&latest); err != nil {
			return err
		}
		if after > latest {
			return ErrInvalid
		}
		rows, err := tx.Query(ctx, `SELECT event_seq,event_type,payload,created_at FROM action.events WHERE tenant_id=$1 AND execution_id=$2 AND event_seq>$3 ORDER BY event_seq LIMIT 100`, a.TenantID, id, after)
		if err != nil {
			return err
		}
		for rows.Next() {
			var v Event
			v.ExecutionID = id
			if err = rows.Scan(&v.EventSeq, &v.EventType, &v.Payload, &v.OccurredAt); err != nil {
				rows.Close()
				return err
			}
			v.EventID = id.String() + ":" + formatSeq(v.EventSeq)
			out = append(out, v)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for i := range out {
			if out[i].EventType == "output" {
				var ref struct {
					Seq int64 `json:"seq"`
				}
				if json.Unmarshal(out[i].Payload, &ref) != nil {
					return ErrInvalid
				}
				var chunk OutputChunk
				chunk.ExecutionID = id
				chunk.Seq = ref.Seq
				if err = tx.QueryRow(ctx, `SELECT stream,bytes,observed_at FROM action.output_chunks WHERE tenant_id=$1 AND execution_id=$2 AND seq=$3`, a.TenantID, id, ref.Seq).Scan(&chunk.Stream, &chunk.Bytes, &chunk.ObservedAt); err != nil {
					return ErrCursorExpired
				}
				out[i].Payload = Canonical(chunk)
			}
		}
		return nil
	})
	return out, err
}
