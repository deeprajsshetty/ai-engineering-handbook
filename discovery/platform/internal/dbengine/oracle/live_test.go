package oracle

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"

	goora "github.com/sijms/go-ora/v2"

	"github.com/deeprajsshetty/observability-platform/platform/internal/dbengine"
)

// TestLiveCDB runs the engine against a real Oracle container database
// (scripts/dev-oracle.sh) as a common user holding only the documented
// grants (CREATE SESSION + SELECT_CATALOG_ROLE + CONTAINER_DATA=ALL),
// connected to the CDB root.
func TestLiveCDB(t *testing.T) {
	raw := os.Getenv("OBS_TEST_ORACLE_ADMIN_URL")
	if raw == "" {
		if os.Getenv("OBS_TEST_REQUIRE_ORACLE") == "1" {
			t.Fatal("OBS_TEST_REQUIRE_ORACLE=1 but OBS_TEST_ORACLE_ADMIN_URL is not set")
		}
		t.Skip("set OBS_TEST_ORACLE_ADMIN_URL to run against a live Oracle")
	}
	ctx := context.Background()
	u, _ := url.Parse(raw)
	u.Path = "/FREE" // CDB root service of Oracle Database Free
	admin := sql.OpenDB(goora.NewConnector(u.String()))
	defer admin.Close()
	b := make([]byte, 5)
	_, _ = rand.Read(b)
	user := "C##OBS_MON_" + strings.ToUpper(hex.EncodeToString(b))
	pw := "Mon" + hex.EncodeToString(b) + "z7"
	for _, s := range []string{
		`CREATE USER ` + user + ` IDENTIFIED BY "` + pw + `" CONTAINER=ALL`,
		`GRANT CREATE SESSION, SELECT_CATALOG_ROLE TO ` + user + ` CONTAINER=ALL`,
		// Without this, V$PDBS and CDB_* views show only the root container.
		`ALTER USER ` + user + ` SET CONTAINER_DATA=ALL CONTAINER=CURRENT`,
	} {
		if _, err := admin.ExecContext(ctx, s); err != nil {
			t.Fatalf("creating monitoring user: %v", err)
		}
	}
	t.Cleanup(func() { _, _ = admin.ExecContext(context.Background(), `DROP USER `+user+` CASCADE`) })

	port, _ := strconv.Atoi(u.Port())
	var d net.Dialer
	db, err := Engine{}.Open(dbengine.Target{Host: u.Hostname(), Port: port, Database: "FREE", Username: user, TLS: dbengine.TLSDisable}, pw, d.DialContext)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	q := dbengine.SQLQuerier{DB: db}
	inv, err := Engine{}.Inventory(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if inv.Instance.ID == "" || inv.Instance.Properties["cdb"] != true || !strings.HasPrefix(inv.Instance.Version, "23") {
		t.Fatalf("instance: %+v", inv.Instance)
	}
	pdbs := map[string]bool{}
	for _, p := range inv.Databases {
		pdbs[p.Name] = true
	}
	if !pdbs["FREEPDB1"] || pdbs["PDB$SEED"] {
		t.Fatalf("PDBs: %v", pdbs)
	}
	rootTS, pdbTS := 0, 0
	for _, ts := range inv.Tablespaces {
		if ts.Database == "" {
			rootTS++
		} else if ts.Database == "FREEPDB1" {
			pdbTS++
		}
	}
	if rootTS == 0 || pdbTS == 0 {
		t.Fatalf("tablespaces: root=%d pdb=%d (%+v)", rootTS, pdbTS, inv.Tablespaces)
	}
	r, err := Engine{}.Sample(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	g := r.Gauges[dbengine.ObjectRef{Kind: "instance"}]
	for _, m := range []string{"db.connections", "db.sessions.active", "db.cache.hit_ratio", "storage.used"} {
		if _, ok := g[m]; !ok {
			t.Errorf("instance gauge %s missing: %v", m, g)
		}
	}
	t.Logf("live Oracle %s: %d PDBs, %d tablespaces, instance gauges %v", inv.Instance.Version, len(inv.Databases), len(inv.Tablespaces), g)
}
