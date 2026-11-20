---
layout: "postgresql"
page_title: "PostgreSQL: postgresql_function"
sidebar_current: "docs-postgresql-resource-postgresql_function"
description: |-
Creates and manages a function on a PostgreSQL server.
---

# postgresql\_function

The ``postgresql_function`` resource creates and manages a function on a PostgreSQL
server.

## Usage

```hcl
resource "postgresql_function" "increment" {
    name = "increment"
    arg {
        name = "i"
        type = "integer"
    }
    returns = "integer"
    language = "plpgsql"
    body = <<-EOF
        BEGIN
            RETURN i + 1;
        END;
    EOF
}
```

To create a trigger function:

```hcl
resource "postgresql_function" "before_insert" {
    name = "before_insert"
    trigger = true
    body = <<-EOF
        BEGIN
            RETURN NEW;
        END;
    EOF
}
```

This defines the function. Attach it to a table separately with `CREATE TRIGGER`.

## Argument Reference

* `name` - (Required) The name of the function.

* `schema` - (Optional) The schema where the function is located.
  If not specified, the function is created in the current schema.

* `database` - (Optional) The database where the function is located.
  If not specified, the function is created in the current database.

* `trigger` - (Optional) Create a trigger function with return type `TRIGGER`. Default is false.
  Changing this value recreates the function. When true, `arg` blocks, an explicit `returns`,
  and `strict = true` are rejected. Trigger arguments are supplied by `CREATE TRIGGER`
  and accessed through `TG_ARGV` instead of function arguments.

* `arg` - (Optional) List of arguments for the function. Cannot be set when `trigger` is true.
  * `type` - (Required) The type of the argument.
  * `name` - (Optional) The name of the argument.
  * `mode` - (Optional) Can be one of IN, INOUT, OUT, or VARIADIC. Default is IN.
  * `default` - (Optional) An expression to be used as default value if the parameter is not specified.

* `returns` - (Optional) Type that the function returns. It can be computed from the OUT arguments. Default is void. When `trigger` is true, omit this attribute; the return type is automatically `TRIGGER`.

* `language` - (Optional) The function programming language. Can be one of internal, sql, c, plpgsql. Default is plpgsql.

* `parallel` - (Optional) Indicates if the function is parallel safe. Can be one of UNSAFE, RESTRICTED, or SAFE. Default is UNSAFE.

* `security_definer` - (Optional) If the function should execute with the permissions of the owner, rather than the permissions of the caller. Default is false.

* `strict` - (Optional) If the function should always return NULL when any of the inputs is NULL. Default is false. Cannot be true when `trigger` is true.

* `volatility` - (Optional) Defines the volatility of the function. Can be one of VOLATILE, STABLE, or IMMUTABLE. Default is VOLATILE.

* `body` - (Required) Function body.
  This should be the body content within the `AS $$` and the final `$$`. It will also accept the `AS $$` and `$$` if added.

* `drop_cascade` - (Optional) True to automatically drop objects that depend on the function (such as
  operators or triggers), and in turn all objects that depend on those objects. Default is false.

## Import

It is possible to import a `postgresql_function` resource with the following
command:

```
$ terraform import postgresql_function.function_foo "my_database.my_schema.my_function_name(arguments)"
```

Where `my_database` is the name of the database containing the schema,
`my_schema` is the name of the schema in the PostgreSQL database, `my_function_name` is the function name to be imported, `arguments` is the argument signature of the function including all non OUT types and
`postgresql_schema.function_foo` is the name of the resource whose state will be
populated as a result of the command.
