package source

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/audit"
	"ops-platform/internal/auth"
	"ops-platform/internal/persistence"
)

type Service struct {
	pool     persistence.TxBeginner
	verifier CredentialVerifier
}

func NewService(pool persistence.TxBeginner, verifier CredentialVerifier) (*Service, error) {
	if pool == nil {
		return nil, errors.New("source database pool is required")
	}
	return &Service{pool: pool, verifier: verifier}, nil
}

func (service *Service) Register(ctx context.Context, tx pgx.Tx, actor auth.RequestContext, command RegisterCommand) (SourceRegistration, error) {
	if err := command.Validate(); err != nil {
		return SourceRegistration{}, err
	}
	if err := authorizeAdmin(ctx, tx, actor); err != nil {
		return SourceRegistration{}, err
	}
	if command.ClusterID != nil {
		var active bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM platform.cluster_registrations
			WHERE tenant_id = $1 AND cluster_id = $2 AND status = 'active')`, actor.TenantID, *command.ClusterID).Scan(&active); err != nil {
			return SourceRegistration{}, fmt.Errorf("validate source cluster scope: %w", err)
		}
		if !active {
			return SourceRegistration{}, fmt.Errorf("%w: source cluster is not active in this tenant", ErrInvalidInput)
		}
	}
	var clusterID any
	if command.ClusterID != nil {
		clusterID = *command.ClusterID
	}
	sourceID := uuid.Must(uuid.NewV7())
	var createdID uuid.UUID
	err := tx.QueryRow(ctx, `
INSERT INTO platform.source_registrations (tenant_id, source_id, source_type, instance_key, cluster_id, auth_ref, allowed_schemas)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (tenant_id, source_type, instance_key) DO NOTHING
RETURNING source_id`, actor.TenantID, sourceID, command.SourceType, command.InstanceKey, clusterID, command.AuthRef, command.AllowedSchemas).Scan(&createdID)
	if errors.Is(err, pgx.ErrNoRows) {
		var existing SourceRegistration
		var storedClusterID *uuid.UUID
		err = tx.QueryRow(ctx, `SELECT source_id, cluster_id, auth_ref, credential_revision, status, revision, created_at, updated_at, allowed_schemas
			FROM platform.source_registrations WHERE tenant_id = $1 AND source_type = $2 AND instance_key = $3`,
			actor.TenantID, command.SourceType, command.InstanceKey).
			Scan(&existing.SourceID, &storedClusterID, &existing.AuthRef, &existing.CredentialRevision,
				&existing.Status, &existing.Revision, &existing.CreatedAt, &existing.UpdatedAt, &existing.AllowedSchemas)
		if err != nil {
			return SourceRegistration{}, fmt.Errorf("load duplicate source registration: %w", err)
		}
		if storedClusterID != nil {
			existing.ClusterID = *storedClusterID
		}
		if existing.AuthRef != command.AuthRef || !sameClusterID(existing.ClusterID, command.ClusterID) || !slices.Equal(existing.AllowedSchemas, command.AllowedSchemas) {
			return SourceRegistration{}, ErrIdentityConflict
		}
		return loadSource(ctx, tx, actor.TenantID, existing.SourceID, false)
	}
	if err != nil {
		return SourceRegistration{}, fmt.Errorf("register source: %w", err)
	}
	created, err := loadSource(ctx, tx, actor.TenantID, createdID, false)
	if err != nil {
		return SourceRegistration{}, err
	}
	if err := appendSourceRevision(ctx, tx, created, actor.Subject); err != nil {
		return SourceRegistration{}, err
	}
	if _, err := audit.Append(ctx, tx, audit.Entry{
		TenantID: actor.TenantID, RecordID: uuid.Must(uuid.NewV7()), EventType: "source_registration.created",
		EntityKind: "source_registration", EntityID: created.SourceID, Subject: actor.Subject,
		Payload: map[string]any{
			"source_type": created.SourceType, "instance_key": created.InstanceKey,
			"cluster_id": nullableUUIDString(created.ClusterID), "auth_version": created.CredentialRevision, "revision": created.Revision, "allowed_schemas": created.AllowedSchemas,
		},
	}); err != nil {
		return SourceRegistration{}, fmt.Errorf("audit source registration: %w", err)
	}
	return created, nil
}

func (service *Service) UpdateRegistration(ctx context.Context, tx pgx.Tx, actor auth.RequestContext, sourceID uuid.UUID, command SourceUpdateCommand) (SourceRegistration, error) {
	if sourceID == uuid.Nil || command.ExpectedRevision < 1 || (!command.ClusterIDSet && command.Status == "" && command.AllowedSchemas == nil) ||
		(command.AllowedSchemas != nil && !validAllowedSchemas(command.AllowedSchemas)) ||
		(command.Status != "" && command.Status != "active" && command.Status != "disabled") ||
		(command.ClusterIDSet && command.ClusterID != nil && *command.ClusterID == uuid.Nil) {
		return SourceRegistration{}, ErrInvalidInput
	}
	if err := authorizeAdmin(ctx, tx, actor); err != nil {
		return SourceRegistration{}, err
	}
	previous, err := loadSource(ctx, tx, actor.TenantID, sourceID, true)
	if err != nil {
		return SourceRegistration{}, err
	}
	if previous.Revision != command.ExpectedRevision {
		return SourceRegistration{}, ErrRevisionConflict
	}
	newClusterID := previous.ClusterID
	if command.ClusterIDSet {
		newClusterID = uuid.Nil
		if command.ClusterID != nil {
			newClusterID = *command.ClusterID
			var active bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM platform.cluster_registrations
				WHERE tenant_id = $1 AND cluster_id = $2 AND status = 'active')`, actor.TenantID, newClusterID).Scan(&active); err != nil {
				return SourceRegistration{}, fmt.Errorf("validate updated source cluster scope: %w", err)
			}
			if !active {
				return SourceRegistration{}, fmt.Errorf("%w: updated source cluster is not active in this tenant", ErrInvalidInput)
			}
		}
	}
	newStatus := previous.Status
	if command.Status != "" {
		newStatus = command.Status
	}
	newSchemas := previous.AllowedSchemas
	if command.AllowedSchemas != nil {
		newSchemas = command.AllowedSchemas
	}
	if newClusterID == previous.ClusterID && newStatus == previous.Status && slices.Equal(newSchemas, previous.AllowedSchemas) {
		return previous, nil
	}
	var clusterValue any
	if newClusterID != uuid.Nil {
		clusterValue = newClusterID
	}
	updated, err := tx.Query(ctx, `UPDATE platform.source_registrations
		SET cluster_id = $1, status = $2, allowed_schemas = $6, revision = revision + 1, updated_at = clock_timestamp()
		WHERE tenant_id = $3 AND source_id = $4 AND revision = $5
		RETURNING revision, updated_at`, clusterValue, newStatus, actor.TenantID, sourceID, command.ExpectedRevision, newSchemas)
	if err != nil {
		return SourceRegistration{}, fmt.Errorf("update source registration: %w", err)
	}
	if !updated.Next() {
		updated.Close()
		return SourceRegistration{}, ErrRevisionConflict
	}
	if err := updated.Scan(&previous.Revision, &previous.UpdatedAt); err != nil {
		updated.Close()
		return SourceRegistration{}, fmt.Errorf("read updated source registration revision: %w", err)
	}
	updated.Close()
	result, err := loadSource(ctx, tx, actor.TenantID, sourceID, false)
	if err != nil {
		return SourceRegistration{}, err
	}
	if err := appendSourceRevision(ctx, tx, result, actor.Subject); err != nil {
		return SourceRegistration{}, err
	}
	if _, err := audit.Append(ctx, tx, audit.Entry{
		TenantID: actor.TenantID, RecordID: uuid.Must(uuid.NewV7()), EventType: "source_registration.updated",
		EntityKind: "source_registration", EntityID: result.SourceID, Subject: actor.Subject,
		Payload: map[string]any{
			"before": map[string]any{"cluster_id": nullableUUIDString(previous.ClusterID), "status": previous.Status, "revision": command.ExpectedRevision, "allowed_schemas": previous.AllowedSchemas},
			"after":  map[string]any{"cluster_id": nullableUUIDString(result.ClusterID), "status": result.Status, "revision": result.Revision, "allowed_schemas": result.AllowedSchemas},
		},
	}); err != nil {
		return SourceRegistration{}, fmt.Errorf("audit source registration update: %w", err)
	}
	return result, nil
}

