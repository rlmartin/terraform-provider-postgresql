package postgresql

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/validation"
	"github.com/lib/pq"
)

const (
	triggerNameAttr          = "name"
	triggerWhenAttr          = "when"
	triggerEventAttr         = "event"
	triggerUpdateColumnsAttr = "update_columns"
	triggerTableNameAttr     = "table_name"
	triggerLevelAttr         = "level"
	triggerConditionAttr     = "condition"
	triggerFunctionNameAttr  = "function_name"
	triggerDatabaseAttr      = "database"
	internalPGConditionAttr  = "internal_pg_condition"
)

func resourcePostgreSQLTrigger() *schema.Resource {
	return &schema.Resource{
		Create: PGResourceFunc(resourcePostgreSQLTriggerCreate),
		Read:   PGResourceFunc(resourcePostgreSQLTriggerRead),
		Update: PGResourceFunc(resourcePostgreSQLTriggerUpdate),
		Delete: PGResourceFunc(resourcePostgreSQLTriggerDelete),
		Importer: &schema.ResourceImporter{
			StateContext: schema.ImportStatePassthroughContext,
		},
		CustomizeDiff: func(_ context.Context, d *schema.ResourceDiff, _ interface{}) error {
			for _, attr := range []string{triggerWhenAttr, triggerEventAttr, triggerUpdateColumnsAttr, triggerLevelAttr, triggerConditionAttr} {
				if !d.NewValueKnown(attr) {
					return nil
				}
			}
			return validateTrigger(d.Get)
		},
		Schema: map[string]*schema.Schema{
			triggerNameAttr: {
				Type: schema.TypeString, Required: true, ForceNew: true,
				ValidateFunc: validateTriggerLiteralName,
				Description:  "The trigger name, unqualified by schema. Unique within its table.",
			},
			triggerDatabaseAttr: {
				Type: schema.TypeString, Optional: true, Computed: true, ForceNew: true,
				Description: "Database containing the trigger. Defaults to the provider database.",
			},
			triggerWhenAttr: {
				Type: schema.TypeString, Required: true,
				ValidateFunc: validation.StringInSlice([]string{"BEFORE", "AFTER", "INSTEAD OF"}, false),
				Description:  "When to fire the trigger: BEFORE, AFTER, or INSTEAD OF.",
			},
			triggerEventAttr: {
				Type: schema.TypeList, Required: true, MinItems: 1, MaxItems: 4,
				Elem:        &schema.Schema{Type: schema.TypeString, ValidateFunc: validation.StringInSlice([]string{"INSERT", "UPDATE", "DELETE", "TRUNCATE"}, false)},
				Description: "Events that fire the trigger: INSERT, UPDATE, DELETE, or TRUNCATE.",
			},
			triggerUpdateColumnsAttr: {
				Type: schema.TypeList, Optional: true,
				Elem:        &schema.Schema{Type: schema.TypeString, ValidateFunc: validateTriggerLiteralName},
				Description: "Columns for UPDATE OF. Requires UPDATE in event and is not supported for INSTEAD OF triggers.",
			},
			triggerTableNameAttr: {
				Type: schema.TypeString, Required: true, ForceNew: true,
				ValidateFunc: validateTriggerQualifiedName,
				Description:  "Table or view name, optionally schema-qualified.",
			},
			triggerLevelAttr: {
				Type: schema.TypeString, Optional: true, Default: "STATEMENT",
				ValidateFunc: validation.StringInSlice([]string{"STATEMENT", "ROW"}, false),
				Description:  "Fire once per STATEMENT (default) or per ROW.",
			},
			triggerConditionAttr: {
				Type: schema.TypeString, Optional: true, Default: "",
				Description: "Boolean WHEN expression, without the WHEN keyword. Subject to trigger event and timing restrictions.",
				DiffSuppressFunc: func(_ string, old, new string, _ *schema.ResourceData) bool {
					return triggerConditionsEqual(old, new)
				},
			},
			triggerFunctionNameAttr: {
				Type: schema.TypeString, Required: true,
				ValidateFunc: validateTriggerQualifiedName,
				Description:  "Function to execute, optionally schema-qualified, without parentheses. Must take no arguments and return trigger.",
			},
			internalPGConditionAttr: {
				Type: schema.TypeString, Computed: true,
			},
		},
	}
}

func triggerStrings(value interface{}) []string {
	values := value.([]interface{})
	result := make([]string, len(values))
	for i, item := range values {
		result[i] = item.(string)
	}
	return result
}

