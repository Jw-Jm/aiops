package configregistry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"ops-platform/internal/audit"
	"ops-platform/internal/auth"
	"ops-platform/internal/bundle"
	"ops-platform/internal/persistence"
)

type Service struct {
	pool     persistence.TxBeginner
	verifier SignatureVerifier
}

func NewService(pool persistence.TxBeginner, verifier SignatureVerifier) (*Service, error) {
	if pool == nil || verifier == nil {
		return nil, errors.New("configuration registry database pool and signature verifier are required")
	}
	return &Service{pool: pool, verifier: verifier}, nil
}

func (service *Service) CreateDraft(ctx context.Context, tx pgx.Tx, actor auth.RequestContext, command DraftCommand) (Draft, error) {
	if !validKind(command.Kind) || !validLogicalName(command.LogicalName) || !jsonObject(command.Content) {
		return Draft{}, ErrInvalidInput
	}
	if err := authorizeAdmin(ctx, tx, actor); err != nil {
		return Draft{}, err
	}
	draftID := uuid.Must(uuid.NewV7())
	var result Draft
	err := tx.QueryRow(ctx, `INSERT INTO platform.registry_drafts
		(tenant_id, draft_id, kind, logical_name, content, updated_by)
		VALUES ($1, $2, $3, $4, $5::jsonb, $6)
		RETURNING tenant_id, draft_id, kind, logical_name, revision, content, status, updated_by, created_at, updated_at`,
		actor.TenantID, draftID, command.Kind, command.LogicalName, []byte(command.Content), actor.Subject).
		Scan(&result.TenantID, &result.DraftID, &result.Kind, &result.LogicalName, &result.Revision,
			&result.Content, &result.Status, &result.UpdatedBy, &result.CreatedAt, &result.UpdatedAt)
	if err != nil {
		return Draft{}, fmt.Errorf("create registry draft: %w", err)
	}
	if err := appendAudit(ctx, tx, actor, "config_registry.draft_created", "registry_draft", draftID,
		map[string]any{"kind": command.Kind, "logical_name": command.LogicalName, "revision": result.Revision}); err != nil {
		return Draft{}, err
	}
	return result, nil
}

func (service *Service) UpdateDraft(ctx context.Context, tx pgx.Tx, actor auth.RequestContext, ref DraftRef, command DraftUpdateCommand) (Draft, error) {
	if ref.TenantID == uuid.Nil || ref.DraftID == uuid.Nil || command.ExpectedRevision < 1 || !jsonObject(command.Content) {
		return Draft{}, ErrInvalidInput
	}
	if err := authorizeAdmin(ctx, tx, actor); err != nil {
		return Draft{}, err
	}
	draft, err := loadDraft(ctx, tx, actor.TenantID, ref.DraftID, true)
	if err != nil {
		return Draft{}, err
	}
	if ref.TenantID != actor.TenantID {
		return Draft{}, ErrNotFound
	}
	if draft.Status != "draft" {
		return Draft{}, ErrImmutable
	}
	if draft.Revision != command.ExpectedRevision {
		return Draft{}, ErrRevisionConflict
	}
	var result Draft
	err = tx.QueryRow(ctx, `UPDATE platform.registry_drafts
		SET content = $1::jsonb, revision = revision + 1, updated_by = $2, updated_at = clock_timestamp()
		WHERE tenant_id = $3 AND draft_id = $4 AND status = 'draft' AND revision = $5
		RETURNING tenant_id, draft_id, kind, logical_name, revision, content, status, updated_by, created_at, updated_at`,
		[]byte(command.Content), actor.Subject, actor.TenantID, ref.DraftID, command.ExpectedRevision).
		Scan(&result.TenantID, &result.DraftID, &result.Kind, &result.LogicalName, &result.Revision,
			&result.Content, &result.Status, &result.UpdatedBy, &result.CreatedAt, &result.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Draft{}, ErrRevisionConflict
	}
	if err != nil {
		return Draft{}, fmt.Errorf("update registry draft: %w", err)
	}
	if err := appendAudit(ctx, tx, actor, "config_registry.draft_updated", "registry_draft", ref.DraftID,
		map[string]any{"kind": result.Kind, "logical_name": result.LogicalName, "revision": result.Revision}); err != nil {
		return Draft{}, err
	}
	return result, nil
}