func (service *Service) RotateCredential(ctx context.Context, tx pgx.Tx, actor auth.RequestContext, sourceID uuid.UUID, command CredentialRotationCommand) (SourceRegistration, error) {
	if sourceID == uuid.Nil || command.ExpectedRevision < 1 || !validAuthRef(command.AuthRef) {
		return SourceRegistration{}, ErrInvalidInput
	}
	if err := authorizeAdmin(ctx, tx, actor); err != nil {
		return SourceRegistration{}, err
	}
	previous, err := loadSource(ctx, tx, actor.TenantID, sourceID, true)
	if err != nil {
		return SourceRegistration{}, err
	}
	if previous.Revision != command.ExpectedRevision {
		return SourceRegistration{}, ErrRevisionConflict
	}
	if previous.AuthRef == command.AuthRef {
		return SourceRegistration{}, ErrInvalidInput
	}
	var resultID uuid.UUID
	err = tx.QueryRow(ctx, `UPDATE platform.source_registrations
		SET auth_ref = $1, credential_revision = credential_revision + 1,
		    status = CASE WHEN status = 'disabled' THEN 'disabled' ELSE 'active' END,
		    revision = revision + 1, updated_at = clock_timestamp()
		WHERE tenant_id = $2 AND source_id = $3 AND revision = $4
		RETURNING source_id`, command.AuthRef, actor.TenantID, sourceID, command.ExpectedRevision).Scan(&resultID)
	if errors.Is(err, pgx.ErrNoRows) {
		return SourceRegistration{}, ErrRevisionConflict
	}
	if err != nil {
		return SourceRegistration{}, fmt.Errorf("rotate source credential reference: %w", err)
	}
	result, err := loadSource(ctx, tx, actor.TenantID, resultID, false)
	if err != nil {
		return SourceRegistration{}, err
	}
	if err := appendSourceRevision(ctx, tx, result, actor.Subject); err != nil {
		return SourceRegistration{}, err
	}
	if _, err := audit.Append(ctx, tx, audit.Entry{
		TenantID: actor.TenantID, RecordID: uuid.Must(uuid.NewV7()), EventType: "source_registration.credential_rotated",
		EntityKind: "source_registration", EntityID: result.SourceID, Subject: actor.Subject,
		Payload: map[string]any{
			"auth_version":          result.CredentialRevision,
			"previous_auth_version": previous.CredentialRevision,
			"revision":              result.Revision, "previous_revision": previous.Revision,
		},
	}); err != nil {
		return SourceRegistration{}, fmt.Errorf("audit source credential rotation: %w", err)
	}
	return result, nil
}

