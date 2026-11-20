package postgresql

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-sdk/v2/terraform"
)

func TestValidateTriggerCondition(t *testing.T) {
	for _, test := range []struct {
		name, condition, when, forEach string
		events                         []string
		wantError                      string
	}{
		{"update", "OLD.value IS DISTINCT FROM NEW.value", "BEFORE", "ROW", []string{"UPDATE"}, ""},
		{"insert", "NEW.value > 0", "BEFORE", "ROW", []string{"INSERT", "UPDATE"}, ""},
		{"delete", "OLD.value > 0", "AFTER", "ROW", []string{"DELETE", "UPDATE"}, ""},
		{"statement constant", "current_user <> 'postgres'", "AFTER", "STATEMENT", []string{"INSERT"}, ""},
		{"literal keywords", `NEW.value::text <> 'OLD.value SELECT NEW.ctid'`, "BEFORE", "ROW", []string{"INSERT"}, ""},
		{"escaped literal", `NEW.value::text <> E'it\'s OLD.value SELECT'`, "BEFORE", "ROW", []string{"INSERT"}, ""},
		{"dollar literal", `NEW.value::text <> $text$OLD.value SELECT$text$`, "BEFORE", "ROW", []string{"INSERT"}, ""},
		{"comment keywords", "NEW.value > 0 /* OLD.value /* SELECT */ */ -- OLD.value\n", "BEFORE", "ROW", []string{"INSERT"}, ""},
		{"quoted column", `NEW."OLD" > 0`, "BEFORE", "ROW", []string{"INSERT"}, ""},
		{"row named column", "NEW.old > 0", "BEFORE", "ROW", []string{"INSERT"}, ""},
		{"keyword column", "NEW.select > 0", "BEFORE", "ROW", []string{"INSERT"}, ""},
		{"type named old", "CAST(NEW.value AS old) IS NOT NULL", "BEFORE", "ROW", []string{"INSERT"}, ""},
		{"cast named old", "NEW.value::old IS NOT NULL", "BEFORE", "ROW", []string{"INSERT"}, ""},
		{"unicode dollar literal", `NEW.value::text <> $текст$OLD.value SELECT$текст$`, "BEFORE", "ROW", []string{"INSERT"}, ""},
		{"qualified function", "old.test()", "AFTER", "STATEMENT", []string{"INSERT"}, ""},
		{"after system column", "NEW.ctid IS NOT NULL", "AFTER", "ROW", []string{"INSERT"}, ""},
		{"instead condition", "true", "INSTEAD OF", "ROW", []string{"INSERT"}, "INSTEAD OF"},
		{"insert old", "OLD.value > 0", "BEFORE", "ROW", []string{"INSERT"}, "OLD"},
		{"multi event old", "OLD.value > 0", "AFTER", "ROW", []string{"UPDATE", "INSERT"}, "OLD"},
		{"delete new", "new.value > 0", "AFTER", "ROW", []string{"UPDATE", "DELETE"}, "NEW"},
		{"whole row old", "OLD IS NOT NULL", "AFTER", "ROW", []string{"INSERT"}, "OLD"},
		{"whole row new", "NEW IS NOT NULL", "BEFORE", "ROW", []string{"DELETE"}, "NEW"},
		{"quoted old", `"old".value > 0`, "BEFORE", "ROW", []string{"INSERT"}, "OLD"},
		{"statement row", "NEW.value > 0", "BEFORE", "STATEMENT", []string{"UPDATE"}, "STATEMENT"},
		{"before system column", "NEW.ctid IS NOT NULL", "BEFORE", "ROW", []string{"INSERT"}, "system columns"},
		{"parenthesized system column", `(NEW)."xmin" > 0`, "BEFORE", "ROW", []string{"INSERT"}, "system columns"},
		{"subquery", "EXISTS (SELECT 1)", "AFTER", "STATEMENT", []string{"INSERT"}, "subqueries"},
		{"table subquery", "EXISTS (TABLE accounts)", "AFTER", "STATEMENT", []string{"UPDATE"}, "subqueries"},
		{"values subquery", "1 IN (VALUES (1))", "AFTER", "STATEMENT", []string{"UPDATE"}, "subqueries"},
		{"multiple statements", "true); SELECT 1; --", "AFTER", "STATEMENT", []string{"INSERT"}, "parentheses"},
		{"semicolon", "true;", "AFTER", "STATEMENT", []string{"INSERT"}, "single Boolean"},
		{"comment only", "/* true */", "AFTER", "STATEMENT", []string{"INSERT"}, "Boolean expression"},
		{"unbalanced", "(NEW.value > 0", "AFTER", "ROW", []string{"INSERT"}, "parentheses"},
		{"unclosed comment", "true /*", "AFTER", "STATEMENT", []string{"INSERT"}, "unterminated"},
		{"unclosed quote", "'true", "AFTER", "STATEMENT", []string{"INSERT"}, "unterminated"},
		{"unclosed dollar", "$tag$true", "AFTER", "STATEMENT", []string{"INSERT"}, "unterminated"},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := validateTriggerCondition(test.condition, test.when, test.forEach, test.events)
			if test.wantError == "" && err != nil {
				t.Fatal(err)
			}
			if test.wantError != "" && (err == nil || !strings.Contains(err.Error(), test.wantError)) {
				t.Fatalf("expected error containing %q, got %v", test.wantError, err)
			}
		})
	}
}

