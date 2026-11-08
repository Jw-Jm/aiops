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

func TestKubectlRiskWithGlobalFlags(t *testing.T) {
	for _, command := range []string{"kubectl --request-timeout=10s --namespace=ops-current get pod x", "kubectl -n ops-current --context current logs x", "kubectl --namespace ops-current describe pod x"} {
		if risk := Classify(command); risk.OperationRisk != "medium" {
			t.Errorf("literal read classified as mutation: %q %+v", command, risk)
		}
	}
	for _, command := range []string{"kubectl -n ops-current delete pod x", "kubectl --context=current exec x -- id", "kubectl --unknown get delete pod x", "kubectl --namespace", "kubectl -n $NS get pod x", "kubectl --namespace= get pod x", "kubectl --namespace --help get pod x", "kubectl -- get pod x", "kubectl --request-timeout=10s get pod x; rm x"} {
		if risk := Classify(command); risk.OperationRisk != "high" {
			t.Errorf("ambiguous or mutating command downgraded: %q %+v", command, risk)
		}
	}
}
func ptr(s string) *string { return &s }
