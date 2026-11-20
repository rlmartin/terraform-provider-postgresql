package postgresql

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/resource"
	"github.com/hashicorp/terraform-plugin-sdk/v2/terraform"
)

func setupTriggerFixtures(t *testing.T, database string) {
	t.Helper()
	skipIfNotAcc(t)
	testAccPreCheck(t)
	testCheckCompatibleVersion(t, featureTrigger)
	triggerFixtureExec(t, database, `CREATE SCHEMA trigger_tests;
		CREATE TABLE trigger_tests.accounts (value integer, other integer);
		CREATE TABLE trigger_tests.calls (label text, event text);
		CREATE TABLE trigger_tests."Accounts.Test" ("Value.Amount" integer);
		CREATE VIEW trigger_tests.account_view AS SELECT value, other FROM trigger_tests.accounts;`)
	t.Cleanup(func() {
		triggerFixtureExec(t, database, `DROP SCHEMA trigger_tests CASCADE`)
	})
}

func triggerFixtureExec(t *testing.T, database, statement string) {
	t.Helper()
	client := testAccProvider.Meta().(*Client)
	txn, err := startTransaction(client, database)
	if err != nil {
		t.Fatal(err)
	}
	defer deferredRollback(txn)
	if _, err := txn.Exec(statement); err != nil {
		t.Fatal(err)
	}
	if err := txn.Commit(); err != nil {
		t.Fatal(err)
	}
}

func triggerFunctionConfig(database string) string {
	config := ""
	for _, label := range []string{"one", "two"} {
		config += fmt.Sprintf(`
resource "postgresql_function" "%s" {
    database = %q
    schema = "trigger_tests"
    name = "trigger_%s"
    trigger = true
    body = <<-SQL
        BEGIN
            INSERT INTO trigger_tests.calls VALUES ('%s', TG_OP);
            IF TG_WHEN = 'INSTEAD OF' AND TG_OP = 'INSERT' THEN
                INSERT INTO trigger_tests.accounts VALUES (NEW.value, NEW.other);
            END IF;
            IF TG_OP = 'DELETE' THEN RETURN OLD; END IF;
            RETURN NEW;
        END;
    SQL
}
`, label, database, label, label)
	}
	return config
}

func triggerResourceConfig(database, name, table, attributes string) string {
	return triggerFunctionConfig(database) + fmt.Sprintf(`
resource "postgresql_trigger" "test" {
    database = %q
    name = %q
    table_name = %q
    function_name = "${postgresql_function.one.schema}.${postgresql_function.one.name}"
    %s
}
`, database, name, table, attributes)
}

func checkTriggerSQL(database, statements, query string, expected int) resource.TestCheckFunc {
	return func(_ *terraform.State) error {
		client := testAccProvider.Meta().(*Client)
		txn, err := startTransaction(client, database)
		if err != nil {
			return err
		}
		defer deferredRollback(txn)
		if _, err := txn.Exec(statements); err != nil {
			return err
		}
		var actual int
		if err := txn.QueryRow(query).Scan(&actual); err != nil {
			return err
		}
		if actual != expected {
			return fmt.Errorf("trigger query returned %d, expected %d", actual, expected)
		}
		return nil
	}
}