func validateTrigger(get func(string) interface{}) error {
	when, forEach := get(triggerWhenAttr).(string), get(triggerLevelAttr).(string)
	events := triggerStrings(get(triggerEventAttr))
	columns := triggerStrings(get(triggerUpdateColumnsAttr))
	for _, attr := range []string{triggerEventAttr, triggerUpdateColumnsAttr} {
		if duplicate, unique := isUniqueArr(get(attr).([]interface{})); !unique {
			return fmt.Errorf("%s contains duplicate value %q", attr, duplicate)
		}
	}
	if len(columns) > 0 && !sliceContainsStr(events, "UPDATE") {
		return fmt.Errorf("update_columns requires UPDATE in event")
	}
	if when == "INSTEAD OF" {
		if forEach != "ROW" {
			return fmt.Errorf("INSTEAD OF triggers require level = ROW")
		}
		if len(columns) > 0 {
			return fmt.Errorf("update_columns is not supported for INSTEAD OF triggers")
		}
	}
	if forEach == "ROW" && sliceContainsStr(events, "TRUNCATE") {
		return fmt.Errorf("TRUNCATE requires level = STATEMENT")
	}
	return validateTriggerCondition(get(triggerConditionAttr).(string), when, forEach, events)
}

// Accept one or more SQL identifiers separated by dots, including quoted names
// with dots or escaped quotes. Unquoted names retain case, like other resources.
func parseTriggerName(input string, maxParts int) ([]string, error) {
	tokens, err := triggerSQLTokens(input)
	if err != nil {
		return nil, err
	}
	var parts []string
	for i, token := range tokens {
		if i%2 == 1 {
			if token.text != "." {
				return nil, fmt.Errorf("expected a dot in identifier %q", input)
			}
			continue
		}
		if token.kind != 'i' && token.kind != 'q' {
			return nil, fmt.Errorf("invalid identifier %q", input)
		}
		part := token.text
		if token.kind == 'q' {
			part = strings.ReplaceAll(part[1:len(part)-1], `""`, `"`)
		}
		if part == "" || len(part) > 63 || strings.IndexByte(part, 0) >= 0 {
			return nil, fmt.Errorf("invalid identifier component in %q", input)
		}
		parts = append(parts, part)
	}
	if len(tokens)%2 == 0 || len(parts) > maxParts {
		return nil, fmt.Errorf("expected between 1 and %d identifier components in %q", maxParts, input)
	}
	return parts, nil
}

func validateTriggerLiteralName(value interface{}, key string) ([]string, []error) {
	name := value.(string)
	if strings.TrimSpace(name) == "" || len(name) > 63 || strings.IndexByte(name, 0) >= 0 {
		return nil, []error{fmt.Errorf("%s must be a nonempty identifier of at most 63 bytes without NUL characters", key)}
	}
	return nil, nil
}

func validateTriggerQualifiedName(value interface{}, key string) ([]string, []error) {
	if _, err := parseTriggerName(value.(string), 2); err != nil {
		return nil, []error{fmt.Errorf("%s: %w", key, err)}
	}
	return nil, nil
}

func quoteTriggerName(parts []string) string {
	quoted := make([]string, len(parts))
	for i, part := range parts {
		quoted[i] = pq.QuoteIdentifier(part)
	}
	return strings.Join(quoted, ".")
}

func triggerID(database, tableSchema, table, name string) string {
	parts := []string{database, tableSchema, table, name}
	for i, part := range parts {
		parts[i] = quoteIdentifier(part)
	}
	return strings.Join(parts, ".")
}

func parseTriggerID(id string) ([]string, error) {
	parts, err := parseTriggerName(id, 4)
	if err != nil || len(parts) != 4 {
		return nil, fmt.Errorf("invalid trigger ID %q: expected database.schema.table.name", id)
	}
	return parts, nil
}

func checkTriggerVersion(db *DBConnection) error {
	if !db.featureSupported(featureTrigger) {
		return fmt.Errorf("postgresql_trigger requires PostgreSQL 11 or later (server version: %s)", db.version)
	}
	return nil
}

func resourcePostgreSQLTriggerCreate(db *DBConnection, d *schema.ResourceData) error {
	return writeTrigger(db, d, false)
}

func resourcePostgreSQLTriggerUpdate(db *DBConnection, d *schema.ResourceData) error {
	return writeTrigger(db, d, true)
}

