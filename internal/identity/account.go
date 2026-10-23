package identity

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

const (
	maxLoginNameCharacters   = 128
	maxDisplayNameCharacters = 256
	maxSubjectCharacters     = 256
)

var loginNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

var (
	ErrLoginNameInvalid = errors.New("login name is invalid")
	ErrDisplayName      = errors.New("display name is invalid")
	ErrSubjectInvalid   = errors.New("subject is invalid")
	ErrTenantRequired   = errors.New("tenant is required")
)

// NormalizeLoginName makes the stored and compared login form explicit:
// surrounding ASCII/Unicode whitespace is removed and ASCII letters are
// lower-cased. It performs no Unicode folding, no separator rewrite, and no
// truncation. Input outside the reviewed grammar is rejected by validation.
func NormalizeLoginName(input string) string {
	return strings.ToLower(strings.TrimSpace(input))
}

// ValidateLoginName accepts the normalized form only. This keeps the database
// unique key identical to the lookup key and avoids case or space collisions.
func ValidateLoginName(input string) error {
	if runes := utf8.RuneCountInString(input); runes < 1 || runes > maxLoginNameCharacters {
		return ErrLoginNameInvalid
	}
	if !loginNamePattern.MatchString(input) {
		return ErrLoginNameInvalid
	}
	return nil
}

func ValidateDisplayName(input string) error {
	if runes := utf8.RuneCountInString(input); runes < 1 || runes > maxDisplayNameCharacters {
		return ErrDisplayName
	}
	if strings.TrimSpace(input) == "" {
		return ErrDisplayName
	}
	return nil
}

func validateSubject(input string) error {
	if runes := utf8.RuneCountInString(input); runes < 1 || runes > maxSubjectCharacters {
		return ErrSubjectInvalid
	}
	return nil
}

type AccountStatus string

const (
	AccountStatusActive    AccountStatus = "active"
	AccountStatusDisabled  AccountStatus = "disabled"
	AccountStatusArchived  AccountStatus = "archived"
	PasswordChangeRequired               = "change_required"
	PasswordActive                       = "active"
)

type Role string

const (
	RoleOperator      Role = "operator"
	RolePlatformAdmin Role = "platform_admin"
)

type Scope struct {
	ClusterUIDs []string `json:"clusterUids,omitempty"`
	Namespaces  []string `json:"namespaces,omitempty"`
}

type FirstAdministratorInput struct {
	// TenantSlug and TenantID are mutually exclusive. Neither is accepted from
	// an HTTP client; this type is the bootstrap command's input contract.
	TenantSlug  string
	TenantID    uuid.UUID
	LoginName   string
	DisplayName string
	Password    string
}

// MarshalJSON removes the password if this internal request is accidentally
// encoded. Callers must pass the plaintext only through memory to the hasher.
func (input FirstAdministratorInput) MarshalJSON() ([]byte, error) {
	type safeFirstAdministratorInput struct {
		TenantSlug      string    `json:"tenantSlug,omitempty"`
		TenantID        uuid.UUID `json:"tenantId,omitempty"`
		LoginName       string    `json:"loginName"`
		DisplayName     string    `json:"displayName"`
		PasswordPresent bool      `json:"passwordPresent"`
	}
	return json.Marshal(safeFirstAdministratorInput{
		TenantSlug: input.TenantSlug, TenantID: input.TenantID,
		LoginName: input.LoginName, DisplayName: input.DisplayName,
		PasswordPresent: input.Password != "",
	})
}

func (input FirstAdministratorInput) Validate() error {
	hasID := input.TenantID != uuid.Nil
	hasSlug := strings.TrimSpace(input.TenantSlug) != ""
	if hasID == hasSlug {
		return fmt.Errorf("%w: specify exactly one of tenantId or tenantSlug", ErrTenantRequired)
	}
	if err := ValidateLoginName(input.LoginName); err != nil {
		return err
	}
	if err := ValidateDisplayName(input.DisplayName); err != nil {
		return err
	}
	return ValidatePassword(input.Password)
}

type FirstAdministratorResult struct {
	TenantID             uuid.UUID `json:"tenantId"`
	UserID               uuid.UUID `json:"userId"`
	LoginName            string    `json:"loginName"`
	Subject              string    `json:"subject"`
	AccountRevision      int       `json:"accountRevision"`
	CredentialVersion    int       `json:"credentialVersion"`
	AuthorizationVersion int       `json:"authorizationVersion"`
	SessionVersion       int       `json:"sessionVersion"`
}

type LoginAccount struct {
	TenantID             uuid.UUID
	UserID               uuid.UUID
	LoginName            string
	DisplayName          string
	Status               string
	PasswordState        string
	MustChangePassword   bool
	AccountRevision      int
	CredentialVersion    int
	AuthorizationVersion int
	SessionVersion       int
	PasswordHash         string
	FailedCount          int
	WindowStartedAt      *time.Time
	LockedUntil          *time.Time
}

// MarshalJSON makes accidental log/response serialization fail closed for the
// password hash. P2-02 must build an explicit account/session view instead of
// serializing this internal lookup record.
func (account LoginAccount) MarshalJSON() ([]byte, error) {
	type safeLoginAccount struct {
		TenantID             uuid.UUID  `json:"tenantId"`
		UserID               uuid.UUID  `json:"userId"`
		LoginName            string     `json:"loginName"`
		DisplayName          string     `json:"displayName"`
		Status               string     `json:"status"`
		PasswordState        string     `json:"passwordState"`
		MustChangePassword   bool       `json:"mustChangePassword"`
		AccountRevision      int        `json:"accountRevision"`
		CredentialVersion    int        `json:"credentialVersion"`
		AuthorizationVersion int        `json:"authorizationVersion"`
		SessionVersion       int        `json:"sessionVersion"`
		FailedCount          int        `json:"failedCount"`
		WindowStartedAt      *time.Time `json:"windowStartedAt,omitempty"`
		LockedUntil          *time.Time `json:"lockedUntil,omitempty"`
		PasswordHashPresent  bool       `json:"passwordHashPresent"`
	}
	return json.Marshal(safeLoginAccount{
		TenantID: account.TenantID, UserID: account.UserID,
		LoginName: account.LoginName, DisplayName: account.DisplayName,
		Status: account.Status, PasswordState: account.PasswordState,
		MustChangePassword:   account.MustChangePassword,
		AccountRevision:      account.AccountRevision,
		CredentialVersion:    account.CredentialVersion,
		AuthorizationVersion: account.AuthorizationVersion,
		SessionVersion:       account.SessionVersion,
		FailedCount:          account.FailedCount,
		WindowStartedAt:      account.WindowStartedAt,
		LockedUntil:          account.LockedUntil,
		PasswordHashPresent:  account.PasswordHash != "",
	})
}
