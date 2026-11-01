package repository_test

import (
	"errors"
	"strings"
	"testing"

	"durpdeploy/internal/repository"
)

func TestAgentLabelPolicy(t *testing.T) {
	r := remoteFixture(t)
	for _, label := range []string{"x", "Mixed Case", strings.Repeat("a", 64), strings.Repeat("é", 32)} {
		for i, want := range []int64{1, 0} {
			normalized, changed, err := r.AddAgentLabel(
				t.Context(),
				"a",
				"\u2003 "+label+"\t",
			)
			if err != nil || normalized != label || changed != want {
				t.Fatalf(
					"add %q attempt %d: label=%q rows=%d err=%v",
					label,
					i,
					normalized,
					changed,
					err,
				)
			}
		}
		for i, want := range []int64{1, 0} {
			normalized, changed, err := r.DeleteAgentLabel(
				t.Context(),
				"a",
				"\t"+label+"\u2003",
			)
			if err != nil || normalized != label || changed != want {
				t.Fatalf(
					"delete %q attempt %d: label=%q rows=%d err=%v",
					label,
					i,
					normalized,
					changed,
					err,
				)
			}
		}
	}
}

func TestAgentLabelInvalid(t *testing.T) {
	r := remoteFixture(t)
	for _, raw := range []string{
		"", " \t\n\u2003", strings.Repeat("a", 65), strings.Repeat("é", 33),
	} {
		for _, mutate := range []func(string) (string, int64, error){
			func(label string) (string, int64, error) {
				return r.AddAgentLabel(t.Context(), "missing", label)
			},
			func(label string) (string, int64, error) {
				return r.DeleteAgentLabel(t.Context(), "missing", label)
			},
		} {
			_, changed, err := mutate(raw)
			if !errors.Is(err, repository.ErrInvalidAgentLabel) ||
				changed != 0 {
				t.Fatalf(
					"invalid label %q: changed=%d err=%v",
					raw,
					changed,
					err,
				)
			}
		}
	}
}

func TestAgentLabelMissingAgent(t *testing.T) {
	r := remoteFixture(t)
	label, changed, err := r.AddAgentLabel(t.Context(), "missing", " label ")
	if !errors.Is(err, repository.ErrAgentNotFound) || label != "label" ||
		changed != 0 {
		t.Fatalf(
			"missing agent add: label=%q rows=%d err=%v",
			label,
			changed,
			err,
		)
	}
	label, changed, err = r.DeleteAgentLabel(t.Context(), "missing", " label ")
	if err != nil || label != "label" || changed != 0 {
		t.Fatalf(
			"missing agent delete: label=%q rows=%d err=%v",
			label,
			changed,
			err,
		)
	}
}

func TestAgentLabelDatabaseFailures(t *testing.T) {
	r := remoteFixture(t)
	for _, statement := range []string{
		`CREATE TRIGGER fail_label_add BEFORE INSERT ON agent_labels
BEGIN SELECT RAISE(FAIL, 'label insert failed'); END`,
		`CREATE TRIGGER fail_label_delete BEFORE DELETE ON agent_labels
BEGIN SELECT RAISE(FAIL, 'label delete failed'); END`,
	} {
		if _, err := r.DB.ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
	_, _, err := r.AddAgentLabel(t.Context(), "a", "new")
	if err == nil || !strings.Contains(err.Error(), "label insert failed") {
		t.Fatalf("insert error=%v", err)
	}
	_, _, err = r.DeleteAgentLabel(t.Context(), "a", "linux")
	if err == nil || !strings.Contains(err.Error(), "label delete failed") {
		t.Fatalf("delete error=%v", err)
	}
	if _, err := r.DB.ExecContext(t.Context(), `
DROP TRIGGER fail_label_delete;
ALTER TABLE agents RENAME COLUMN name TO unavailable_name`); err != nil {
		t.Fatal(err)
	}
	_, _, err = r.AddAgentLabel(t.Context(), "a", "new")
	if err == nil || errors.Is(err, repository.ErrAgentNotFound) {
		t.Fatalf("lookup database error=%v", err)
	}
	_, changed, err := r.DeleteAgentLabel(t.Context(), "a", "linux")
	if err != nil || changed != 1 {
		t.Fatalf("delete must not look up agent: rows=%d err=%v", changed, err)
	}
}