func writeTrigger(db *DBConnection, d *schema.ResourceData, replace bool) error {
	if err := checkTriggerVersion(db); err != nil {
		return err
	}
	if err := validateTrigger(d.Get); err != nil {
		return err
	}
	table, err := parseTriggerName(d.Get(triggerTableNameAttr).(string), 2)
	if err != nil {
		return err
	}
	function, err := parseTriggerName(d.Get(triggerFunctionNameAttr).(string), 2)
	if err != nil {
		return err
	}
	txn, err := startTransaction(db.client, getDatabase(d, db.client.databaseName))
	if err != nil {
		return err
	}
	defer deferredRollback(txn)

	var database, tableSchema, tableName, relationKind string
	if err := txn.QueryRow(`SELECT current_database(), n.nspname, c.relname, c.relkind
		FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE c.oid = $1::regclass`, quoteTriggerName(table)).Scan(&database, &tableSchema, &tableName, &relationKind); err != nil {
		return fmt.Errorf("error finding trigger table: %w", err)
	}
	when, forEach := d.Get(triggerWhenAttr).(string), d.Get(triggerLevelAttr).(string)
	if when == "INSTEAD OF" && relationKind != "v" {
		return fmt.Errorf("INSTEAD OF triggers can only be created on views")
	}
	if relationKind == "v" && (forEach == "ROW" && when != "INSTEAD OF" || sliceContainsStr(triggerStrings(d.Get(triggerEventAttr)), "TRUNCATE")) {
		return fmt.Errorf("views require INSTEAD OF for ROW triggers and do not support TRUNCATE triggers")
	}
	var validFunction bool
	if err := txn.QueryRow(`SELECT prorettype = 'pg_catalog.trigger'::regtype AND pronargs = 0
		FROM pg_proc WHERE oid = $1::regprocedure`, quoteTriggerName(function)+"()").Scan(&validFunction); err != nil {
		return fmt.Errorf("error finding trigger function: %w", err)
	}
	if !validFunction {
		return fmt.Errorf("function_name must refer to a function taking no arguments and returning trigger")
	}

	name := d.Get(triggerNameAttr).(string)
	qualifiedTable := quoteTriggerName([]string{tableSchema, tableName})
	if replace {
		if _, err := txn.Exec(fmt.Sprintf("DROP TRIGGER %s ON %s", pq.QuoteIdentifier(name), qualifiedTable)); err != nil {
			return fmt.Errorf("error replacing trigger: %w", err)
		}
	}
	events := triggerStrings(d.Get(triggerEventAttr))
	columns := triggerStrings(d.Get(triggerUpdateColumnsAttr))
	for i, event := range events {
		if event == "UPDATE" && len(columns) > 0 {
			quoted := make([]string, len(columns))
			for j, column := range columns {
				quoted[j] = pq.QuoteIdentifier(column)
			}
			events[i] += " OF " + strings.Join(quoted, ", ")
		}
	}
	statement := fmt.Sprintf("CREATE TRIGGER %s %s %s ON %s FOR EACH %s", pq.QuoteIdentifier(name), when, strings.Join(events, " OR "), qualifiedTable, forEach)
	if condition := strings.TrimSpace(d.Get(triggerConditionAttr).(string)); condition != "" {
		statement += " WHEN (\n" + condition + "\n)"
	}
	statement += " EXECUTE FUNCTION " + quoteTriggerName(function) + "()"
	if _, err := txn.Exec(statement); err != nil {
		return fmt.Errorf("error creating trigger: %w", err)
	}
	if err := txn.Commit(); err != nil {
		return err
	}
	d.SetId(triggerID(database, tableSchema, tableName, name))
	return readTrigger(db, d, true)
}

func resourcePostgreSQLTriggerRead(db *DBConnection, d *schema.ResourceData) error {
	return readTrigger(db, d, false)
}

// Keep configured ordering for lists whose SQL meaning is order-independent.
func triggerReadList(configured interface{}, actual []string) []string {
	previous := triggerStrings(configured)
	if len(previous) == len(actual) {
		equal := true
		for _, value := range actual {
			if !sliceContainsStr(previous, value) {
				equal = false
				break
			}
		}
		if equal {
			return previous
		}
	}
	return actual
}

func triggerReadName(configured, namespace, name string) string {
	parts, err := parseTriggerName(configured, 2)
	if err == nil && parts[len(parts)-1] == name && (len(parts) == 1 || parts[0] == namespace) {
		return configured
	}
	return quoteIdentifier(namespace) + "." + quoteIdentifier(name)
}

