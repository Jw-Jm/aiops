package bootstrap

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"
)

// VictoriaTrust is a private bootstrap input, never a Deployment Profile or a
// Bundle payload. Its CA is operator trust independent of the image publisher.
type VictoriaTrust struct {
	Name        string `json:"name"`
	CA          string `json:"ca"`
	Certificate string `json:"certificate"`
	PrivateKey  string `json:"privateKey"`
	Username    string `json:"username"`
	Password    string `json:"password"`
}

type VictoriaTrustInput struct {
	SchemaVersion int             `json:"schemaVersion"`
	Sources       []VictoriaTrust `json:"sources"`
}

func (c VictoriaTrustInput) Validate(namespace string) error {
	if c.SchemaVersion != 1 || len(c.Sources) != 2 || !environmentName.MatchString(namespace) || namespace == "default" || strings.HasPrefix(namespace, "kube-") {
		return errors.New("two explicit native Victoria identities and dedicated namespace required")
	}
	seen, passwords := map[string]bool{}, map[string]bool{}
	for _, source := range c.Sources {
		if (source.Name != "victoriametrics" && source.Name != "victorialogs") || seen[source.Name] || passwords[source.Password] || !regexp.MustCompile(`^[A-Za-z0-9_.-]{1,128}$`).MatchString(source.Username) || !regexp.MustCompile(`^[!-~]{24,256}$`).MatchString(source.Password) {
			return errors.New("distinct explicit native Victoria credentials required")
		}
		seen[source.Name], passwords[source.Password] = true, true
		roots := x509.NewCertPool()
		if strings.Contains(source.CA, "PRIVATE KEY") || !roots.AppendCertsFromPEM([]byte(source.CA)) {
			return errors.New("independent native Victoria public CA required")
		}
		pair, err := tls.X509KeyPair([]byte(source.Certificate), []byte(source.PrivateKey))
		if err != nil || len(pair.Certificate) == 0 {
			return errors.New("native Victoria TLS certificate/key mismatch")
		}
		leaf, err := x509.ParseCertificate(pair.Certificate[0])
		if err != nil {
			return errors.New("native Victoria certificate invalid")
		}
		intermediates := x509.NewCertPool()
		for _, der := range pair.Certificate[1:] {
			cert, err := x509.ParseCertificate(der)
			if err != nil {
				return errors.New("native Victoria certificate chain invalid")
			}
			intermediates.AddCert(cert)
		}
		service := "ops-victoria-metrics"
		if source.Name == "victorialogs" {
			service = "ops-victoria-logs"
		}
		if _, err = leaf.Verify(x509.VerifyOptions{Roots: roots, Intermediates: intermediates, DNSName: service + "." + namespace + ".svc.cluster.local", CurrentTime: time.Now(), KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err != nil {
			return errors.New("native Victoria certificate differs from actual target trust")
		}
	}
	return nil
}

func (c VictoriaTrust) ServerData() map[string]string {
	return map[string]string{"ca.pem": c.CA, "tls.crt": c.Certificate, "tls.key": c.PrivateKey, "username": c.Username, "password": c.Password}
}

func (c VictoriaTrust) ClientCredential() string {
	raw, _ := json.Marshal(map[string]string{"schemaVersion": "ops-source-http-credentials/v1", "mode": "basic", "username": c.Username, "password": c.Password})
	return string(raw)
}