func TestTriggerConditionFromDefinition(t *testing.T) {
	for _, test := range []struct{ definition, condition string }{
		{`CREATE TRIGGER t AFTER INSERT ON accounts FOR EACH STATEMENT EXECUTE FUNCTION test()`, ""},
		{`CREATE TRIGGER t BEFORE UPDATE ON accounts FOR EACH ROW WHEN (old.value IS DISTINCT FROM new.value) EXECUTE FUNCTION test()`, "old.value IS DISTINCT FROM new.value"},
		{`CREATE TRIGGER "WHEN" AFTER UPDATE ON "WHEN" FOR EACH ROW WHEN ((new."WHEN" = ') EXECUTE FUNCTION (') AND (new.value > 0)) EXECUTE FUNCTION test()`, `(new."WHEN" = ') EXECUTE FUNCTION (') AND (new.value > 0)`},
		{`CREATE TRIGGER t AFTER INSERT ON accounts FOR EACH ROW WHEN (new.value::text = $$) WHEN ($$) EXECUTE FUNCTION test()`, `new.value::text = $$) WHEN ($$`},
	} {
		condition, err := triggerConditionFromDefinition(test.definition)
		if err != nil || condition != test.condition {
			t.Errorf("condition = %q, %v; want %q", condition, err, test.condition)
		}
	}
}

func TestTriggerConditionsEqual(t *testing.T) {
	for _, test := range []struct {
		a, b  string
		equal bool
	}{
		{"NEW.value > 0", "(new.value > 0)", true},
		{"((NEW.value > 0))", "new.value > 0 /* comment */", true},
		{"NEW.value > 0", "new.value > 1", false},
		{"true OR false AND false", "(true OR false) AND false", false},
		{"NEW.value::text = 'OLD'", "new.value::text = 'old'", false},
		{`NEW."Value" > 0`, "new.value > 0", false},
	} {
		if got := triggerConditionsEqual(test.a, test.b); got != test.equal {
			t.Errorf("equal(%q, %q) = %v, want %v", test.a, test.b, got, test.equal)
		}
	}
}

func TestTriggerNames(t *testing.T) {
	for _, test := range []struct {
		input string
		parts []string
	}{
		{"accounts", []string{"accounts"}},
		{"public.Accounts", []string{"public", "Accounts"}},
		{`"a.b"."c""d"`, []string{"a.b", `c"d`}},
	} {
		parts, err := parseTriggerName(test.input, 2)
		if err != nil || !reflect.DeepEqual(parts, test.parts) {
			t.Errorf("parse(%q) = %v, %v", test.input, parts, err)
		}
	}
	for _, name := range []string{"", "public.", ".accounts", "a.b.c", "test()", "a;DROP TABLE a", `""`, "'accounts'"} {
		if _, err := parseTriggerName(name, 2); err == nil {
			t.Errorf("expected invalid name %q", name)
		}
	}
	id := triggerID("database", "a.b", `c"d`, "trigger name")
	parts, err := parseTriggerID(id)
	if err != nil || !reflect.DeepEqual(parts, []string{"database", "a.b", `c"d`, "trigger name"}) {
		t.Fatalf("ID roundtrip failed: %s => %v, %v", id, parts, err)
	}
	if _, err := parseTriggerID("bad.id"); err == nil {
		t.Fatal("expected malformed import ID to be rejected")
	}
}

func TestTriggerCustomizeDiff(t *testing.T) {
	for _, test := range []struct {
		name   string
		values map[string]interface{}
		error  string
	}{
		{"default statement", nil, ""},
		{"update columns", map[string]interface{}{"event": []interface{}{"UPDATE"}, "update_columns": []interface{}{"value"}}, ""},
		{"columns without update", map[string]interface{}{"update_columns": []interface{}{"value"}}, "requires UPDATE"},
		{"duplicate event", map[string]interface{}{"event": []interface{}{"INSERT", "INSERT"}}, "duplicate"},
		{"duplicate column", map[string]interface{}{"event": []interface{}{"UPDATE"}, "update_columns": []interface{}{"value", "value"}}, "duplicate"},
		{"instead statement", map[string]interface{}{"when": "INSTEAD OF"}, "require level = ROW"},
		{"instead columns", map[string]interface{}{"when": "INSTEAD OF", "level": "ROW", "event": []interface{}{"UPDATE"}, "update_columns": []interface{}{"value"}}, "update_columns"},
		{"truncate row", map[string]interface{}{"level": "ROW", "event": []interface{}{"TRUNCATE"}}, "STATEMENT"},
		{"condition event", map[string]interface{}{"level": "ROW", "condition": "OLD.value > 0"}, "OLD"},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := map[string]interface{}{"name": "test", "when": "BEFORE", "event": []interface{}{"INSERT"}, "table_name": "accounts", "function_name": "test_fn"}
			for key, value := range test.values {
				config[key] = value
			}
			_, err := resourcePostgreSQLTrigger().Diff(context.Background(), nil, terraform.NewResourceConfigRaw(config), nil)
			if test.error == "" && err != nil || test.error != "" && (err == nil || !strings.Contains(err.Error(), test.error)) {
				t.Fatalf("expected error %q, got %v", test.error, err)
			}
		})
	}
}
