package postgresql

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/resource"
	"github.com/hashicorp/terraform-plugin-sdk/v2/terraform"
)

func TestAccPostgresqlTask_Basic(t *testing.T) {
	config := `
resource "postgresql_extension" "pg_cron" {
	name = "pg_cron"
}
resource "postgresql_task" "basic_task" {
	name = "basic_task"
	query = "SELECT * FROM unnest(ARRAY[1]) AS element;"
	schedule = "0 * * * *"
	depends_on = [postgresql_extension.pg_cron]
}
`

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featureTask)
			testSuperuserPreCheck(t)
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlTaskDestroy,
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlTaskExists("postgresql_task.basic_task", ""),
					resource.TestCheckResourceAttr(
						"postgresql_task.basic_task", "schema", "public"),
					resource.TestCheckResourceAttr(
						"postgresql_task.basic_task", "name", "basic_task"),
					resource.TestCheckResourceAttr(
						"postgresql_task.basic_task", "query", "SELECT * FROM unnest(ARRAY[1]) AS element;"),
					resource.TestCheckResourceAttr(
						"postgresql_task.basic_task", "schedule", "0 * * * *"),
				),
			},
		},
	})
}

func TestAccPostgresqlTask_WithQuotes(t *testing.T) {
	config := `
resource "postgresql_extension" "pg_cron" {
	name = "pg_cron"
}
resource "postgresql_task" "basic_task" {
	name = "basic_task"
	query = <<-EOF
SELECT 1 AS "One", '2' AS two;
	EOF
	schedule = "0 * * * *"
	depends_on = [postgresql_extension.pg_cron]
}
`

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featureTask)
			testSuperuserPreCheck(t)
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlTaskDestroy,
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlTaskExists("postgresql_task.basic_task", ""),
					resource.TestCheckResourceAttr(
						"postgresql_task.basic_task", "schema", "public"),
					resource.TestCheckResourceAttr(
						"postgresql_task.basic_task", "name", "basic_task"),
					resource.TestCheckResourceAttr(
						"postgresql_task.basic_task", "query", "SELECT 1 AS \"One\", '2' AS two;\n"),
					resource.TestCheckResourceAttr(
						"postgresql_task.basic_task", "schedule", "0 * * * *"),
				),
			},
		},
	})
}

func TestAccPostgresqlTask_SpecificDatabase(t *testing.T) {
	skipIfNotAcc(t)

	dbSuffix, teardown := setupTestDatabase(t, true, true)
	defer teardown()

	dbName, _ := getTestDBNames(dbSuffix)

	config := `
resource "postgresql_extension" "pg_cron" {
	name = "pg_cron"
}
resource "postgresql_task" "basic_task" {
	database = "%s"
	schema = "my_schema"
	name = "basic_task"
	query = "SELECT * FROM unnest(ARRAY[1]) AS element;"
	schedule = "0 * * * *"
	depends_on = [postgresql_extension.pg_cron]
}
`
	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featureTask)
			testSuperuserPreCheck(t)
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlTaskDestroy,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(config, dbName),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlTaskExists("postgresql_task.basic_task", ""),
					resource.TestCheckResourceAttr(
						"postgresql_task.basic_task", "database", dbName),
					resource.TestCheckResourceAttr(
						"postgresql_task.basic_task", "schema", "my_schema"),
					resource.TestCheckResourceAttr(
						"postgresql_task.basic_task", "name", "basic_task"),
					resource.TestCheckResourceAttr(
						"postgresql_task.basic_task", "query", "SELECT * FROM unnest(ARRAY[1]) AS element;"),
					resource.TestCheckResourceAttr(
						"postgresql_task.basic_task", "schedule", "0 * * * *"),
				),
			},
		},
	})
}