// RollbackRegistration creates a new source revision containing the selected
// historical auth reference, status, and tenant-local cluster scope. The
// credential generation remains monotonic so identities issued before the
// rollback cannot become valid again.
func (service *Service) RollbackRegistration(ctx context.Context, tx pgx.Tx, actor auth.RequestContext, sourceID uuid.UUID, command SourceRegistrationRollbackCommand) (SourceRegistration, error) {
	if sourceID == uuid.Nil || command.ExpectedRevision < 1 || command.TargetRevision < 1 || command.TargetRevision >= command.ExpectedRevision {
		return SourceRegistration{}, ErrInvalidInput
	}
	if err := authorizeAdmin(ctx, tx, actor); err != nil {
		return SourceRegistration{}, err
	}
	current, err := loadSource(ctx, tx, actor.TenantID, sourceID, true)
	if err != nil {
		return SourceRegistration{}, err
	}
	if current.Revision != command.ExpectedRevision {
		return SourceRegistration{}, ErrRevisionConflict
	}
	var authRef, status string
	var targetCredentialRevision int64
	var scopeJSON []byte
	err = tx.QueryRow(ctx, `SELECT auth_ref, credential_revision, status, scope
		FROM platform.source_registration_revisions
		WHERE tenant_id = $1 AND source_id = $2 AND revision = $3`,
		actor.TenantID, sourceID, command.TargetRevision).
		Scan(&authRef, &targetCredentialRevision, &status, &scopeJSON)
	if errors.Is(err, pgx.ErrNoRows) {
		return SourceRegistration{}, ErrResourceNotFound
	}
	if err != nil {
		return SourceRegistration{}, fmt.Errorf("load source rollback revision: %w", err)
	}
	var scope struct {
		SourceType     string   `json:"source_type"`
		InstanceKey    string   `json:"instance_key"`
		ClusterID      *string  `json:"cluster_id"`
		AllowedSchemas []string `json:"allowed_schemas"`
	}
	if err := json.Unmarshal(scopeJSON, &scope); err != nil || scope.SourceType != current.SourceType || scope.InstanceKey != current.InstanceKey {
		return SourceRegistration{}, fmt.Errorf("%w: source rollback revision scope is invalid", ErrInvalidInput)
	}
	if scope.AllowedSchemas == nil {
		scope.AllowedSchemas = []string{}
	}
	if len(scope.AllowedSchemas) > 0 && !validAllowedSchemas(scope.AllowedSchemas) {
		return SourceRegistration{}, ErrInvalidInput
	}
	clusterID := uuid.Nil
	if scope.ClusterID != nil {
		clusterID, err = uuid.Parse(*scope.ClusterID)
		if err != nil || clusterID == uuid.Nil {
			return SourceRegistration{}, fmt.Errorf("%w: source rollback revision cluster is invalid", ErrInvalidInput)
		}
	}
	if status == "active" && clusterID != uuid.Nil {
		var active bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM platform.cluster_registrations
			WHERE tenant_id = $1 AND cluster_id = $2 AND status = 'active')`, actor.TenantID, clusterID).Scan(&active); err != nil {
			return SourceRegistration{}, fmt.Errorf("validate rollback source cluster scope: %w", err)
		}
		if !active {
			return SourceRegistration{}, fmt.Errorf("%w: rollback source cluster is not active in this tenant", ErrInvalidInput)
		}
	}
	credentialRevision := current.CredentialRevision
	if authRef != current.AuthRef {
		credentialRevision = max(credentialRevision, targetCredentialRevision) + 1
	}
	var clusterValue any
	if clusterID != uuid.Nil {
		clusterValue = clusterID
	}
	tag, err := tx.Exec(ctx, `UPDATE platform.source_registrations
		SET cluster_id = $1, auth_ref = $2, credential_revision = $3, status = $4, allowed_schemas = $8,
		    revision = revision + 1, updated_at = clock_timestamp()
		WHERE tenant_id = $5 AND source_id = $6 AND revision = $7`,
		clusterValue, authRef, credentialRevision, status, actor.TenantID, sourceID, command.ExpectedRevision, scope.AllowedSchemas)
	if err != nil {
		return SourceRegistration{}, fmt.Errorf("rollback source registration: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return SourceRegistration{}, ErrRevisionConflict
	}
	result, err := loadSource(ctx, tx, actor.TenantID, sourceID, false)
	if err != nil {
		return SourceRegistration{}, err
	}
	if err := appendSourceRevision(ctx, tx, result, actor.Subject); err != nil {
		return SourceRegistration{}, err
	}
	if _, err := audit.Append(ctx, tx, audit.Entry{
		TenantID: actor.TenantID, RecordID: uuid.Must(uuid.NewV7()), EventType: "source_registration.rolled_back",
		EntityKind: "source_registration", EntityID: result.SourceID, Subject: actor.Subject,
		Payload: map[string]any{
			"restored_from_revision": command.TargetRevision,
			"before":                 map[string]any{"cluster_id": nullableUUIDString(current.ClusterID), "status": current.Status, "revision": current.Revision, "allowed_schemas": current.AllowedSchemas},
			"after":                  map[string]any{"cluster_id": nullableUUIDString(result.ClusterID), "status": result.Status, "revision": result.Revision, "allowed_schemas": result.AllowedSchemas},
			"auth_ref_changed":       current.AuthRef != result.AuthRef, "auth_version": result.CredentialRevision,
		},
	}); err != nil {
		return SourceRegistration{}, fmt.Errorf("audit source registration rollback: %w", err)
	}
	return result, nil
}

func (service *Service) ListSources(ctx context.Context, request auth.RequestContext) ([]SourceRegistration, error) {
	if err := requireAdminContext(request); err != nil {
		return nil, err
	}
	var result []SourceRegistration
	err := persistence.WithTenantTx(ctx, service.pool, request.TenantID, func(tx pgx.Tx) error {
		if err := authorizeAdmin(ctx, tx, request); err != nil {
			return err
		}
		var err error
		result, err = listSources(ctx, tx, request.TenantID)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("list current tenant sources: %w", err)
	}
	return result, nil
}

func (service *Service) AuthenticateEnvelope(ctx context.Context, identity SourceIdentity, envelope FindingEnvelope) (BoundSourceContext, error) {
	if identity.TenantID == uuid.Nil || identity.SourceID == uuid.Nil || identity.CredentialRevision < 1 {
		return BoundSourceContext{}, ErrUnauthorized
	}
	var registration SourceRegistration
	err := persistence.WithTenantTx(ctx, service.pool, identity.TenantID, func(tx pgx.Tx) error {
		var err error
		registration, err = loadSource(ctx, tx, identity.TenantID, identity.SourceID, false)
		if err != nil {
			return err
		}
		if registration.ClusterID != uuid.Nil {
			var active bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM platform.cluster_registrations
				WHERE tenant_id = $1 AND cluster_id = $2 AND status = 'active')`, registration.TenantID, registration.ClusterID).Scan(&active); err != nil {
				return err
			}
			if !active {
				return ErrUnauthorized
			}
		}
		return nil
	})
	if err != nil {
		return BoundSourceContext{}, ErrUnauthorized
	}
	return bindEnvelope(ctx, registration, identity, envelope, service.verifier)
}

