package action

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"mvdan.cc/sh/v3/syntax"
	"slices"
	"strings"
	"unicode/utf8"
)

const ComparatorVersion = "bash-target-v1-mvdan-3.14.1"
const RiskVersion = "bash-risk/v1-mvdan-3.14.1"

var ErrInvalid = errors.New("invalid command request")
var ErrDenied = errors.New("command request denied")
var ErrConflict = errors.New("command request conflicts with durable state")
var ErrAcknowledgement = errors.New("risk acknowledgement missing, changed, consumed or expired")

func ValidateCommand(command, shell string) error {
	if shell != "bash" || !utf8.ValidString(command) || strings.ContainsRune(command, 0) || len(command) > 64<<10 || strings.TrimSpace(command) == "" {
		return ErrInvalid
	}
	return nil
}
func Digest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

type Risk struct {
	OperationRisk  string   `json:"operationRisk"`
	DataRisk       string   `json:"dataRisk"`
	Features       []string `json:"features"`
	Warnings       []string `json:"warnings"`
	RequiresStepUp bool     `json:"requiresStepUp"`
	Version        string   `json:"riskAssessmentVersion"`
}

func parse(command string) (*syntax.File, error) {
	return syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(command), "")
}

// AST analysis describes risk. It neither rewrites Bash nor grants authority.
func Classify(command string) Risk {
	r := Risk{OperationRisk: "medium", DataRisk: "D1", Features: []string{}, Warnings: []string{"AST analysis is not a sandbox; target credentials and network enforce scope"}, Version: RiskVersion}
	high := func(feature string) {
		r.OperationRisk = "high"
		r.DataRisk = "D3"
		if !slices.Contains(r.Features, feature) {
			r.Features = append(r.Features, feature)
		}
	}
	f, err := parse(command)
	if err != nil {
		high("parse_unknown")
		r.Warnings = append(r.Warnings, "Bash effect cannot be determined")
		return r
	}
	syntax.Walk(f, func(node syntax.Node) bool {
		switch n := node.(type) {
		case *syntax.BinaryCmd:
			high("compound_command")
		case *syntax.Redirect:
			high("redirect")
			if n.Hdoc != nil {
				high("heredoc")
			}
		case *syntax.Subshell, *syntax.CmdSubst, *syntax.ProcSubst:
			high("subshell")
		case *syntax.ParamExp, *syntax.ArithmExp, *syntax.ExtGlob:
			high("dynamic_expansion")
		case *syntax.FuncDecl, *syntax.ForClause, *syntax.WhileClause, *syntax.IfClause, *syntax.CoprocClause:
			high("control_flow")
		case *syntax.CallExpr:
			if len(n.Args) == 0 {
				high("assignment")
				break
			}
			name := n.Args[0].Lit()
			if name == "" {
				high("dynamic_executable")
				break
			}
			switch name {
			case "true", "false", "printf", "echo", "cat", "head", "tail", "wc", "id", "whoami", "pwd", "ls", "date":
			case "kubectl":
				if len(n.Args) < 2 || !slices.Contains([]string{"get", "describe", "logs", "version", "api-resources", "api-versions"}, n.Args[1].Lit()) {
					high("kubernetes_mutation")
				}
			case "virtctl":
				high("virtualization_disabled")
			case "sudo", "su", "doas":
				high("privilege_change")
			case "curl", "wget", "nc", "ssh", "scp":
				high("network_or_exfiltration")
			default:
				high("effect_unknown")
			}
			if len(n.Assigns) > 0 {
				high("environment_assignment")
			}
		case *syntax.Stmt:
			if n.Background {
				high("background")
			}
		}
		return true
	})
	slices.Sort(r.Features)
	return r
}

// Conservative versioned comparison requires the same executable and primary
// literal operands for EVERY call. Dynamic commands cannot establish similarity.
func executableTargets(command string) ([]string, bool) {
	f, err := parse(command)
	if err != nil {
		return nil, false
	}
	targets := []string{}
	known := true
	syntax.Walk(f, func(node syntax.Node) bool {
		switch n := node.(type) {
		case *syntax.CallExpr:
			if len(n.Args) == 0 {
				known = false
				break
			}
			parts := []string{}
			for i, a := range n.Args {
				if i >= 3 {
					break
				}
				v := a.Lit()
				if v == "" {
					known = false
				}
				parts = append(parts, v)
			}
			targets = append(targets, strings.Join(parts, "\x00"))
		case *syntax.ParamExp, *syntax.CmdSubst, *syntax.ProcSubst:
			known = false
		}
		return true
	})
	return targets, known && len(targets) > 0
}
func SuggestionMatch(suggestion *string, actual string) string {
	if suggestion == nil {
		return "not_applicable"
	}
	if *suggestion == actual {
		return "exact"
	}
	a, ok := executableTargets(*suggestion)
	b, ok2 := executableTargets(actual)
	if ok && ok2 && slices.Equal(a, b) {
		return "modified"
	}
	return "unrelated"
}
