package oracle

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/deeprajsshetty/observability-platform/platform/internal/dbengine"
	"github.com/deeprajsshetty/observability-platform/platform/internal/dbengine/enginetest"
)

func cdbFake() *enginetest.Fake {
	return &enginetest.Fake{Answers: []enginetest.Answer{
		{Contains: "FROM v$database d CROSS JOIN v$instance i", Rows: []dbengine.Row{{int64(1234567), "ERPCDB", "YES", "PRIMARY", "ARCHIVELOG", "ERPCDB1", "19.0.0.0.0", "dbhost1", "OPEN"}}},
		{Contains: "FROM v$pdbs", Rows: []dbengine.Row{{"ERPPDB", "READ WRITE", int64(3)}, {"HRPDB", "READ WRITE", int64(4)}}},
		{Contains: "cdb_tablespace_usage_metrics", Rows: []dbengine.Row{
			{"CDB$ROOT", "SYSTEM", float64(1e9), float64(4e9), float64(25), "PERMANENT"},
			{"ERPPDB", "ERP_DATA", "8000000000", "10000000000", "80.5", "PERMANENT"},
			{"HRPDB", "HR_DATA", int64(2e9), int64(1e10), float64(20), "PERMANENT"},
		}},
		{Contains: "SELECT cdb FROM v$database", Rows: []dbengine.Row{{"YES"}}},
		{Contains: "FROM v$session", Rows: []dbengine.Row{{int64(42), int64(7)}}},
		{Contains: "FROM v$sysmetric", Rows: []dbengine.Row{{"Buffer Cache Hit Ratio", float64(98.7)}, {"User Transaction Per Sec", float64(310.2)}, {"Host CPU Utilization (%)", float64(41)}}},
	}}
}

func TestInventoryCDB(t *testing.T) {
	inv, err := Engine{}.Inventory(context.Background(), cdbFake())
	if err != nil {
		t.Fatal(err)
	}
	if inv.Instance.ID != "1234567:erpcdb1" || inv.Instance.Version != "19.0.0.0.0" || inv.Instance.Properties["cdb"] != true {
		t.Fatalf("instance: %+v", inv.Instance)
	}
	if len(inv.Databases) != 2 || inv.Databases[0].Name != "ERPPDB" || inv.Databases[0].Properties["kind"] != "pdb" {
		t.Fatalf("pdbs: %+v", inv.Databases)
	}
	if len(inv.Tablespaces) != 3 || inv.Tablespaces[0].Database != "" || inv.Tablespaces[1].Database != "ERPPDB" {
		t.Fatalf("root tablespaces must belong to the instance, PDB ones to the PDB: %+v", inv.Tablespaces)
	}
}

func TestInventoryNonCDB(t *testing.T) {
	f := &enginetest.Fake{Answers: []enginetest.Answer{
		{Contains: "CROSS JOIN v$instance", Rows: []dbengine.Row{{int64(99), "LEGACY", "NO", "PRIMARY", "NOARCHIVELOG", "legacy", "12.2.0.1.0", "h", "OPEN"}}},
		{Contains: "dba_tablespace_usage_metrics", Rows: []dbengine.Row{{"LEGACY", "USERS", float64(1), float64(2), float64(50), "PERMANENT"}}},
	}}
	inv, err := Engine{}.Inventory(context.Background(), f)
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.Databases) != 1 || inv.Databases[0].Name != "LEGACY" || inv.Tablespaces[0].Database != "LEGACY" {
		t.Fatalf("non-CDB: the database itself is the only database: %+v", inv)
	}
	for _, q := range f.Seen {
		if strings.Contains(q, "v$pdbs") || strings.Contains(q, "cdb_") {
			t.Fatalf("non-CDB must not query CDB views: %s", q)
		}
	}
}

func TestSample(t *testing.T) {
	r, err := Engine{}.Sample(context.Background(), cdbFake())
	if err != nil {
		t.Fatal(err)
	}
	inst := r.Gauges[dbengine.ObjectRef{Kind: "instance"}]
	if inst["db.connections"] != 42 || inst["db.sessions.active"] != 7 || inst["db.cache.hit_ratio"] != 98.7 ||
		inst["db.transactions.rate"] != 310.2 || inst["cpu.utilization"] != 41 || inst["storage.used"] != 11e9 {
		t.Fatalf("instance gauges: %v", inst)
	}
	ts := r.Gauges[dbengine.ObjectRef{Kind: "tablespace", Database: "ERPPDB", Name: "ERP_DATA"}]
	if ts["db.storage.utilization"] != 80.5 || ts["storage.used"] != 8e9 {
		t.Fatalf("tablespace gauges: %v", ts)
	}
	if r.Gauges[dbengine.ObjectRef{Kind: "database", Name: "ERPPDB"}]["storage.used"] != 8e9 {
		t.Fatal("per-PDB storage must sum its tablespaces")
	}
	if len(r.Counters) != 0 {
		t.Fatal("Oracle v$sysmetric values are rates already; no counters expected")
	}
}

func TestErrorsPropagate(t *testing.T) {
	f := &enginetest.Fake{Answers: []enginetest.Answer{{Contains: "v$session", Err: errors.New("ORA-00942: table or view does not exist")}}}
	if _, err := (Engine{}).Sample(context.Background(), f); err == nil || !strings.Contains(err.Error(), "v$session") {
		t.Fatalf("missing privilege must surface with context: %v", err)
	}
}

func TestOpenDoesNotConnect(t *testing.T) {
	for _, mode := range []dbengine.TLSMode{dbengine.TLSVerifyFull, dbengine.TLSRequire, dbengine.TLSDisable} {
		db, err := Engine{}.Open(dbengine.Target{Host: "db.example", Database: "ORCLPDB1", Username: "c##mon", TLS: mode}, "pw", nil)
		if err != nil {
			t.Fatal(mode, err)
		}
		_ = db.Close()
	}
	if _, err := (Engine{}).Open(dbengine.Target{Host: "h", TLS: "bogus"}, "", nil); err == nil {
		t.Fatal("unknown TLS mode must be rejected")
	}
}
