package action

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"golang.org/x/crypto/ssh"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func newSSHKey() ([]byte, string, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, "", err
	}
	defer clear(priv)
	key, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, "", err
	}
	sshpub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return nil, "", err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key}), strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshpub))), nil
}
func AnsibleCommand(ctx context.Context, dir string, private []byte, c SSHCredential, command string) (*exec.Cmd, error) {
	if !namePattern.MatchString(c.Principal) || net.ParseIP(c.Address) == nil || c.Port < 1 || c.Port > 65535 || c.KnownHosts == "" || !strings.HasPrefix(c.Certificate, "ssh-ed25519-cert-v01@openssh.com ") || ValidateCommand(command, "bash") != nil {
		return nil, ErrInvalid
	}
	for name, b := range map[string][]byte{"identity": private, "identity-cert.pub": []byte(c.Certificate), "known_hosts": []byte(c.KnownHosts)} {
		if err := os.WriteFile(filepath.Join(dir, name), b, 0600); err != nil {
			return nil, err
		}
	}
	// All connection options belong to this locked transport. Operators supply
	// only Bash, never an inventory/playbook or arbitrary Ansible options.
	input := map[string]any{"command": command, "address": c.Address, "port": c.Port, "principal": c.Principal, "privateDataDir": dir}
	if err := os.WriteFile(filepath.Join(dir, "transport.json"), Canonical(input), 0600); err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, "/usr/local/bin/python3", "/opt/ops/ansible_transport.py", filepath.Join(dir, "transport.json"))
	return cmd, nil
}