func TestAccPostgresqlTrigger_Lifecycle(t *testing.T) {
	setupTriggerFixtures(t, "")
	create := triggerResourceConfig("", "test_trigger", "trigger_tests.accounts", `
    when = "BEFORE"
    event = ["INSERT", "UPDATE"]
    update_columns = ["value"]
    level = "ROW"
    condition = "NEW.value > 0"
`)
	update := triggerFunctionConfig("") + `
resource "postgresql_trigger" "test" {
    name = "test_trigger"
    table_name = "trigger_tests.accounts"
    function_name = "${postgresql_function.two.schema}.${postgresql_function.two.name}"
    when = "AFTER"
    event = ["UPDATE"]
    update_columns = ["other", "value"]
    level = "ROW"
    condition = "OLD.value IS DISTINCT FROM NEW.value"
}
`
	statement := triggerResourceConfig("", "test_trigger", "trigger_tests.accounts", `
    when = "AFTER"
    event = ["UPDATE"]
`)
	operations := `INSERT INTO trigger_tests.accounts VALUES (1, 0), (-1, 0);
		UPDATE trigger_tests.accounts SET other = 1;
		UPDATE trigger_tests.accounts SET value = 2 WHERE value = 1;`
	resource.Test(t, resource.TestCase{
		Providers: testAccProviders, CheckDestroy: testAccCheckPostgresqlTriggerDestroy,
		Steps: []resource.TestStep{
			{Config: create, Check: resource.ComposeTestCheckFunc(
				resource.TestCheckResourceAttr("postgresql_trigger.test", "when", "BEFORE"),
				resource.TestCheckResourceAttr("postgresql_trigger.test", "event.#", "2"),
				resource.TestCheckResourceAttr("postgresql_trigger.test", "level", "ROW"),
				resource.TestCheckResourceAttr("postgresql_trigger.test", "condition", "NEW.value > 0"),
				checkTriggerSQL("", operations, `SELECT count(*) FROM trigger_tests.calls WHERE label = 'one'`, 2),
			)},
			{
				ResourceName: "postgresql_trigger.test", ImportState: true, ImportStateVerify: true,
				// PostgreSQL deparses the condition with its own formatting.
				ImportStateVerifyIgnore: []string{triggerConditionAttr},
			},
			{Config: update, Check: resource.ComposeTestCheckFunc(
				resource.TestCheckResourceAttr("postgresql_trigger.test", "when", "AFTER"),
				resource.TestCheckResourceAttr("postgresql_trigger.test", "function_name", "trigger_tests.trigger_two"),
				resource.TestCheckResourceAttr("postgresql_trigger.test", "update_columns.#", "2"),
				checkTriggerSQL("", operations, `SELECT count(*) FROM trigger_tests.calls WHERE label = 'two' AND event = 'UPDATE'`, 1),
			)},
			{Config: statement, Check: resource.ComposeTestCheckFunc(
				resource.TestCheckResourceAttr("postgresql_trigger.test", "level", "STATEMENT"),
				resource.TestCheckResourceAttr("postgresql_trigger.test", "condition", ""),
				resource.TestCheckResourceAttr("postgresql_trigger.test", "update_columns.#", "0"),
				checkTriggerSQL("", operations, `SELECT count(*) FROM trigger_tests.calls`, 2),
			)},
		},
	})
}

func TestAccPostgresqlTrigger_InsteadOf(t *testing.T) {
	setupTriggerFixtures(t, "")
	config := triggerResourceConfig("", "view_trigger", "trigger_tests.account_view", `
    when = "INSTEAD OF"
    event = ["INSERT"]
    level = "ROW"
`)
	resource.Test(t, resource.TestCase{
		Providers: testAccProviders, CheckDestroy: testAccCheckPostgresqlTriggerDestroy,
		Steps: []resource.TestStep{{Config: config, Check: resource.ComposeTestCheckFunc(
			resource.TestCheckResourceAttr("postgresql_trigger.test", "when", "INSTEAD OF"),
			checkTriggerSQL("", `INSERT INTO trigger_tests.account_view VALUES (10, 1)`, `SELECT count(*) FROM trigger_tests.accounts WHERE value = 10`, 1),
		)}},
	})
}

func TestAccPostgresqlTrigger_Truncate(t *testing.T) {
	setupTriggerFixtures(t, "")
	config := triggerResourceConfig("", "truncate_trigger", "trigger_tests.accounts", `
    when = "AFTER"
    event = ["TRUNCATE"]
    condition = "true"
`)
	resource.Test(t, resource.TestCase{
		Providers: testAccProviders, CheckDestroy: testAccCheckPostgresqlTriggerDestroy,
		Steps: []resource.TestStep{{Config: config, Check: resource.ComposeTestCheckFunc(
			resource.TestCheckResourceAttr("postgresql_trigger.test", "level", "STATEMENT"),
			checkTriggerSQL("", `TRUNCATE trigger_tests.accounts`, `SELECT count(*) FROM trigger_tests.calls WHERE event = 'TRUNCATE'`, 1),
		)}},
	})
}

func TestAccPostgresqlTrigger_QuotedIdentifiers(t *testing.T) {
	setupTriggerFixtures(t, "")
	config := triggerResourceConfig("", `Trigger."Name`, `trigger_tests."Accounts.Test"`, `
    when = "BEFORE"
    event = ["UPDATE"]
    update_columns = ["Value.Amount"]
    level = "ROW"
    condition = "NEW.\"Value.Amount\" > 0"
`)
	resource.Test(t, resource.TestCase{
		Providers: testAccProviders, CheckDestroy: testAccCheckPostgresqlTriggerDestroy,
		Steps: []resource.TestStep{
			{Config: config, Check: checkTriggerSQL("", `INSERT INTO trigger_tests."Accounts.Test" VALUES (0);
				UPDATE trigger_tests."Accounts.Test" SET "Value.Amount" = 1`, `SELECT count(*) FROM trigger_tests.calls`, 1)},
			{ResourceName: "postgresql_trigger.test", ImportState: true, ImportStateVerify: true, ImportStateVerifyIgnore: []string{triggerConditionAttr}},
		},
	})
}