func TestAccPostgresqlTask_Update(t *testing.T) {
	skipIfNotAcc(t)

	dbSuffix, teardown := setupTestDatabase(t, true, true)
	defer teardown()

	dbName, _ := getTestDBNames(dbSuffix)

	configCreate := `
resource "postgresql_extension" "pg_cron" {
	name = "pg_cron"
}
resource "postgresql_task" "basic_task" {
	name = "basic_task"
	query = "SELECT * FROM unnest(ARRAY[1]) AS element;"
	schedule = "0 * * * *"
	depends_on = [postgresql_extension.pg_cron]
}
`

	configUpdate := `
resource "postgresql_extension" "pg_cron" {
	name = "pg_cron"
}
resource "postgresql_task" "basic_task" {
	database = "%s"
	schema = "my_schema"
	name = "basic_task2"
	query = "SELECT count(*) FROM unnest(ARRAY[1]) AS element;"
	schedule = "0 0 0 * *"
	depends_on = [postgresql_extension.pg_cron]
}
`
	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featureView)
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlViewDestroy,
		Steps: []resource.TestStep{
			{
				Config: configCreate,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlTaskExists("postgresql_task.basic_task", ""),
					resource.TestCheckResourceAttr(
						"postgresql_task.basic_task", "database", "postgres"),
					resource.TestCheckResourceAttr(
						"postgresql_task.basic_task", "schema", "public"),
					resource.TestCheckResourceAttr(
						"postgresql_task.basic_task", "name", "basic_task"),
					resource.TestCheckResourceAttr(
						"postgresql_task.basic_task", "query", "SELECT * FROM unnest(ARRAY[1]) AS element;"),
					resource.TestCheckResourceAttr(
						"postgresql_task.basic_task", "schedule", "0 * * * *"),
				),
			},
			{
				Config: fmt.Sprintf(configUpdate, dbName),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlTaskExists("postgresql_task.basic_task", ""),
					resource.TestCheckResourceAttr(
						"postgresql_task.basic_task", "database", dbName),
					resource.TestCheckResourceAttr(
						"postgresql_task.basic_task", "schema", "my_schema"),
					resource.TestCheckResourceAttr(
						"postgresql_task.basic_task", "name", "basic_task2"),
					resource.TestCheckResourceAttr(
						"postgresql_task.basic_task", "query", "SELECT count(*) FROM unnest(ARRAY[1]) AS element;"),
					resource.TestCheckResourceAttr(
						"postgresql_task.basic_task", "schedule", "0 0 0 * *"),
				),
			},
		},
	})
}

func testAccCheckPostgresqlTaskDestroy(s *terraform.State) error {
	client := testAccProvider.Meta().(*Client)

	for _, rs := range s.RootModule().Resources {
		if rs.Type != "postgresql_task" {
			continue
		}

		taskId := rs.Primary.ID

		txn, err := startTransaction(client, "")
		if err != nil {
			return err
		}
		defer deferredRollback(txn)

		exists, err := checkTaskExists(txn, taskId)

		if err != nil {
			if strings.Contains(err.Error(), "relation \"cron.job\" does not exist") {
				// Extension was removed before the task, effectively removing the task.
				return nil
			}
			return fmt.Errorf("Error checking task %s", err)
		}

		if exists {
			return fmt.Errorf("Task still exists after destroy")
		}
	}

	return nil
}

func testAccCheckPostgresqlTaskExists(n string, database string) resource.TestCheckFunc {
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

		exists, err := checkTaskExists(txn, signature)

		if err != nil {
			return fmt.Errorf("Error checking function %s", err)
		}

		if !exists {
			return fmt.Errorf("Task not found")
		}

		return nil
	}
}

func checkTaskExists(txn *sql.Tx, signature string) (bool, error) {
	var exists bool
	taskIDParts := strings.Split(signature, ".")
	err := txn.QueryRow(fmt.Sprintf("SELECT count(*) > 0 AS exists FROM cron.job where jobname = '%s' and database = '%s'", signature, taskIDParts[0])).Scan(&exists)
	switch {
	case err == sql.ErrNoRows:
		return false, nil
	case err != nil:
		return false, fmt.Errorf("Error reading info about task: %s", err)
	}

	return exists, nil
}
