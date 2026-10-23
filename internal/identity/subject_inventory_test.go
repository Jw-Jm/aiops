package identity

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

func TestSubjectInventoryRequiresExplicitUniqueReviewedBindings(t *testing.T) {
	doc := SubjectInventory{SchemaVersion: "native-subject-inventory/v1", TenantID: uuid.New(), ReviewedBy: "reviewed operator", Entries: []SubjectInventoryEntry{{LegacySubject: "legacy:opaque-subject", UserID: uuid.New()}}}
	raw, _ := json.Marshal(doc)
	parsed, canonical, err := ParseSubjectInventory(raw)
	if err != nil || parsed.Entries[0] != doc.Entries[0] {
		t.Fatalf("explicit binding rejected: %v", err)
	}
	var pretty bytes.Buffer
	if err = json.Indent(&pretty, raw, "", "  "); err != nil {
		t.Fatal(err)
	}
	_, again, err := ParseSubjectInventory(pretty.Bytes())
	if err != nil || !bytes.Equal(canonical, again) {
		t.Fatal("formatting changed approval digest")
	}
	for name, mutate := range map[string]func(*SubjectInventory){
		"unversioned":        func(d *SubjectInventory) { d.SchemaVersion = "" },
		"no tenant":          func(d *SubjectInventory) { d.TenantID = uuid.Nil },
		"NUL reviewer":       func(d *SubjectInventory) { d.ReviewedBy = "reviewer\x00name" },
		"NUL subject":        func(d *SubjectInventory) { d.Entries[0].LegacySubject = "old\x00subject" },
		"no reviewer":        func(d *SubjectInventory) { d.ReviewedBy = " " },
		"no entries":         func(d *SubjectInventory) { d.Entries = nil },
		"same subject twice": func(d *SubjectInventory) { d.Entries = append(d.Entries, d.Entries[0]) },
		"empty subject":      func(d *SubjectInventory) { d.Entries[0].LegacySubject = " " },
		"missing native id":  func(d *SubjectInventory) { d.Entries[0].UserID = uuid.Nil },
	} {
		t.Run(name, func(t *testing.T) {
			copy := doc
			copy.Entries = append([]SubjectInventoryEntry{}, doc.Entries...)
			mutate(&copy)
			bad, _ := json.Marshal(copy)
			if _, _, err := ParseSubjectInventory(bad); err == nil {
				t.Fatal("invalid binding accepted")
			}
		})
	}
	for _, bad := range [][]byte{
		bytes.Replace(raw, []byte(`"schemaVersion":`), []byte(`"schemaVersion":"duplicate","schemaVersion":`), 1),
		bytes.Replace(raw, []byte(`"legacySubject":`), []byte(`"loginName":"same-name","legacySubject":`), 1),
		append(append([]byte{}, raw...), []byte(` {}`)...),
		bytes.Repeat([]byte(" "), (1<<20)+1),
	} {
		if _, _, err := ParseSubjectInventory(bad); err == nil {
			t.Fatal("ambiguous/oversized document accepted")
		}
	}
}
