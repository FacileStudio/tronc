package testdb

import (
	"reflect"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"

	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

// MatchOptions tunes AssertModelsMatchWith.
type MatchOptions struct {
	// Extra returns column types to accept for a field on top of the ones
	// AssertModelsMatch derives, for a custom type the defaults cannot know
	// about (a pgvector field, a Postgres enum). Nil adds nothing.
	Extra func(*schema.Field) []string
}

// AssertModelsMatch fails the test when a GORM model reads or writes a column
// the migrated schema lacks, or holds one in a type the Go field cannot carry.
// db must be scoped to the schema the migrations built (the handle Open
// returns). Pass every model the app queries.
//
// AutoMigrate no longer adds a missing column, so a field added to a struct
// without a numbered migration fails here, in the gate, instead of at query
// time in production.
//
//	func TestModelsMatchTheMigratedSchema(t *testing.T) {
//		testdb.AssertModelsMatch(t, requireDB(t), &User{}, &Space{})
//	}
func AssertModelsMatch(t testing.TB, db *gorm.DB, models ...any) {
	t.Helper()
	AssertModelsMatchWith(t, db, MatchOptions{}, models...)
}

// AssertModelsMatchWith is AssertModelsMatch with options.
func AssertModelsMatchWith(t testing.TB, db *gorm.DB, opts MatchOptions, models ...any) {
	t.Helper()
	for _, model := range models {
		parsed, err := schema.Parse(model, &sync.Map{}, db.NamingStrategy)
		if err != nil {
			t.Fatalf("testdb: parse %T: %v", model, err)
		}
		columns := columnTypes(t, db, parsed.Table)
		if len(columns) == 0 {
			t.Errorf("%T: table %s does not exist", model, parsed.Table)
			continue
		}
		for _, field := range parsed.Fields {
			checkField(t, parsed.Table, field, columns, opts)
		}
	}
}

func checkField(t testing.TB, table string, field *schema.Field, columns map[string]string, opts MatchOptions) {
	t.Helper()
	if field.DBName == "" {
		return
	}
	got, ok := columns[field.DBName]
	if !ok {
		t.Errorf("%s.%s: no such column", table, field.DBName)
		return
	}
	want := AllowedTypes(field)
	if opts.Extra != nil {
		want = append(want, opts.Extra(field)...)
	}
	if !slices.Contains(want, got) {
		t.Errorf("%s.%s: column is %s, field %s needs one of %v", table, field.DBName, got, field.FieldType, want)
	}
}

func columnTypes(t testing.TB, db *gorm.DB, table string) map[string]string {
	t.Helper()
	var rows []struct {
		ColumnName string
		DataType   string
	}
	err := db.Raw(`SELECT column_name, data_type FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name = ?`, table).Scan(&rows).Error
	if err != nil {
		t.Fatalf("testdb: read the columns of %s: %v", table, err)
	}
	columns := map[string]string{}
	for _, row := range rows {
		columns[row.ColumnName] = row.DataType
	}
	return columns
}

var parenthesized = regexp.MustCompile(`\([^)]*\)`)

var typeAliases = map[string]string{
	"varchar": "character varying", "char": "character", "timestamptz": "timestamp with time zone",
	"timestamp": "timestamp without time zone", "int": "integer", "int4": "integer", "int2": "smallint",
	"int8": "bigint", "serial": "integer", "bigserial": "bigint", "bool": "boolean", "float8": "double precision",
	"float4": "real", "decimal": "numeric", "vector": "USER-DEFINED",
}

func tagType(tag string) string {
	name := strings.TrimSpace(parenthesized.ReplaceAllString(strings.ToLower(tag), ""))
	if strings.HasSuffix(name, "[]") {
		return "ARRAY"
	}
	if alias, ok := typeAliases[name]; ok {
		return alias
	}
	return name
}

// AllowedTypes lists the information_schema data types a column may have to
// hold field. An explicit `gorm:"type:..."` tag decides alone; otherwise the
// Go type does. A [16]byte is a uuid, a serializer field accepts jsonb, json,
// text and bytea, and a slice, map or struct with no scalar mapping accepts
// jsonb, json and bytea.
func AllowedTypes(field *schema.Field) []string {
	if tag := field.TagSettings["TYPE"]; tag != "" {
		return []string{tagType(tag)}
	}
	if kind := field.FieldType; kind.Kind() == reflect.Array && kind.Len() == 16 {
		return []string{"uuid"}
	}
	if field.Serializer != nil {
		return []string{"jsonb", "json", "text", "bytea"}
	}
	switch field.DataType {
	case schema.Bool:
		return []string{"boolean"}
	case schema.Int, schema.Uint:
		return integerTypes(field.Size)
	case schema.Float:
		return []string{"numeric", "double precision", "real"}
	case schema.String:
		return []string{"text", "character varying", "uuid"}
	case schema.Time:
		return []string{"timestamp with time zone"}
	case schema.Bytes:
		return []string{"bytea"}
	}
	return []string{"jsonb", "json", "bytea"}
}

func integerTypes(size int) []string {
	switch {
	case size > 0 && size <= 16:
		return []string{"smallint", "integer", "bigint"}
	case size > 0 && size <= 32:
		return []string{"integer", "bigint"}
	}
	return []string{"bigint"}
}
