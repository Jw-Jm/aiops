package action

import (
	"strings"
	"testing"
)

func TestCommandValidation(t *testing.T) {
	for _, command := range []string{"", "  ", "echo \x00x", strings.Repeat("x", 65537), string([]byte{0xff})} {
		if ValidateCommand(command, "bash") == nil {
			t.Errorf("accepted invalid command length=%d", len(command))
		}
	}
	if ValidateCommand("printf '%s\\n' 中文", "bash") != nil {
		t.Fatal("rejected UTF-8 Bash")
	}
	if ValidateCommand("true", "sh") == nil {
		t.Fatal("accepted unsupported shell")
	}
}
func TestRiskClassifier(t *testing.T) {
	for _, command := range []string{"echo x | cat", "echo x > /tmp/x", "(true)", "cat <<EOF\nx\nEOF", "kubectl delete pod x", "kubectl apply -f x", "kubectl exec x -- id", "virtctl start x", "curl https://example.org", "systemctl restart x", "rm x", "mkfs /dev/x", "reboot", "sudo id", "$CMD", "eval 'id'", "if"} {
		risk := Classify(command)
		if risk.OperationRisk != "high" {
			t.Errorf("%q risk=%+v", command, risk)
		}
	}
	if Classify("kubectl get pods").OperationRisk != Classify("kubectl  get  pods").OperationRisk {
		t.Fatal("whitespace changed risk")
	}
	if Digest([]byte("kubectl get pods")) == Digest([]byte("kubectl  get pods")) {
		t.Fatal("normalized raw digest")
	}
}
func TestSuggestionMatch(t *testing.T) {
	for _, tc := range []struct {
		suggestion   *string
		actual, want string
	}{
		{nil, "true", "not_applicable"}, {ptr("kubectl get pods"), "kubectl get pods", "exact"},
		{ptr("kubectl get pods"), "kubectl get pods -o json", "modified"},
		{ptr("kubectl get pods"), "kubectl delete pods x", "unrelated"},
		{ptr("if"), "true", "unrelated"},
	} {
		if got := SuggestionMatch(tc.suggestion, tc.actual); got != tc.want {
			t.Fatalf("got %s want %s", got, tc.want)
		}
	}
}
func ptr(s string) *string { return &s }
