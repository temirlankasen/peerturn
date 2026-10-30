package store

import (
	"path/filepath"
	"reflect"
	"testing"
)

const migrationSchemaSnapshot = `SELECT type,name,tbl_name,sql FROM sqlite_schema
 WHERE name NOT LIKE 'sqlite_%' ORDER BY type,name`

func migrationRejectOpen(t *testing.T, path string) {
	t.Helper()
	s, err := Open(path, Limits{})
	if s != nil {
		if closeErr := s.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	}
	if err == nil {
		t.Error("Open admitted a database that must fail closed")
		return
	}
	if s != nil {
		t.Error("failed Open exposed a usable Store")
	}
	requireStatus(t, err, 503)
}

func TestRelaySchemaV2UnknownVersionFailsClosedWithoutChangingRecords(t *testing.T) {
	for _, version := range []struct {
		name   string
		value  int64
		pragma string
	}{
		{"negative", -1, `PRAGMA user_version=-1`},
		{"next", 3, `PRAGMA user_version=3`},
		{"future", 99, `PRAGMA user_version=99`},
	} {
		t.Run(version.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "unknown.db")
			_, before := migrationSeedLegacy(t, path)
			db := migrationSQL(t, path)
			migrationExec(t, db, version.pragma)
			schemaBefore := migrationRows(t, db, migrationSchemaSnapshot)
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			migrationRejectOpen(t, path)
			db = migrationSQL(t, path)
			migrationInt(t, db, `PRAGMA user_version`, version.value)
			if after := migrationSnapshot(t, db); !reflect.DeepEqual(before, after) {
				t.Fatal("rejected unknown version changed legacy records")
			}
			if after := migrationRows(t, db, migrationSchemaSnapshot); !reflect.DeepEqual(schemaBefore, after) {
				t.Fatal("rejected unknown version changed its schema")
			}
		})
	}
}

func TestRelaySchemaV2MigrationFailureRollsBackDDLAndData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "interrupted.db")
	_, before := migrationSeedLegacy(t, path)
	db := migrationSQL(t, path)
	// Occupy the final migration index name so preceding CREATE/ALTER steps
	// cannot commit independently when that late DDL statement fails.
	migrationExec(t, db, `CREATE TABLE invites_inviter_created(sentinel TEXT NOT NULL)`)
	migrationExec(t, db, `INSERT INTO invites_inviter_created VALUES('keep-existing-object')`)
	schemaBefore := migrationRows(t, db, migrationSchemaSnapshot)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	migrationRejectOpen(t, path)
	db = migrationSQL(t, path)
	migrationInt(t, db, `PRAGMA user_version`, 1)
	if after := migrationRows(t, db, migrationSchemaSnapshot); !reflect.DeepEqual(schemaBefore, after) {
		t.Fatalf("failed migration left partial DDL: before=%v, after=%v", schemaBefore, after)
	}
	if after := migrationSnapshot(t, db); !reflect.DeepEqual(before, after) {
		t.Fatal("failed migration changed legacy custody or authority")
	}
	migrationInt(t, db, `SELECT COUNT(*) FROM invites_inviter_created WHERE sentinel='keep-existing-object'`, 1)
	migrationExec(t, db, `DROP TABLE invites_inviter_created`)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	// Removing only the injected obstruction must permit a clean retry.
	s := migrationOpen(t, path)
	migrationInt(t, s.db, `PRAGMA user_version`, 2)
	if after := migrationSnapshot(t, s.db); !reflect.DeepEqual(before, after) {
		t.Fatal("migration retry changed legacy records")
	}
}