func bindEnvelope(ctx context.Context, registration SourceRegistration, identity SourceIdentity, envelope FindingEnvelope, verifier CredentialVerifier) (BoundSourceContext, error) {
	if registration.TenantID == uuid.Nil || registration.SourceID == uuid.Nil || registration.ClusterID == uuid.Nil || registration.ClusterUID == "" ||
		registration.Status != "active" || registration.TenantID != identity.TenantID || registration.SourceID != identity.SourceID ||
		registration.CredentialRevision != identity.CredentialRevision || envelope.TenantID != registration.TenantID ||
		!slices.Contains(registration.AllowedSchemas, envelope.SchemaVersion) || envelope.SchemaVersion == "" ||
		envelope.ClusterUID != registration.ClusterUID || envelope.Source.System != registration.SourceType ||
		envelope.Source.Instance != registration.InstanceKey || verifier == nil {
		return BoundSourceContext{}, ErrUnauthorized
	}
	if err := verifier.Verify(ctx, registration.AuthRef, identity, envelope); err != nil {
		return BoundSourceContext{}, ErrUnauthorized
	}
	return BoundSourceContext{
		TenantID: registration.TenantID, SourceID: registration.SourceID, ClusterID: registration.ClusterID,
		ClusterUID: registration.ClusterUID, CredentialRevision: registration.CredentialRevision,
	}, nil
}

