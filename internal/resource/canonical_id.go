package resource

import (
	"errors"
	"net/url"
	"regexp"
	"strings"
	"unicode"
)

type CanonicalID struct{ Domain, Tenant, Scope, APIGroup, Kind, StableID string }

var domainPattern = regexp.MustCompile(`^[a-z][a-z0-9.-]*$`)
var tenantPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{0,127}$`)
var ErrIdentity = errors.New("invalid canonical resource identity")

func ValidateCanonicalID(id CanonicalID) error {
	if !domainPattern.MatchString(id.Domain) || !tenantPattern.MatchString(id.Tenant) {
		return ErrIdentity
	}
	for _, v := range []string{id.Scope, id.APIGroup, id.Kind, id.StableID} {
		if v == "" || len(v) > 512 || v == "." || v == ".." || strings.IndexFunc(v, unicode.IsControl) >= 0 {
			return ErrIdentity
		}
	}
	return nil
}
func (id CanonicalID) String() string {
	if ValidateCanonicalID(id) != nil {
		return ""
	}
	return id.Domain + "+v1://" + id.Tenant + "/" + url.PathEscape(id.Scope) + "/" + url.PathEscape(id.APIGroup) + "/" + url.PathEscape(id.Kind) + "/" + url.PathEscape(id.StableID)
}

// Prefix is the canonical identity without its stable id. It is the exact
// prefix every canonical id of the same tenant, scope, API group and kind
// starts with. It exists so a lookup by identity uses one encoding instead of a
// second, hand written one.
func (id CanonicalID) Prefix() string {
	if id.Domain == "" || id.Tenant == "" || id.Scope == "" || id.APIGroup == "" || id.Kind == "" {
		return ""
	}
	return id.Domain + "+v1://" + id.Tenant + "/" + url.PathEscape(id.Scope) + "/" + url.PathEscape(id.APIGroup) + "/" + url.PathEscape(id.Kind) + "/"
}

func ParseCanonicalID(raw string) (CanonicalID, error) {
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !strings.HasSuffix(u.Scheme, "+v1") {
		return CanonicalID{}, ErrIdentity
	}
	parts := strings.Split(strings.TrimPrefix(u.EscapedPath(), "/"), "/")
	if len(parts) != 4 {
		return CanonicalID{}, ErrIdentity
	}
	for i := range parts {
		parts[i], err = url.PathUnescape(parts[i])
		if err != nil {
			return CanonicalID{}, ErrIdentity
		}
	}
	id := CanonicalID{strings.TrimSuffix(u.Scheme, "+v1"), u.Host, parts[0], parts[1], parts[2], parts[3]}
	if ValidateCanonicalID(id) != nil || id.String() != raw {
		return CanonicalID{}, ErrIdentity
	}
	return id, nil
}

func (id CanonicalID) MarshalText() ([]byte, error) {
	if id == (CanonicalID{}) {
		return []byte(""), nil
	}
	if err := ValidateCanonicalID(id); err != nil {
		return nil, err
	}
	return []byte(id.String()), nil
}

func (id *CanonicalID) UnmarshalText(raw []byte) error {
	if len(raw) == 0 {
		*id = CanonicalID{}
		return nil
	}
	parsed, err := ParseCanonicalID(string(raw))
	if err != nil {
		return err
	}
	*id = parsed
	return nil
}
