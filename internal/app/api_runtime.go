package app

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"ops-platform/internal/auth"
	"ops-platform/internal/configregistry"
	"ops-platform/internal/httpapi"
	"ops-platform/internal/observability"
)

// OpenRuntimePool selects the process's least-privilege role and refuses an
// owner/superuser login even when that login temporarily SET ROLEs down.
func OpenRuntimePool(ctx context.Context, dsn, role string) (*pgxpool.Pool, error) {
	if role != "api_runtime_role" && role != "worker_runtime_role" {
		return nil, errors.New("unsupported runtime database role")
	}
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, errors.New("runtime database configuration is invalid")
	}
	config.ConnConfig.RuntimeParams["role"] = role
	otherRole := "worker_runtime_role"
	if role == otherRole {
		otherRole = "api_runtime_role"
	}
	config.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		var current string
		var privileged bool
		err := conn.QueryRow(ctx, `SELECT current_user,
			(SELECT rolsuper OR rolbypassrls OR rolcreaterole OR rolcreatedb OR rolreplication FROM pg_roles WHERE rolname = session_user)
			OR pg_has_role(session_user, 'schema_owner', 'MEMBER')
			OR pg_has_role(session_user, 'audit_append_owner', 'MEMBER')
			OR pg_has_role(session_user, 'migration_role', 'MEMBER')
			OR pg_has_role(session_user, $1, 'MEMBER')`, otherRole).Scan(&current, &privileged)
		if err != nil || current != role || privileged {
			return errors.New("runtime database login exceeds process privileges")
		}
		return nil
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, errors.New("runtime database pool could not be opened")
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, errors.New("runtime database is unavailable or privileges are invalid")
	}
	return pool, nil
}

func RegistryTrustFromFile(path string) (configregistry.Ed25519TrustStore, error) {
	trust := configregistry.Ed25519TrustStore{Keys: map[string]ed25519.PublicKey{}}
	// An absent trust file leaves publication disabled; it never grants trust to
	// a key carried in a draft, request or bundle.
	if path == "" {
		return trust, nil
	}
	encoded, err := os.ReadFile(path)
	if err != nil || len(encoded) > 64<<10 {
		return trust, errors.New("registry trust file is unavailable or invalid")
	}
	var keys map[string]string
	if json.Unmarshal(encoded, &keys) != nil {
		return trust, errors.New("registry trust file must contain a key-id to public-key map")
	}
	for id, encoded := range keys {
		key, err := base64.StdEncoding.DecodeString(encoded)
		if id == "" || len(id) > 200 || err != nil || len(key) != ed25519.PublicKeySize {
			return trust, errors.New("registry trust file contains an invalid public key")
		}
		trust.Keys[id] = ed25519.PublicKey(key)
	}
	return trust, nil
}

func (application *APIApp) Serve(ctx context.Context, listener net.Listener, runtime *observability.Runtime) error {
	if listener == nil || runtime == nil {
		return errors.New("API listener and observability are required")
	}
	if err := application.config.validateRuntimeProfile(false); err != nil {
		return err
	}
	pool, err := OpenRuntimePool(ctx, application.config.DatabaseURL, "api_runtime_role")
	if err != nil {
		return err
	}
	defer pool.Close()
	bindings, err := auth.NewPostgreSQLRoleBindingSource(pool)
	if err != nil {
		return err
	}
	authenticator, err := auth.NewOIDCAuthenticator(ctx, application.config.OIDCIssuerURL, "ops-api", bindings)
	if err != nil {
		return errors.New("API OIDC discovery failed")
	}
	trust, err := RegistryTrustFromFile(os.Getenv("PLATFORM_REGISTRY_TRUST_FILE"))
	if err != nil {
		return err
	}
	sp04, err := NewSP04API(ctx, pool)
	if err != nil {
		return err
	}
	stopSP06, err := StartSP06API(ctx, pool, sp04, trust)
	if err != nil {
		return err
	}
	defer stopSP06()
	stopSP07, err := StartSP07API(ctx, pool, sp04, trust)
	if err != nil {
		return err
	}
	defer stopSP07()
	handler, err := httpapi.NewFoundationHandlerWithSP04(pool, authenticator, trust, runtime, sp04)
	if err != nil {
		return err
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: time.Minute}
	errorsCh := make(chan error, 1)
	go func() { errorsCh <- server.Serve(listener) }()
	select {
	case <-ctx.Done():
		server.SetKeepAlivesEnabled(false)
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdown); err != nil {
			_ = server.Close()
			return err
		}
		return nil
	case err := <-errorsCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return errors.New("API listener failed")
	}
}
