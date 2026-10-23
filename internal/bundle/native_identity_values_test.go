package bundle

import (
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"

	"ops-platform/internal/profile"
)

func nativeBusinessDocument() map[string]any {
	d := validBusinessDocument()
	d["humanAuth"] = map[string]any{"mode": "native", "tenantId": "11111111-1111-4111-8111-111111111111", "publicOrigin": "https://ops.example:30466", "mfaTransitKey": "ops-native-mfa"}
	return d
}

func TestNativeDependencyChartDisablesOnlyKeycloak(t *testing.T) {
	helm, err := exec.LookPath("helm")
	if err != nil {
		t.Skip("helm required")
	}
	args := []string{"template", "ops-dependencies", "../../deploy/charts/ops-dependencies", "--set", "components.keycloak.mode=disabled"}
	out, err := exec.Command(helm, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("native dependency chart: %v %s", err, out)
	}
	if strings.Contains(string(out), "name: ops-keycloak") {
		t.Fatal("disabled identity service still rendered")
	}
	for _, name := range []string{"postgresql", "openbao", "seaweedfs"} {
		if _, err := exec.Command(helm, append(args, "--set", "components."+name+".mode=disabled")...).CombinedOutput(); err == nil {
			t.Fatalf("native identity allowed disabling required %s", name)
		}
	}
}

func TestNativeWebPreflightKeepsTLSAndBindsReceipt(t *testing.T) {
	d := capabilityDocument(t)
	d["humanAuth"] = nativeBusinessDocument()["humanAuth"]
	web := object(d, "sp08")
	web["publicOrigin"] = object(d, "humanAuth")["publicOrigin"]
	delete(web, "oidc")
	delete(web, "oidcCAConfigMapName")
	b := readBusinessTest(t, d)
	b.installStage = "business"
	p := importProfile()
	p.Architecture = "amd64"
	p.Components["keycloak"] = profile.ResolvedComponent{Mode: "disabled"}
	if err := validateWebProfile(b, p); err != nil {
		t.Fatal(err)
	}
	run := func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if strings.Contains(strings.Join(args, " "), "oidc") {
			t.Fatal("native preflight requested OIDC trust")
		}
		return []byte("present"), nil
	}
	if err := preflightBusinessSecrets(t.Context(), p, b, run); err != nil {
		t.Fatal(err)
	}
	if err := preflightBusinessSecrets(t.Context(), p, b, func(ctx context.Context, program string, args ...string) ([]byte, error) {
		if strings.Contains(strings.Join(args, " "), "web.key") {
			return []byte("absent"), nil
		}
		return run(ctx, program, args...)
	}); err == nil {
		t.Fatal("native installation accepted missing Web TLS key")
	}
	before := businessInstallationDigest(b)
	object(d, "humanAuth")["mfaTransitKey"] = "changed-key"
	if before == businessInstallationDigest(readBusinessTest(t, d)) {
		t.Fatal("receipt omitted the native identity configuration")
	}
	web["oidc"] = map[string]any{"authority": "https://old.example/realms/ops", "proxyHost": "old.example", "clientId": "old"}
	raw, _ := json.Marshal(d)
	if _, err := ReadBusinessValues(bytes.NewReader(raw)); err == nil {
		t.Fatal("native Web accepted mixed legacy OIDC configuration")
	}
}

func TestNativeIdentityBusinessValuesAndProfile(t *testing.T) {
	b := readBusinessTest(t, nativeBusinessDocument())
	if !b.NativeIdentity() || readBusinessTest(t, validBusinessDocument()).NativeIdentity() {
		t.Fatal("native and legacy modes confused")
	}
	p := importProfile()
	if err := validateIdentityProfile(&b, p); err == nil {
		t.Fatal("native installation accepted enabled Keycloak")
	}
	p.Components["keycloak"] = profile.ResolvedComponent{Mode: "disabled"}
	if err := validateIdentityProfile(&b, p); err != nil {
		t.Fatal(err)
	}
	d := nativeBusinessDocument()
	object(d, "sp06")["tenants"] = []string{"11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222"}
	raw, _ := json.Marshal(d)
	if _, err := ReadBusinessValues(bytes.NewReader(raw)); err == nil {
		t.Fatal("fixed native identity accepted multiple deployment tenants")
	}
	for name, mutate := range map[string]func(map[string]any){
		"unknown mode": func(c map[string]any) { c["mode"] = "unknown" },
		"zero tenant":  func(c map[string]any) { c["tenantId"] = "00000000-0000-0000-0000-000000000000" },
		"other tenant": func(c map[string]any) { c["tenantId"] = "22222222-2222-4222-8222-222222222222" },
		"origin path":  func(c map[string]any) { c["publicOrigin"] = "https://ops.example/path" },
		"origin port":  func(c map[string]any) { c["publicOrigin"] = "https://ops.example:65536" },
		"secret":       func(c map[string]any) { c["password"] = "not-permitted" },
		"key path":     func(c map[string]any) { c["mfaTransitKey"] = "../other-key" },
	} {
		t.Run(name, func(t *testing.T) {
			d := nativeBusinessDocument()
			mutate(d["humanAuth"].(map[string]any))
			raw, err := json.Marshal(d)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := ReadBusinessValues(bytes.NewReader(raw)); err == nil {
				t.Fatal("unsafe native configuration accepted")
			}
		})
	}
}
