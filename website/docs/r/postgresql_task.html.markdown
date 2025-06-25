---
layout: "postgresql"
page_title: "PostgreSQL: postgresql_task"
sidebar_current: "docs-postgresql-resource-postgresql_task"
description: |-
Creates and manages a scheduled task on a PostgreSQL server.
---

# postgresql_task

The `postgresql_task` resource creates and manages a scheduled on a PostgreSQL server. Similar in concept to [Snowflake tasks](https://docs.snowflake.com/en/user-guide/tasks-intro), this relies on the `pg_cron` extension. This extension is not created by default, and must be managed separately from the task itself.

## Usage

```hcl
resource "postgresql_extension" "pg_cron" {
    name = "pg_cron"
}
resource "postgresql_task" "scheduled_task" {
    database  = "database_name"
    schema = "schema_name"
    name = "scheduled_task"
    query = <<-EOF
      SELECT schemaname, tablename
      FROM pg_catalog.pg_tables;
    EOF
    schedule = "0 * * * *"
  	depends_on = [postgresql_extension.pg_cron]
}
```

## Argument Reference

- `database` - (Optional) The database where the task is located.
  If not specified, the task is created in the current database.

- `schema` - (Optional) The schema where the task is located, used to
  namespace the task name. If not specified, the task is created in the
  'public' schema.

- `name` - (Required) The name of the task.

- `query` - (Required) The query.

- `schedule` - (Required) Schedule (cron) on which to run the task.

## pg_cron documentation

- https://github.com/citusdata/pg_cron
