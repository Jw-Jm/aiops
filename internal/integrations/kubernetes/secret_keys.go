package kubernetes

import "strings"

// secretKeyName reports whether a label/annotation key is likely to carry
// credentials. The projection only keeps whitelisted object paths, so this is
// defence in depth for label and annotation keys: it normalises separators and
// also covers camelCase aliases (accessKey/secretKey/apiKey/privateKey) that a
// substring rule on the raw key would miss.
func secretKeyName(key string) bool {
	name := strings.NewReplacer("_", "", "-", "", ".", "", "/", "").Replace(strings.ToLower(key))
	switch name {
	case "password", "passwd", "passphrase", "token", "secret", "credential",
		"credentials", "apikey", "accesskey", "secretkey", "privatekey",
		"sessionkey", "auth", "authorization", "bearer", "clientsecret":
		return true
	}
	for _, fragment := range []string{"password", "passwd", "passphrase", "token", "secret", "credential", "apikey", "accesskey", "privatekey", "bearer"} {
		if strings.Contains(name, fragment) {
			return true
		}
	}
	return false
}