func (service *Service) Publish(ctx context.Context, tx pgx.Tx, actor auth.RequestContext, ref DraftRef, expectedRevision int64, signature PublicationSignature) (PublishedVersion, error) {
	if ref.TenantID == uuid.Nil || ref.DraftID == uuid.Nil || expectedRevision < 1 || signature.KeyID == "" || len(signature.Signature) == 0 {
		return PublishedVersion{}, ErrInvalidInput
	}
	if err := authorizeAdmin(ctx, tx, actor); err != nil {
		return PublishedVersion{}, err
	}
	draft, err := loadDraft(ctx, tx, actor.TenantID, ref.DraftID, true)
	if err != nil {
		return PublishedVersion{}, err
	}
	if ref.TenantID != actor.TenantID {
		return PublishedVersion{}, ErrNotFound
	}
	if draft.Status != "draft" {
		return PublishedVersion{}, ErrImmutable
	}
	if draft.Revision != expectedRevision {
		return PublishedVersion{}, ErrRevisionConflict
	}
	if err := ValidateContent(draft.Kind, draft.Content); err != nil {
		return PublishedVersion{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	if !contentNameMatches(draft.Content, draft.LogicalName) {
		return PublishedVersion{}, ErrInvalidInput
	}
	message, digest, err := SigningPayload(draft.TenantID, draft.Kind, draft.LogicalName, draft.Content)
	if err != nil {
		return PublishedVersion{}, err
	}
	if err := service.verifier.Verify(ctx, signature.KeyID, message, signature.Signature); err != nil {
		return PublishedVersion{}, ErrInvalidSignature
	}
	if err := lockIdentity(ctx, tx, actor.TenantID.String()+"|"+string(draft.Kind)+"|"+draft.LogicalName); err != nil {
		return PublishedVersion{}, err
	}
	var versionNumber int32
	if err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(version_number), 0) + 1 FROM platform.registry_versions
		WHERE tenant_id = $1 AND kind = $2 AND logical_name = $3`, actor.TenantID, draft.Kind, draft.LogicalName).Scan(&versionNumber); err != nil {
		return PublishedVersion{}, fmt.Errorf("allocate registry version number: %w", err)
	}
	versionID := uuid.Must(uuid.NewV7())
	var result PublishedVersion
	err = tx.QueryRow(ctx, `INSERT INTO platform.registry_versions
		(tenant_id, version_id, draft_id, kind, logical_name, version_number, content, digest, signature, signer_key_id, published_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb, $8, $9, $10, $11)
		RETURNING tenant_id, version_id, draft_id, kind, logical_name, version_number, content, digest,
			signature, signer_key_id, published_by, published_at, retired_at`,
		actor.TenantID, versionID, draft.DraftID, draft.Kind, draft.LogicalName, versionNumber,
		[]byte(draft.Content), digest, signature.Signature, signature.KeyID, actor.Subject).
		Scan(&result.TenantID, &result.VersionID, &result.DraftID, &result.Kind, &result.LogicalName,
			&result.VersionNumber, &result.Content, &result.Digest, &result.Signature, &result.SignerKeyID,
			&result.PublishedBy, &result.PublishedAt, &result.RetiredAt)
	if err != nil {
		return PublishedVersion{}, mapRegistryWriteError(err)
	}
	command, err := tx.Exec(ctx, `UPDATE platform.registry_drafts SET status = 'published', revision = revision + 1,
		updated_by = $1, updated_at = clock_timestamp() WHERE tenant_id = $2 AND draft_id = $3 AND status = 'draft' AND revision = $4`,
		actor.Subject, actor.TenantID, draft.DraftID, expectedRevision)
	if err != nil {
		return PublishedVersion{}, fmt.Errorf("mark registry draft published: %w", err)
	}
	if command.RowsAffected() != 1 {
		return PublishedVersion{}, ErrRevisionConflict
	}
	if err := appendAudit(ctx, tx, actor, "config_registry.version_published", "registry_version", versionID,
		map[string]any{"kind": draft.Kind, "logical_name": draft.LogicalName, "version_number": versionNumber,
			"digest": digest, "signer_key_id": signature.KeyID}); err != nil {
		return PublishedVersion{}, err
	}
	return result, nil
}

func (service *Service) PublishPolicyBundle(ctx context.Context, tx pgx.Tx, actor auth.RequestContext, ref DraftRef, expectedRevision int64, signature PublicationSignature) (PublishedVersion, error) {
	if ref.TenantID == uuid.Nil || ref.DraftID == uuid.Nil {
		return PublishedVersion{}, ErrInvalidInput
	}
	if ref.TenantID != actor.TenantID {
		return PublishedVersion{}, ErrNotFound
	}
	if err := authorizeAdmin(ctx, tx, actor); err != nil {
		return PublishedVersion{}, err
	}
	draft, err := loadDraft(ctx, tx, actor.TenantID, ref.DraftID, false)
	if err != nil {
		return PublishedVersion{}, err
	}
	if draft.Kind != KindPolicy {
		return PublishedVersion{}, ErrInvalidInput
	}
	return service.Publish(ctx, tx, actor, ref, expectedRevision, signature)
}

func (service *Service) Activate(ctx context.Context, tx pgx.Tx, actor auth.RequestContext, ref VersionRef, kind Kind, logicalName string, scope Scope, expectedRevision int64) (Activation, error) {
	if ref.TenantID == uuid.Nil || ref.VersionID == uuid.Nil || !validKind(kind) || !validLogicalName(logicalName) || expectedRevision < 0 || scope.Validate() != nil {
		return Activation{}, ErrInvalidInput
	}
	if ref.TenantID != actor.TenantID {
		return Activation{}, ErrNotFound
	}
	if err := authorizeAdmin(ctx, tx, actor); err != nil {
		return Activation{}, err
	}
	version, err := loadVersion(ctx, tx, actor.TenantID, ref.VersionID, true)
	if err != nil {
		return Activation{}, err
	}
	if version.Kind != kind || version.LogicalName != logicalName || version.RetiredAt != nil {
		return Activation{}, ErrInvalidInput
	}
	if scope.Type != ScopeTenant {
		var active bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM platform.cluster_registrations
			WHERE tenant_id = $1 AND cluster_id = $2 AND status = 'active')`, actor.TenantID, scope.ClusterID).Scan(&active); err != nil {
			return Activation{}, fmt.Errorf("validate registry activation cluster: %w", err)
		}
		if !active {
			return Activation{}, ErrInvalidInput
		}
	}
	if err := lockIdentity(ctx, tx, scope.key(actor.TenantID, kind, logicalName)); err != nil {
		return Activation{}, err
	}
	current, currentErr := loadActivation(ctx, tx, actor.TenantID, kind, logicalName, scope, true)
	if currentErr != nil && !errors.Is(currentErr, ErrNotFound) {
		return Activation{}, currentErr
	}
	var result Activation
	if errors.Is(currentErr, ErrNotFound) {
		if expectedRevision != 0 {
			return Activation{}, ErrRevisionConflict
		}
		activationID := uuid.Must(uuid.NewV7())
		err = tx.QueryRow(ctx, `INSERT INTO platform.registry_activations
			(tenant_id, activation_id, kind, logical_name, scope_type, cluster_id, namespace, version_id, revision, activated_by)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 1, $9)
			RETURNING activated_at`, actor.TenantID, activationID, kind, logicalName, scope.Type,
			nullableUUID(scope.ClusterID), nullableText(scope.Namespace), version.VersionID, actor.Subject).Scan(&result.ActivatedAt)
		if err != nil {
			return Activation{}, mapRegistryWriteError(err)
		}
		result = Activation{TenantID: actor.TenantID, ActivationID: activationID, Kind: kind,
			LogicalName: logicalName, Scope: scope, VersionID: version.VersionID, Revision: 1,
			ActivatedBy: actor.Subject, ActivatedAt: result.ActivatedAt}
	} else {
		if expectedRevision == 0 {
			return Activation{}, ErrScopeConflict
		}
		if current.Revision != expectedRevision {
			return Activation{}, ErrRevisionConflict
		}
		if current.VersionID == version.VersionID {
			return activationFromRow(actor.TenantID, kind, logicalName, current), nil
		}
		err = tx.QueryRow(ctx, `UPDATE platform.registry_activations SET version_id = $1, revision = revision + 1,
			activated_by = $2, activated_at = clock_timestamp() WHERE tenant_id = $3 AND activation_id = $4 AND revision = $5
			RETURNING revision, activated_at`, version.VersionID, actor.Subject, actor.TenantID, current.ActivationID, expectedRevision).
			Scan(&current.Revision, &current.ActivatedAt)
		if err != nil {
			return Activation{}, fmt.Errorf("update registry activation: %w", err)
		}
		current.VersionID = version.VersionID
		current.ActivatedBy = actor.Subject
		result = activationFromRow(actor.TenantID, kind, logicalName, current)
	}
	if err := appendActivationHistory(ctx, tx, result); err != nil {
		return Activation{}, err
	}
	if err := appendAudit(ctx, tx, actor, "config_registry.version_activated", "registry_activation", result.ActivationID,
		map[string]any{"kind": kind, "logical_name": logicalName, "scope_type": scope.Type,
			"cluster_id": nullableUUIDString(scope.ClusterID), "namespace": nullableText(scope.Namespace),
			"version_id": result.VersionID, "revision": result.Revision}); err != nil {
		return Activation{}, err
	}
	return result, nil
}

