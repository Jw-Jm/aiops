package action

import (
	"bytes"
	"golang.org/x/crypto/ssh"
	"time"
)

func VerifySSHCertificate(raw, key, authority, principal string, ttl time.Duration) error {
	parsed, _, _, rest, err := ssh.ParseAuthorizedKey([]byte(raw))
	if err != nil || len(rest) > 0 {
		return ErrDenied
	}
	cert, ok := parsed.(*ssh.Certificate)
	if !ok {
		return ErrDenied
	}
	pub, _, _, rest, err := ssh.ParseAuthorizedKey([]byte(key))
	if err != nil || len(rest) > 0 {
		return ErrDenied
	}
	ca, _, _, rest, err := ssh.ParseAuthorizedKey([]byte(authority))
	if err != nil || len(rest) > 0 {
		return ErrDenied
	}
	if cert.CertType != ssh.UserCert || !bytes.Equal(cert.Key.Marshal(), pub.Marshal()) || !bytes.Equal(cert.SignatureKey.Marshal(), ca.Marshal()) || len(cert.ValidPrincipals) != 1 || cert.ValidPrincipals[0] != principal || cert.ValidBefore > uint64(time.Now().Add(ttl).Unix()) || ttl <= 0 {
		return ErrDenied
	}
	checker := ssh.CertChecker{IsUserAuthority: func(key ssh.PublicKey) bool { return bytes.Equal(key.Marshal(), ca.Marshal()) }}
	if checker.CheckCert(principal, cert) != nil {
		return ErrDenied
	}
	return nil
}
