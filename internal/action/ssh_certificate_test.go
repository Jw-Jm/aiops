package action

import (
	"crypto/ed25519"
	"crypto/rand"
	"golang.org/x/crypto/ssh"
	"testing"
	"time"
)

func TestSSHCertificateMustMatchPinnedAuthorityKeyPrincipalAndTTL(t *testing.T) {
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	signer, _ := ssh.NewSignerFromKey(key)
	_, client, err := newSSHKey()
	if err != nil {
		t.Fatal(err)
	}
	pk, _, _, _, err := ssh.ParseAuthorizedKey([]byte(client))
	if err != nil {
		t.Fatal(err)
	}
	ca, _ := ssh.NewPublicKey(pub)
	cert := &ssh.Certificate{Key: pk, CertType: ssh.UserCert, ValidPrincipals: []string{"opsordinary"}, ValidAfter: uint64(time.Now().Add(-time.Minute).Unix()), ValidBefore: uint64(time.Now().Add(5 * time.Minute).Unix())}
	if err = cert.SignCert(rand.Reader, signer); err != nil {
		t.Fatal(err)
	}
	raw := string(ssh.MarshalAuthorizedKey(cert))
	authority := string(ssh.MarshalAuthorizedKey(ca))
	if err = VerifySSHCertificate(raw, client, authority, "opsordinary", 900*time.Second); err != nil {
		t.Fatal(err)
	}
	if VerifySSHCertificate(raw, client, authority, "root", 900*time.Second) == nil {
		t.Fatal("principal substitution")
	}
	if VerifySSHCertificate(raw, client, authority, "opsordinary", 10*time.Second) == nil {
		t.Fatal("unbounded TTL")
	}
	_, other, _ := newSSHKey()
	if VerifySSHCertificate(raw, other, authority, "opsordinary", 900*time.Second) == nil {
		t.Fatal("key substitution")
	}
	cert.ValidBefore = uint64(time.Now().Add(-time.Second).Unix())
	cert.SignCert(rand.Reader, signer)
	if VerifySSHCertificate(string(ssh.MarshalAuthorizedKey(cert)), client, authority, "opsordinary", 900*time.Second) == nil {
		t.Fatal("expired certificate")
	}
}