func (service *Service) Retire(ctx context.Context, tx pgx.Tx, actor auth.RequestContext, ref VersionRef, expectedDigest string) (PublishedVersion, error) {
	if ref.TenantID == uuid.Nil || ref.VersionID == uuid.Nil || !regexp.MustCompile(`^sha256:[0-9a-f]{64}$`).MatchString(expectedDigest) {
		return PublishedVersion{}, ErrInvalidInput
	}
	if ref.TenantID != actor.TenantID {
		return PublishedVersion{}, ErrNotFound
	}
	if err := authorizeAdmin(ctx, tx, actor); err != nil {
		return PublishedVersion{}, err
	}
	version, err := loadVersion(ctx, tx, actor.TenantID, ref.VersionID, true)
	if err != nil {
		return PublishedVersion{}, err
	}
	if version.Digest != expectedDigest {
		return PublishedVersion{}, ErrRevisionConflict
	}
	if version.RetiredAt != nil {
		return PublishedVersion{}, ErrStateConflict
	}
	var active bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM platform.registry_activations
		WHERE tenant_id = $1 AND version_id = $2)`, actor.TenantID, version.VersionID).Scan(&active); err != nil {
		return PublishedVersion{}, fmt.Errorf("check active registry references: %w", err)
	}
	if active {
		return PublishedVersion{}, ErrVersionActive
	}
	if err := tx.QueryRow(ctx, `UPDATE platform.registry_versions SET retired_at = clock_timestamp()
		WHERE tenant_id = $1 AND version_id = $2 AND digest = $3 AND retired_at IS NULL RETURNING retired_at`,
		actor.TenantID, version.VersionID, expectedDigest).Scan(&version.RetiredAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return PublishedVersion{}, ErrStateConflict
		}
		return PublishedVersion{}, fmt.Errorf("retire registry version: %w", err)
	}
	command, err := tx.Exec(ctx, `UPDATE platform.registry_drafts SET status = 'retired', revision = revision + 1,
		updated_by = $1, updated_at = clock_timestamp() WHERE tenant_id = $2 AND draft_id = $3 AND status = 'published'`,
		actor.Subject, actor.TenantID, version.DraftID)
	if err != nil {
		return PublishedVersion{}, fmt.Errorf("retire registry draft state: %w", err)
	}
	if command.RowsAffected() != 1 {
		return PublishedVersion{}, ErrStateConflict
	}
	if err := appendAudit(ctx, tx, actor, "config_registry.version_retired", "registry_version", version.VersionID,
		map[string]any{"kind": version.Kind, "logical_name": version.LogicalName, "version_number": version.VersionNumber,
			"digest": version.Digest}); err != nil {
		return PublishedVersion{}, err
	}
	return version, nil
}

func (service *Service) ListVersions(ctx context.Context, request auth.RequestContext, kind Kind) ([]PublishedVersion, error) {
	if !validKind(kind) || request.TenantID == uuid.Nil || request.Subject == "" || !auth.HasRole(auth.WithRequestContext(ctx, request), auth.PlatformAdmin) {
		return nil, ErrUnauthorized
	}
	var result []PublishedVersion
	err := persistence.WithTenantTx(ctx, service.pool, request.TenantID, func(tx pgx.Tx) error {
		if err := authorizeAdmin(ctx, tx, request); err != nil {
			return err
		}
		var err error
		result, err = listVersions(ctx, tx, request.TenantID, kind)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("list registry versions: %w", err)
	}
	return result, nil
}

func (service *Service) ResolveActive(ctx context.Context, tenantID uuid.UUID, kind Kind, logicalName string, scope Scope, at time.Time) (PublishedVersion, error) {
	if tenantID == uuid.Nil || !validKind(kind) || !validLogicalName(logicalName) || scope.Validate() != nil || at.IsZero() {
		return PublishedVersion{}, ErrInvalidInput
	}
	var result PublishedVersion
	err := persistence.WithTenantTx(ctx, service.pool, tenantID, func(tx pgx.Tx) error {
		var versionID uuid.UUID
		err := tx.QueryRow(ctx, `SELECT history.version_id
			FROM platform.registry_activation_history AS history
			JOIN platform.registry_versions AS version
			  ON version.tenant_id = history.tenant_id AND version.version_id = history.version_id
			 AND version.kind = history.kind AND version.logical_name = history.logical_name
			WHERE history.tenant_id = $1 AND history.kind = $2 AND history.logical_name = $3
			  AND history.activated_at <= $4
			  AND (version.retired_at IS NULL OR version.retired_at > $4)
			  AND (history.scope_type = 'tenant'
			       OR (history.scope_type = 'cluster' AND history.cluster_id = $5)
			       OR (history.scope_type = 'namespace' AND history.cluster_id = $5 AND history.namespace = $6))
			ORDER BY CASE history.scope_type WHEN 'namespace' THEN 3 WHEN 'cluster' THEN 2 ELSE 1 END DESC,
			         history.activated_at DESC, history.revision DESC
			LIMIT 1`, tenantID, kind, logicalName, at, nullableUUID(scope.ClusterID), nullableText(scope.Namespace)).Scan(&versionID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("resolve active registry version: %w", err)
		}
		result, err = loadVersion(ctx, tx, tenantID, versionID, false)
		return err
	})
	if err != nil {
		return PublishedVersion{}, err
	}
	return result, nil
}

func authorizeAdmin(ctx context.Context, tx pgx.Tx, actor auth.RequestContext) error {
	if actor.TenantID == uuid.Nil || actor.Subject == "" || !auth.HasRole(auth.WithRequestContext(ctx, actor), auth.PlatformAdmin) {
		return ErrUnauthorized
	}
	var active bool
	err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM platform.tenants WHERE tenant_id = $1 AND status = 'active')
		AND EXISTS (SELECT 1 FROM platform.role_bindings WHERE tenant_id = $1 AND subject = $2
			AND role_name = 'platform_admin' AND status = 'active')`, actor.TenantID, actor.Subject).Scan(&active)
	if err != nil {
		return fmt.Errorf("verify registry administrator: %w", err)
	}
	if !active {
		return ErrUnauthorized
	}
	return nil
}