func authorizeAdmin(ctx context.Context, tx pgx.Tx, actor auth.RequestContext) error {
	if err := requireAdminContext(actor); err != nil {
		return err
	}
	var active bool
	err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM platform.tenants WHERE tenant_id = $1 AND status = 'active')
		AND EXISTS (SELECT 1 FROM platform.role_bindings WHERE tenant_id = $1 AND subject = $2 AND role_name = 'platform_admin' AND status = 'active')`,
		actor.TenantID, actor.Subject).Scan(&active)
	if err != nil {
		return fmt.Errorf("verify source administrator: %w", err)
	}
	if !active {
		return ErrUnauthorized
	}
	return nil
}

func requireAdminContext(actor auth.RequestContext) error {
	if actor.TenantID == uuid.Nil || actor.Subject == "" ||
		!auth.HasRole(auth.WithRequestContext(context.Background(), actor), auth.PlatformAdmin) {
		return ErrUnauthorized
	}
	return nil
}

func appendClusterRevision(ctx context.Context, tx pgx.Tx, value ClusterRegistration, actor string) error {
	_, err := tx.Exec(ctx, `INSERT INTO platform.cluster_registration_revisions
		(tenant_id, cluster_id, revision, cluster_uid, display_name, status, actor_subject, api_endpoint_ref, distribution, actual_versions, capabilities)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		value.TenantID, value.ClusterID, value.Revision, value.ClusterUID, value.DisplayName, value.Status, actor, value.APIEndpointRef, value.Distribution, value.ActualVersions, value.Capabilities)
	if err != nil {
		return fmt.Errorf("append cluster registration revision: %w", err)
	}
	return nil
}

func appendSourceRevision(ctx context.Context, tx pgx.Tx, value SourceRegistration, actor string) error {
	scope, err := json.Marshal(map[string]any{
		"source_type": value.SourceType, "instance_key": value.InstanceKey,
		"cluster_id": nullableUUIDString(value.ClusterID), "cluster_uid": value.ClusterUID,
		"allowed_schemas": value.AllowedSchemas,
	})
	if err != nil {
		return fmt.Errorf("encode source registration scope: %w", err)
	}
	_, err = tx.Exec(ctx, `INSERT INTO platform.source_registration_revisions
		(tenant_id, source_id, revision, auth_ref, credential_revision, status, scope, actor_subject)
		VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb, $8)`,
		value.TenantID, value.SourceID, value.Revision, value.AuthRef, value.CredentialRevision, value.Status, scope, actor)
	if err != nil {
		return fmt.Errorf("append source registration revision: %w", err)
	}
	return nil
}

func sameClusterID(existing uuid.UUID, incoming *uuid.UUID) bool {
	if incoming == nil {
		return existing == uuid.Nil
	}
	return existing == *incoming
}

func nullableUUIDString(value uuid.UUID) any {
	if value == uuid.Nil {
		return nil
	}
	return value.String()
}
