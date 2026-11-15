package testdb

import (
	"slices"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

type matchSample struct {
	ID      uint `gorm:"primaryKey"`
	Name    string
	Count   int
	Small   int16
	Seen    time.Time
	Gone    gorm.DeletedAt
	Payload []byte
	Tags    []string `gorm:"serializer:json"`
	Doc     string   `gorm:"type:jsonb"`
	Varchar string   `gorm:"type:varchar(64)"`
	Key     [16]byte
	Vec     string `gorm:"type:vector(3)"`
}

func TestAllowedTypesPerField(t *testing.T) {
	parsed, err := schema.Parse(&matchSample{}, &sync.Map{}, schema.NamingStrategy{})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"id": "bigint", "name": "text", "count": "bigint", "small": "smallint", "seen": "timestamp with time zone", "gone": "timestamp with time zone",
		"payload": "bytea", "tags": "jsonb", "doc": "jsonb", "varchar": "character varying",
		"key": "uuid", "vec": "USER-DEFINED",
	}
	for column, accepted := range want {
		field := parsed.FieldsByDBName[column]
		if field == nil {
			t.Fatalf("no field for %s", column)
		}
		if !slices.Contains(AllowedTypes(field), accepted) {
			t.Errorf("%s: %v does not accept %s", column, AllowedTypes(field), accepted)
		}
	}
	if slices.Contains(AllowedTypes(parsed.FieldsByDBName["count"]), "integer") {
		t.Error("an int field must not accept a 32-bit column")
	}
}

type matchTable struct {
	ID   uint
	Name string
	Mark time.Time
}

func (matchTable) TableName() string { return "match_table" }

func TestAssertModelsMatchAgainstPostgres(t *testing.T) {
	url, ok := URL()
	if !ok {
		t.Skip(SkipReason(""))
	}
	db := OpenIsolated(t, url, Config{Prefix: "testdb_match", Migrate: func(db *gorm.DB) error {
		return db.Exec(`CREATE TABLE match_table (id bigint, name text, mark timestamp with time zone)`).Error
	}})
	AssertModelsMatch(t, db, &matchTable{})

	recorder := &recordingT{TB: t}
	AssertModelsMatch(recorder, db, &matchSample{})
	if recorder.errors == 0 {
		t.Fatal("a model with no table must fail the assertion")
	}

	id, err := LockID(db)
	if err != nil || id <= 0 {
		t.Fatalf("LockID = %d, %v", id, err)
	}
}

func TestOpenIsolatedGivesEachCallItsOwnSchemaAndDropsIt(t *testing.T) {
	url, ok := URL()
	if !ok {
		t.Skip(SkipReason(""))
	}
	cfg := Config{Prefix: "testdb_iso", Migrate: func(db *gorm.DB) error {
		return db.Exec(`CREATE TABLE marker (id int)`).Error
	}}
	first := OpenIsolated(t, url, cfg)
	second := OpenIsolated(t, url, cfg)
	var one, two string
	first.Raw(`SELECT current_schema()`).Scan(&one)
	second.Raw(`SELECT current_schema()`).Scan(&two)
	if one == "" || one == two || len(one) > maxIdentifier {
		t.Fatalf("schemas %q and %q", one, two)
	}
}

type recordingT struct {
	testing.TB
	errors int
}

func (r *recordingT) Errorf(string, ...any) { r.errors++ }
