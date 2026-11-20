---
layout: "postgresql"
page_title: "PostgreSQL: postgresql_trigger"
sidebar_current: "docs-postgresql-resource-postgresql_trigger"
description: |-
  Creates and manages a trigger on a PostgreSQL table or view.
---

# postgresql_trigger

The `postgresql_trigger` resource manages a trigger on a table, view, or foreign
table. Requires PostgreSQL 11 or later.

## Usage

```hcl
resource "postgresql_function" "check_balance" {
    name = "check_balance"
    schema = "public"
    trigger = true
    body = <<-SQL
        BEGIN
            IF NEW.balance < 0 THEN
                RAISE EXCEPTION 'Balance cannot be negative';
            END IF;
            RETURN NEW;
        END;
    SQL
}

# public.accounts must already exist.
resource "postgresql_trigger" "check_balance" {
    name = "check_balance"
    when = "BEFORE"
    event = ["INSERT", "UPDATE"]
    update_columns = ["balance"]
    table_name = "public.accounts"
    level = "ROW"
    condition = "NEW.balance IS NOT NULL"
    function_name = "${postgresql_function.check_balance.schema}.${postgresql_function.check_balance.name}"
}
```

## Argument Reference

* `name` - (Required) Trigger name, unique within the target table. The name is
  a single identifier; the trigger inherits the table's schema.

* `when` - (Required) One of `BEFORE`, `AFTER`, or `INSTEAD OF`.

* `event` - (Required) A nonempty list containing `INSERT`, `UPDATE`, `DELETE`,
  and/or `TRUNCATE`. Duplicate events are rejected. Event order has no effect
  on when the trigger fires.

* `update_columns` - (Optional) List of column names used in `UPDATE OF`.
  Requires `UPDATE` in `event`. Cannot be used with `INSTEAD OF`. Duplicate
  columns are rejected. When other events are also present, the column
  restriction applies only to `UPDATE`.

* `table_name` - (Required) Existing table, view, or foreign table, optionally
  schema-qualified. Unqualified names use the connection's search path.

* `level` - (Optional) `STATEMENT` or `ROW`. Default is `STATEMENT`.
  This represents SQL `FOR EACH`; Terraform reserves `for_each` for resource iteration.
  `TRUNCATE` requires `STATEMENT`. `INSTEAD OF` requires `ROW` and a view.
  `BEFORE` and `AFTER` triggers on views require `STATEMENT`.

* `condition` - (Optional) Boolean SQL expression for the `WHEN` clause,
  without the `WHEN` keyword. Omit it to fire unconditionally.
  Conditions cannot contain subqueries or be used with `INSTEAD OF`.
  `OLD` and `NEW` row references require `level = "ROW"`.
  `OLD` is forbidden if `event` includes `INSERT`; `NEW` is forbidden if
  `event` includes `DELETE`, including lists with multiple events.
  `BEFORE` conditions cannot access system columns of `NEW`, such as `ctid`.
  The provider checks these restrictions while planning when the values are
  known; PostgreSQL validates SQL syntax, types, column references, and privileges
  during apply. String literals and SQL comments are excluded from reference
  checks. The provider preserves configured condition text while detecting
  changes made to the stored condition outside Terraform. Import uses
  PostgreSQL's formatting of the expression.

* `function_name` - (Required) Existing function to execute, optionally
  schema-qualified, without parentheses. Must take no declared arguments and
  return `trigger`; use `postgresql_function` with `trigger = true` to create it.

* `database` - (Optional) Database containing the trigger. Defaults to the
  provider's database. The table and function must exist in this database.

For `table_name` and `function_name`, the provider quotes each name component
and preserves case. Use double quotes around components that contain dots or
spaces, for example `table_name = "public.\"Accounts.Archive\""`.
Column names and the trigger's `name` are literal identifiers and are quoted
automatically.

Changing `name`, `table_name`, or `database` replaces the resource. Other changes
replace the trigger definition within a transaction. Constraint triggers,
transition relations, and arguments passed by `CREATE TRIGGER` are not supported.

## Import

Import using `database.schema.table.name`:

```shell
terraform import postgresql_trigger.check_balance 'my_database.public.accounts.check_balance'
```

Use SQL double-quoted components for identifiers containing dots, spaces, or
quotes:

```shell
terraform import postgresql_trigger.archived 'my_database.public."Accounts.Archive"."check.balance"'
```

## PostgreSQL documentation

* [CREATE TRIGGER](https://www.postgresql.org/docs/current/sql-createtrigger.html)