func appendAudit(ctx context.Context, tx pgx.Tx, actor auth.RequestContext, event, entity string, id uuid.UUID, payload map[string]any) error {
	if _, err := audit.Append(ctx, tx, audit.Entry{TenantID: actor.TenantID, RecordID: uuid.Must(uuid.NewV7()),
		EventType: event, EntityKind: entity, EntityID: id, Subject: actor.Subject, Payload: payload}); err != nil {
		return fmt.Errorf("append configuration registry audit record: %w", err)
	}
	return nil
}

func appendActivationHistory(ctx context.Context, tx pgx.Tx, activation Activation) error {
	_, err := tx.Exec(ctx, `INSERT INTO platform.registry_activation_history
		(tenant_id, activation_id, revision, kind, logical_name, scope_type, cluster_id, namespace, version_id, activated_by, activated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`, activation.TenantID, activation.ActivationID,
		activation.Revision, activation.Kind, activation.LogicalName, activation.Scope.Type,
		nullableUUID(activation.Scope.ClusterID), nullableText(activation.Scope.Namespace), activation.VersionID,
		activation.ActivatedBy, activation.ActivatedAt)
	if err != nil {
		return fmt.Errorf("append registry activation history: %w", err)
	}
	return nil
}

func activationFromRow(tenantID uuid.UUID, kind Kind, logicalName string, row activationRow) Activation {
	return Activation{TenantID: tenantID, ActivationID: row.ActivationID, Kind: kind, LogicalName: logicalName,
		Scope: Scope{Type: row.Type, ClusterID: row.ClusterID, Namespace: row.Namespace}, VersionID: row.VersionID,
		Revision: row.Revision, ActivatedBy: row.ActivatedBy, ActivatedAt: row.ActivatedAt}
}

func lockIdentity(ctx context.Context, tx pgx.Tx, identity string) error {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, identity); err != nil {
		return fmt.Errorf("lock configuration registry identity: %w", err)
	}
	return nil
}

func jsonObject(content []byte) bool {
	canonical, err := bundle.CanonicalizeJSON(content)
	if err != nil {
		return false
	}
	var object map[string]json.RawMessage
	return json.Unmarshal(canonical, &object) == nil && object != nil
}

func contentNameMatches(content []byte, name string) bool {
	var value struct {
		Name string `json:"name"`
	}
	return json.Unmarshal(content, &value) == nil && value.Name == name
}

func nullableUUIDString(value uuid.UUID) any {
	if value == uuid.Nil {
		return nil
	}
	return value.String()
}

func mapRegistryWriteError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505", "23503", "23514":
			return fmt.Errorf("%w: %s", ErrScopeConflict, pgErr.ConstraintName)
		}
	}
	return fmt.Errorf("write configuration registry: %w", err)
}
