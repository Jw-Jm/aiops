package action

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/archive"
	platformcrypto "ops-platform/internal/crypto"
	"ops-platform/internal/persistence"
	"time"
)

type OutputArchive struct {
	Service Service
	Store   *archive.Store
}

// Archive prepares durable ciphertext before uploading, and recovers the exact
// object after an upload/callback crash without overwriting another version.
func (a OutputArchive) Archive(ctx context.Context, tenant, id uuid.UUID) error {
	if a.Store == nil || a.Service.Protector == nil {
		return ErrDenied
	}
	var envelope platformcrypto.Envelope
	var retain time.Time
	previews := map[string]any{}
	already := false
	err := persistence.WithTenantTx(ctx, a.Service.Pool, tenant, func(tx pgx.Tx) error {
		e, err := executionTx(ctx, tx, tenant, id)
		if err != nil {
			return err
		}
		if e.State != "succeeded" && e.State != "failed" && e.State != "cancelled" && e.State != "execution_unknown" {
			return ErrConflict
		}
		var raw []byte
		err = tx.QueryRow(ctx, `SELECT envelope,retain_until,verified_at IS NOT NULL FROM action.output_archives WHERE tenant_id=$1 AND execution_id=$2`, tenant, id).Scan(&raw, &retain, &already)
		if err == nil {
			if already {
				return nil
			}
			return json.Unmarshal(raw, &envelope)
		}
		if err != pgx.ErrNoRows {
			return err
		}
		chunks := []OutputChunk{}
		rows, err := tx.Query(ctx, `SELECT seq,stream,bytes,observed_at FROM action.output_chunks WHERE tenant_id=$1 AND execution_id=$2 ORDER BY seq`, tenant, id)
		if err != nil {
			return err
		}
		for rows.Next() {
			c := OutputChunk{ExecutionID: id}
			if err = rows.Scan(&c.Seq, &c.Stream, &c.Bytes, &c.ObservedAt); err != nil {
				rows.Close()
				return err
			}
			chunks = append(chunks, c)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, stream := range []string{"stdout", "stderr"} {
			first, last := []byte{}, []byte{}
			for _, c := range chunks {
				if c.Stream != stream {
					continue
				}
				if len(first) < 2048 {
					first = append(first, c.Bytes[:min(len(c.Bytes), 2048-len(first))]...)
				}
				last = append(last, c.Bytes...)
				if len(last) > 2048 {
					last = last[len(last)-2048:]
				}
			}
			previews[stream] = map[string]any{"first": first, "last": last, "encoding": "base64"}
		}
		plain := Canonical(chunks)
		envelope, err = a.Service.Protector.Seal(ctx, tenant, id, plain)
		clear(plain)
		if err != nil {
			return err
		}
		retain = time.Now().UTC().Add(365 * 24 * time.Hour)
		_, err = tx.Exec(ctx, `INSERT INTO action.output_archives(tenant_id,execution_id,envelope,retain_until,plaintext_digest) VALUES($1,$2,$3,$4,$5)`, tenant, id, Canonical(envelope), retain, envelope.PlaintextDigest)
		return err
	})
	if err != nil {
		return err
	}
	if already {
		return nil
	}
	cipher := Canonical(envelope)
	ref, exists, err := a.Store.Find(ctx, tenant, id, "command-output")
	if err != nil {
		return err
	}
	if exists {
		if ref.Digest != Digest(cipher) {
			return archive.ErrDigestMismatch
		}
	} else {
		ref, err = a.Store.Put(ctx, archive.ObjectDescriptor{TenantID: tenant, ObjectID: id, Category: "command-output", ContentType: "application/json", ExpectedDigest: Digest(cipher), RetainUntil: retain}, bytes.NewReader(cipher))
		if err != nil {
			return err
		}
	}
	// Verify decryption as well as the Store's object digest before chunk cleanup.
	recovered, err := a.Store.Get(ctx, tenant, ref)
	if err != nil {
		return err
	}
	var verified platformcrypto.Envelope
	if json.Unmarshal(recovered, &verified) != nil {
		return ErrInvalid
	}
	plain, err := a.Service.Protector.Open(ctx, tenant, id, verified)
	if err != nil {
		return err
	}
	defer clear(plain)
	if len(previews) == 0 {
		var chunks []OutputChunk
		if json.Unmarshal(plain, &chunks) != nil {
			return ErrInvalid
		}
		for _, stream := range []string{"stdout", "stderr"} {
			first, last := []byte{}, []byte{}
			for _, c := range chunks {
				if c.Stream != stream {
					continue
				}
				if len(first) < 2048 {
					first = append(first, c.Bytes[:min(len(c.Bytes), 2048-len(first))]...)
				}
				last = append(last, c.Bytes...)
				if len(last) > 2048 {
					last = last[len(last)-2048:]
				}
			}
			previews[stream] = map[string]any{"first": first, "last": last, "encoding": "base64"}
		}
	}
	if Digest(plain) != envelope.PlaintextDigest {
		return archive.ErrDigestMismatch
	}
	return persistence.WithTenantTx(ctx, a.Service.Pool, tenant, func(tx pgx.Tx) error {
		var old []byte
		if err := tx.QueryRow(ctx, `SELECT object_ref FROM action.output_archives WHERE tenant_id=$1 AND execution_id=$2 FOR UPDATE`, tenant, id).Scan(&old); err != nil {
			return err
		}
		if len(old) > 0 {
			return nil
		}
		if _, err := tx.Exec(ctx, `UPDATE action.output_archives SET object_ref=$3,verified_at=clock_timestamp(),envelope=NULL WHERE tenant_id=$1 AND execution_id=$2`, tenant, id, Canonical(ref)); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE action.executions SET output_archive_ref=$3,output_digest=$4,archived_at=clock_timestamp(),output_previews=$5 WHERE tenant_id=$1 AND execution_id=$2`, tenant, id, string(Canonical(ref)), envelope.PlaintextDigest, Canonical(previews)); err != nil {
			return err
		}
		if err := appendAudit(ctx, tx, workerActor(tenant), id, "command.output_archived", map[string]any{"objectRef": ref, "plaintextDigest": envelope.PlaintextDigest}); err != nil {
			return err
		}
		return event(ctx, tx, tenant, id, "output_archived", map[string]any{"objectRef": ref, "digest": envelope.PlaintextDigest})
	})
}
func (a OutputArchive) Cleanup(ctx context.Context, tenant uuid.UUID) error {
	return persistence.WithTenantTx(ctx, a.Service.Pool, tenant, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM action.output_chunks c USING action.executions e,action.output_archives a WHERE c.tenant_id=$1 AND e.tenant_id=c.tenant_id AND e.execution_id=c.execution_id AND a.tenant_id=e.tenant_id AND a.execution_id=e.execution_id AND a.verified_at IS NOT NULL AND a.object_ref IS NOT NULL AND e.archived_at IS NOT NULL AND e.completed_at+interval '24 hours'<clock_timestamp() AND e.archived_at+interval '24 hours'<clock_timestamp()`, tenant)
		return err
	})
}
