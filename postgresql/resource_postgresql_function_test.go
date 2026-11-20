package postgresql

import (
	"database/sql"
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/resource"
	"github.com/hashicorp/terraform-plugin-sdk/v2/terraform"
)

func TestAccPostgresqlFunction_Basic(t *testing.T) {
	config := `
resource "postgresql_function" "basic_function" {
    name = "basic_function"
    returns = "integer"
    language = "plpgsql"
    body = <<-EOF
        BEGIN
            RETURN 1;
        END;
    EOF
}
`

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featureFunction)
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlFunctionDestroy,
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlFunctionExists("postgresql_function.basic_function", ""),
					resource.TestCheckResourceAttr("postgresql_function.basic_function", "trigger", "false"),
					resource.TestCheckResourceAttr(
						"postgresql_function.basic_function", "name", "basic_function"),
					resource.TestCheckResourceAttr(
						"postgresql_function.basic_function", "schema", "public"),
					resource.TestCheckResourceAttr(
						"postgresql_function.basic_function", "language", "plpgsql"),
				),
			},
		},
	})
}

func TestAccPostgresqlFunction_Trigger(t *testing.T) {
	config := func(increment int) string {
		return fmt.Sprintf(`
resource "postgresql_function" "trigger" {
    name = "trigger_function"
    trigger = true
    security_definer = true
    body = "BEGIN NEW.value := NEW.value + %d; RETURN NEW; END;"
}
`, increment)
	}
	check := func(increment int) resource.TestCheckFunc {
		return resource.ComposeTestCheckFunc(
			testAccCheckPostgresqlFunctionExists("postgresql_function.trigger", ""),
			resource.TestCheckResourceAttr("postgresql_function.trigger", "trigger", "true"),
			resource.TestCheckResourceAttr("postgresql_function.trigger", "returns", "trigger"),
			resource.TestCheckResourceAttr("postgresql_function.trigger", "arg.#", "0"),
			func(_ *terraform.State) error {
				client := testAccProvider.Meta().(*Client)
				txn, err := startTransaction(client, "")
				if err != nil {
					return err
				}
				defer deferredRollback(txn)
				if _, err := txn.Exec(`CREATE TEMP TABLE trigger_test (value integer);
					CREATE TRIGGER before_insert BEFORE INSERT ON trigger_test
					FOR EACH ROW EXECUTE FUNCTION public.trigger_function();`); err != nil {
					return err
				}
				var value int
				if err := txn.QueryRow(`INSERT INTO trigger_test VALUES (10) RETURNING value`).Scan(&value); err != nil {
					return err
				}
				if value != 10+increment {
					return fmt.Errorf("expected trigger to produce %d, got %d", 10+increment, value)
				}
				return nil
			},
		)
	}
	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featureFunction)
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlFunctionDestroy,
		Steps: []resource.TestStep{
			{Config: config(1), Check: check(1)},
			{
				ResourceName:            "postgresql_function.trigger",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{funcDropCascadeAttr},
			},
			{Config: config(2), Check: check(2)},
			{
				Config: `
resource "postgresql_function" "trigger" {
    name = "trigger_function"
    trigger = false
    returns = "integer"
    body = "BEGIN RETURN 1; END;"
}
`,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlFunctionExists("postgresql_function.trigger", ""),
					resource.TestCheckResourceAttr("postgresql_function.trigger", "trigger", "false"),
					resource.TestCheckResourceAttr("postgresql_function.trigger", "returns", "integer"),
				),
			},
			{Config: config(2), Check: check(2)},
		},
	})
}

func TestAccPostgresqlFunction_TriggerInvalidAttributes(t *testing.T) {
	for _, attribute := range []struct {
		name   string
		config string
		error  string
	}{
		{"arg", `arg { type = "integer" }`, "arg is not supported for trigger functions"},
		{"out_arg", `arg {
 type = "integer"
 mode = "OUT"
 }`, "arg is not supported for trigger functions"},
		{"returns", `returns = "integer"`, "returns must not be set for trigger functions"},
		{"returns_trigger", `returns = "trigger"`, "returns must not be set for trigger functions"},
		{"strict", `strict = true`, "strict is not supported for trigger functions"},
	} {
		t.Run(attribute.name, func(t *testing.T) {
			resource.Test(t, resource.TestCase{
				PreCheck: func() {
					testAccPreCheck(t)
					testCheckCompatibleVersion(t, featureFunction)
				},
				Providers: testAccProviders,
				Steps: []resource.TestStep{{
					Config: fmt.Sprintf(`
resource "postgresql_function" "invalid" {
    name = "invalid_trigger_function"
    trigger = true
    body = "BEGIN RETURN NEW; END;"
    %s
}
`, attribute.config),
					PlanOnly:    true,
					ExpectError: regexp.MustCompile(attribute.error),
				}},
			})
		})
	}
}

func TestAccPostgresqlFunction_SpecificDatabase(t *testing.T) {
	skipIfNotAcc(t)

	dbSuffix, teardown := setupTestDatabase(t, true, true)
	defer teardown()

	dbName, _ := getTestDBNames(dbSuffix)

	config := `
resource "postgresql_function" "basic_function" {
    name = "basic_function"
    database = "%s"
    returns = "integer"
    language = "plpgsql"
    body = <<-EOF
        BEGIN
            RETURN 1;
        END;
    EOF
}
`

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featureFunction)
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlFunctionDestroy,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(config, dbName),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlFunctionExists("postgresql_function.basic_function", dbName),
					resource.TestCheckResourceAttr(
						"postgresql_function.basic_function", "name", "basic_function"),
					resource.TestCheckResourceAttr(
						"postgresql_function.basic_function", "database", dbName),
					resource.TestCheckResourceAttr(
						"postgresql_function.basic_function", "schema", "public"),
					resource.TestCheckResourceAttr(
						"postgresql_function.basic_function", "language", "plpgsql"),
				),
			},
		},
	})
}

