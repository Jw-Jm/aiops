package incident

import (
	"github.com/google/uuid"
	"strings"
	"testing"
)

func TestCaseDraftRequiresBoundOriginAndBoundedContent(t *testing.T) {
	valid := CaseDraftInput{Kind: "case", IncidentID: uuid.NewString(), ExpectedRevision: 1, Title: "故障案例", Summary: "已保存的证据", Content: "事实与适用边界"}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*CaseDraftInput){
		func(v *CaseDraftInput) { v.Kind = "runbook" },
		func(v *CaseDraftInput) { v.ExpectedRevision = 0 },
		func(v *CaseDraftInput) { v.IncidentID = "other" },
		func(v *CaseDraftInput) { v.Title = "  " },
		func(v *CaseDraftInput) { v.Summary = strings.Repeat("文", 501) },
		func(v *CaseDraftInput) { v.Content = "text\x00" },
	} {
		in := valid
		change(&in)
		if in.Validate() == nil {
			t.Fatal("invalid draft accepted")
		}
	}
}
