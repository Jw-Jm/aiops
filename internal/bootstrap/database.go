package bootstrap

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"

	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/pbkdf2"
)

type DatabaseLogin struct {
	Name     string `json:"name"`
	Password string `json:"password"`
}
type DatabaseLogins struct {
	SchemaVersion int           `json:"schemaVersion"`
	Migration     DatabaseLogin `json:"migration"`
	API           DatabaseLogin `json:"api"`
	Worker        DatabaseLogin `json:"worker"`
}

func (c DatabaseLogins) Validate() error {
	if c.SchemaVersion != 1 {
		return errors.New("database identity schema version required")
	}
	names := map[string]bool{}
	reserved := map[string]bool{"postgres": true, "ops_keycloak": true, "schema_owner": true, "migration_role": true, "api_runtime_role": true, "worker_runtime_role": true, "audit_append_owner": true, "ops_readonly_role": true}
	for _, login := range []DatabaseLogin{c.Migration, c.API, c.Worker} {
		if !regexp.MustCompile(`^[a-z][a-z0-9_]{1,62}$`).MatchString(login.Name) || reserved[login.Name] || names[login.Name] || !regexp.MustCompile(`^[!-~]{24,256}$`).MatchString(login.Password) {
			return errors.New("three distinct explicit database LOGIN identities and strong private credentials required")
		}
		names[login.Name] = true
	}
	return nil
}

// Only the controlled bootstrap process may create the three fixed duties.
// Passwords are converted to native SCRAM verifiers before CREATE ROLE, keeping
// plaintext out of PostgreSQL statement/error logging.
func ProvisionDatabaseLogins(ctx context.Context, conn *pgx.Conn, c DatabaseLogins) error {
	if err := c.Validate(); err != nil {
		return err
	}
	if conn == nil {
		return ErrIdentity
	}
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var privileged bool
	if err = tx.QueryRow(ctx, `SELECT rolsuper FROM pg_roles WHERE rolname=session_user`).Scan(&privileged); err != nil || !privileged {
		return ErrIdentity
	}
	if _, err = tx.Exec(ctx, `RESET ROLE; LOCK TABLE platform.tenants IN ACCESS EXCLUSIVE MODE`); err != nil {
		return errors.New("version 1 database bootstrap required")
	}
	var version int64
	var tenants int
	if err = tx.QueryRow(ctx, `SELECT max(version_id) FILTER(WHERE is_applied) FROM public.goose_db_version`).Scan(&version); err != nil || version != 1 {
		return errors.New("database LOGIN bootstrap requires exactly migration version 1")
	}
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM platform.tenants`).Scan(&tenants); err != nil || tenants != 0 {
		return ErrInitialized
	}
	var database string
	if err = tx.QueryRow(ctx, `SELECT current_database()`).Scan(&database); err != nil {
		return err
	}
	for _, duty := range []struct {
		Login DatabaseLogin
		Role  string
	}{{c.Migration, "migration_role"}, {c.API, "api_runtime_role"}, {c.Worker, "worker_runtime_role"}} {
		var exists bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=$1)`, duty.Login.Name).Scan(&exists); err != nil {
			return err
		}
		if exists {
			return errors.New("database LOGIN bootstrap refuses existing identities")
		}
		verifier, err := scramVerifier(duty.Login.Password)
		if err != nil {
			return errors.New("database credential generation failed")
		}
		statement := fmt.Sprintf("CREATE ROLE %s LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION NOBYPASSRLS PASSWORD '%s'", pgx.Identifier{duty.Login.Name}.Sanitize(), verifier)
		if _, err = tx.Exec(ctx, statement); err != nil {
			return errors.New("database LOGIN creation failed")
		}
		if _, err = tx.Exec(ctx, "GRANT "+pgx.Identifier{duty.Role}.Sanitize()+" TO "+pgx.Identifier{duty.Login.Name}.Sanitize()); err != nil {
			return errors.New("database duty grant failed")
		}
		if _, err = tx.Exec(ctx, "GRANT CONNECT ON DATABASE "+pgx.Identifier{database}.Sanitize()+" TO "+pgx.Identifier{duty.Login.Name}.Sanitize()); err != nil {
			return errors.New("database connection grant failed")
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return errors.New("database LOGIN bootstrap transaction failed")
	}
	return nil
}
func scramVerifier(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	salted := pbkdf2.Key([]byte(password), salt, 4096, 32, sha256.New)
	mac := func(label string) []byte {
		h := hmac.New(sha256.New, salted)
		h.Write([]byte(label))
		return h.Sum(nil)
	}
	stored := sha256.Sum256(mac("Client Key"))
	server := mac("Server Key")
	encode := base64.StdEncoding.EncodeToString
	return "SCRAM-SHA-256$4096:" + encode(salt) + "$" + encode(stored[:]) + ":" + encode(server), nil
}