func TestAccPostgresqlFunction_MultipleArgs(t *testing.T) {
	config := `
resource "postgresql_schema" "test" {
    name = "test"
}

resource "postgresql_function" "increment" {
    schema = postgresql_schema.test.name
    name = "increment"
    arg {
        name = "i"
        type = "integer"
        default = "7"
    }
    arg {
        name = "result"
        type = "integer"
        mode = "OUT"
    }
    language = "plpgsql"
    parallel = "RESTRICTED"
    strict = true
    security_definer = true
    volatility = "STABLE"
    body = <<-EOF
        BEGIN
            result = i + 1;
        END;
    EOF
}
`

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featureFunction)
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlFunctionDestroy,
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlFunctionExists("postgresql_function.increment", ""),
					resource.TestCheckResourceAttr(
						"postgresql_function.increment", "name", "increment"),
					resource.TestCheckResourceAttr(
						"postgresql_function.increment", "schema", "test"),
					resource.TestCheckResourceAttr(
						"postgresql_function.increment", "language", "plpgsql"),
					resource.TestCheckResourceAttr(
						"postgresql_function.increment", "strict", "true"),
					resource.TestCheckResourceAttr(
						"postgresql_function.increment", "parallel", "RESTRICTED"),
					resource.TestCheckResourceAttr(
						"postgresql_function.increment", "security_definer", "true"),
					resource.TestCheckResourceAttr(
						"postgresql_function.increment", "volatility", "STABLE"),
				),
			},
		},
	})
}

func TestAccPostgresqlFunction_Update(t *testing.T) {
	configCreate := `
resource "postgresql_function" "func" {
    name = "func"
    returns = "integer"
    language = "plpgsql"
    body = <<-EOF
        BEGIN
            RETURN 1;
        END;
    EOF
}
`

	configUpdate := `
resource "postgresql_function" "func" {
    name = "func"
    returns = "integer"
    language = "plpgsql"
    volatility = "IMMUTABLE"
    body = <<-EOF
        BEGIN
            RETURN 2;
        END;
    EOF
}
`
	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featureFunction)
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlFunctionDestroy,
		Steps: []resource.TestStep{
			{
				Config: configCreate,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlFunctionExists("postgresql_function.func", ""),
					resource.TestCheckResourceAttr(
						"postgresql_function.func", "name", "func"),
					resource.TestCheckResourceAttr(
						"postgresql_function.func", "schema", "public"),
					resource.TestCheckResourceAttr(
						"postgresql_function.func", "volatility", "VOLATILE"),
				),
			},
			{
				Config: configUpdate,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlFunctionExists("postgresql_function.func", ""),
					resource.TestCheckResourceAttr(
						"postgresql_function.func", "name", "func"),
					resource.TestCheckResourceAttr(
						"postgresql_function.func", "schema", "public"),
					resource.TestCheckResourceAttr(
						"postgresql_function.func", "volatility", "IMMUTABLE"),
				),
			},
		},
	})
}

func testAccCheckPostgresqlFunctionExists(n string, database string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[n]
		if !ok {
			return fmt.Errorf("Resource not found: %s", n)
		}

		if rs.Primary.ID == "" {
			return fmt.Errorf("No ID is set")
		}

		signature := rs.Primary.ID

		client := testAccProvider.Meta().(*Client)
		txn, err := startTransaction(client, database)
		if err != nil {
			return err
		}
		defer deferredRollback(txn)

		exists, err := checkFunctionExists(txn, signature)

		if err != nil {
			return fmt.Errorf("Error checking function %s", err)
		}

		if !exists {
			return fmt.Errorf("Function not found")
		}

		return nil
	}
}

func testAccCheckPostgresqlFunctionDestroy(s *terraform.State) error {
	client := testAccProvider.Meta().(*Client)

	for _, rs := range s.RootModule().Resources {
		if rs.Type != "postgresql_function" {
			continue
		}

		txn, err := startTransaction(client, "")
		if err != nil {
			return err
		}
		defer deferredRollback(txn)

		_, functionSignature, expandErr := expandFunctionID(rs.Primary.ID, nil, nil)

		if expandErr != nil {
			return fmt.Errorf("Incorrect resource Id %s", err)
		}

		exists, err := checkFunctionExists(txn, functionSignature)

		if err != nil {
			return fmt.Errorf("Error checking function %s", err)
		}

		if exists {
			return fmt.Errorf("Function still exists after destroy")
		}
	}

	return nil
}

func checkFunctionExists(txn *sql.Tx, signature string) (bool, error) {
	var _rez bool
	err := txn.QueryRow(fmt.Sprintf("SELECT to_regprocedure('%s') IS NOT NULL", signature)).Scan(&_rez)
	switch {
	case err == sql.ErrNoRows:
		return false, nil
	case err != nil:
		return false, fmt.Errorf("Error reading info about function: %s", err)
	}

	return _rez, nil
}