func TestAccPostgresqlTrigger_Drift(t *testing.T) {
	setupTriggerFixtures(t, "")
	config := triggerResourceConfig("", "drift_trigger", "trigger_tests.accounts", `
    when = "BEFORE"
    event = ["INSERT"]
    level = "ROW"
    condition = "NEW.value > 0"
`)
	resource.Test(t, resource.TestCase{
		Providers: testAccProviders, CheckDestroy: testAccCheckPostgresqlTriggerDestroy,
		Steps: []resource.TestStep{
			{Config: config},
			{
				PreConfig: func() {
					triggerFixtureExec(t, "", `DROP TRIGGER drift_trigger ON trigger_tests.accounts;
						CREATE TRIGGER drift_trigger BEFORE UPDATE ON trigger_tests.accounts FOR EACH ROW
						WHEN (NEW.value > 10) EXECUTE FUNCTION trigger_tests.trigger_one();`)
				},
				Config: config, PlanOnly: true, ExpectNonEmptyPlan: true,
			},
			{Config: config, Check: checkTriggerSQL("", `INSERT INTO trigger_tests.accounts VALUES (1, 0)`, `SELECT count(*) FROM trigger_tests.calls`, 1)},
			{
				PreConfig: func() { triggerFixtureExec(t, "", `DROP TRIGGER drift_trigger ON trigger_tests.accounts`) },
				Config:    config, Check: checkTriggerSQL("", `INSERT INTO trigger_tests.accounts VALUES (1, 0)`, `SELECT count(*) FROM trigger_tests.calls`, 1),
			},
			{
				PreConfig: func() { triggerFixtureExec(t, "", `DROP TABLE trigger_tests.accounts CASCADE`) },
				Config:    triggerFunctionConfig(""),
			},
		},
	})
}

func TestAccPostgresqlTrigger_SpecificDatabase(t *testing.T) {
	skipIfNotAcc(t)
	suffix, teardown := setupTestDatabase(t, true, true)
	t.Cleanup(teardown)
	database, _ := getTestDBNames(suffix)
	setupTriggerFixtures(t, database)
	config := triggerResourceConfig(database, "db_trigger", "trigger_tests.accounts", `
    when = "AFTER"
    event = ["INSERT"]
`)
	resource.Test(t, resource.TestCase{
		Providers: testAccProviders, CheckDestroy: testAccCheckPostgresqlTriggerDestroy,
		Steps: []resource.TestStep{
			{Config: config, Check: resource.ComposeTestCheckFunc(
				resource.TestCheckResourceAttr("postgresql_trigger.test", "database", database),
				checkTriggerSQL(database, `INSERT INTO trigger_tests.accounts VALUES (1, 0), (2, 0)`, `SELECT count(*) FROM trigger_tests.calls`, 1),
			)},
			{ResourceName: "postgresql_trigger.test", ImportState: true, ImportStateVerify: true},
		},
	})
}

func TestAccPostgresqlTrigger_InvalidCondition(t *testing.T) {
	setupTriggerFixtures(t, "")
	resource.Test(t, resource.TestCase{
		Providers: testAccProviders,
		Steps: []resource.TestStep{{
			Config: triggerResourceConfig("", "invalid", "trigger_tests.accounts", `
    when = "BEFORE"
    event = ["INSERT", "UPDATE"]
    level = "ROW"
    condition = "OLD.value > 0"
`),
			PlanOnly: true, ExpectError: regexp.MustCompile("condition cannot reference OLD when event includes INSERT"),
		}},
	})
}

func testAccCheckPostgresqlTriggerDestroy(state *terraform.State) error {
	client := testAccProvider.Meta().(*Client)
	for _, rs := range state.RootModule().Resources {
		if rs.Type != "postgresql_trigger" {
			continue
		}
		parts, err := parseTriggerID(rs.Primary.ID)
		if err != nil {
			return err
		}
		txn, err := startTransaction(client, parts[0])
		if err != nil {
			return err
		}
		var exists bool
		err = txn.QueryRow(`SELECT EXISTS (SELECT 1 FROM pg_trigger t
			JOIN pg_class c ON c.oid = t.tgrelid JOIN pg_namespace n ON n.oid = c.relnamespace
			WHERE n.nspname = $1 AND c.relname = $2 AND t.tgname = $3)`, parts[1], parts[2], parts[3]).Scan(&exists)
		deferredRollback(txn)
		if err != nil {
			return err
		}
		if exists {
			return fmt.Errorf("trigger %s still exists after destroy", rs.Primary.ID)
		}
	}
	return nil
}