func readTrigger(db *DBConnection, d *schema.ResourceData, written bool) error {
	if err := checkTriggerVersion(db); err != nil {
		return err
	}
	parts, err := parseTriggerID(d.Id())
	if err != nil {
		return err
	}
	txn, err := startTransaction(db.client, parts[0])
	if err != nil {
		return err
	}
	defer deferredRollback(txn)

	var kind int
	var definition, functionSchema, functionName string
	var columns []string
	var unsupported bool
	err = txn.QueryRow(`SELECT t.tgtype, pg_get_triggerdef(t.oid, true), pn.nspname, p.proname,
		ARRAY(SELECT a.attname FROM unnest(t.tgattr::smallint[]) WITH ORDINALITY AS col(num, pos)
			JOIN pg_attribute a ON a.attrelid = t.tgrelid AND a.attnum = col.num ORDER BY col.pos),
		t.tgconstraint <> 0 OR t.tgnargs <> 0 OR t.tgoldtable IS NOT NULL OR t.tgnewtable IS NOT NULL
		FROM pg_trigger t JOIN pg_class c ON c.oid = t.tgrelid
		JOIN pg_namespace n ON n.oid = c.relnamespace
		JOIN pg_proc p ON p.oid = t.tgfoid JOIN pg_namespace pn ON pn.oid = p.pronamespace
		WHERE n.nspname = $1 AND c.relname = $2 AND t.tgname = $3 AND NOT t.tgisinternal`,
		parts[1], parts[2], parts[3]).Scan(&kind, &definition, &functionSchema, &functionName, pq.Array(&columns), &unsupported)
	if err == sql.ErrNoRows {
		d.SetId("")
		return nil
	}
	if err != nil {
		return fmt.Errorf("error reading trigger: %w", err)
	}
	if unsupported {
		return fmt.Errorf("constraint triggers, transition relations, and trigger function arguments are not supported by postgresql_trigger")
	}
	condition, err := triggerConditionFromDefinition(definition)
	if err != nil {
		return err
	}
	canonicalCondition := condition
	configuredCondition := d.Get(triggerConditionAttr).(string)
	if written || condition == d.Get(internalPGConditionAttr).(string) || triggerConditionsEqual(condition, configuredCondition) {
		condition = configuredCondition
	}
	// pg_trigger.tgtype encodes level, timing, and events as bit flags.
	when, forEach := "AFTER", "STATEMENT"
	if kind&2 != 0 {
		when = "BEFORE"
	}
	if kind&64 != 0 {
		when = "INSTEAD OF"
	}
	if kind&1 != 0 {
		forEach = "ROW"
	}
	var events []string
	for i, event := range []string{"INSERT", "DELETE", "UPDATE", "TRUNCATE"} {
		if kind&(1<<uint(i+2)) != 0 {
			events = append(events, event)
		}
	}
	values := map[string]interface{}{
		triggerDatabaseAttr: parts[0], triggerNameAttr: parts[3],
		triggerWhenAttr: when, triggerLevelAttr: forEach,
		triggerEventAttr:         triggerReadList(d.Get(triggerEventAttr), events),
		triggerUpdateColumnsAttr: triggerReadList(d.Get(triggerUpdateColumnsAttr), columns),
		triggerTableNameAttr:     triggerReadName(d.Get(triggerTableNameAttr).(string), parts[1], parts[2]),
		triggerFunctionNameAttr:  triggerReadName(d.Get(triggerFunctionNameAttr).(string), functionSchema, functionName),
		triggerConditionAttr:     condition, internalPGConditionAttr: canonicalCondition,
	}
	for key, value := range values {
		if err := d.Set(key, value); err != nil {
			return fmt.Errorf("error setting trigger %s: %w", key, err)
		}
	}
	return txn.Commit()
}

func resourcePostgreSQLTriggerDelete(db *DBConnection, d *schema.ResourceData) error {
	if err := checkTriggerVersion(db); err != nil {
		return err
	}
	parts, err := parseTriggerID(d.Id())
	if err != nil {
		return err
	}
	txn, err := startTransaction(db.client, parts[0])
	if err != nil {
		return err
	}
	defer deferredRollback(txn)
	var exists bool
	if err := txn.QueryRow(`SELECT EXISTS (SELECT 1 FROM pg_trigger t
		JOIN pg_class c ON c.oid = t.tgrelid JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = $1 AND c.relname = $2 AND t.tgname = $3 AND NOT t.tgisinternal)`, parts[1], parts[2], parts[3]).Scan(&exists); err != nil {
		return err
	}
	if exists {
		if _, err := txn.Exec(fmt.Sprintf("DROP TRIGGER %s ON %s", pq.QuoteIdentifier(parts[3]), quoteTriggerName(parts[1:3]))); err != nil {
			return fmt.Errorf("error deleting trigger: %w", err)
		}
	}
	if err := txn.Commit(); err != nil {
		return err
	}
	d.SetId("")
	return nil
}
