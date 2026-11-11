package sqlitestore

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/fullfran/training-mcp/internal/training"
)

func connectorStore(t *testing.T) (*Store, int64, int64) {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "training.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	a, err := store.ProvisionUser(context.Background(), "user_a")
	if err != nil {
		t.Fatal(err)
	}
	b, err := store.ProvisionUser(context.Background(), "user_b")
	if err != nil {
		t.Fatal(err)
	}
	return store, a, b
}

func notionConnector() training.Connector {
	return training.Connector{Slug: "notion", Label: "Notion", URL: "https://mcp.notion.com/mcp", Auth: training.ConnectorAuthOAuth, State: training.ConnectorDisconnected}
}

func TestConnectorsBelongToOnePerson(t *testing.T) {
	store, a, b := connectorStore(t)
	ctx := context.Background()
	created, err := store.CreateConnector(ctx, a, notionConnector())
	if err != nil {
		t.Fatal(err)
	}
	if created.ID == 0 || created.Slug != "notion" || created.State != training.ConnectorDisconnected || created.CreatedAt.IsZero() {
		t.Fatalf("created = %+v", created)
	}

	// The other person sees nothing and can touch nothing of it.
	if list, err := store.ListConnectors(ctx, b); err != nil || len(list) != 0 {
		t.Fatalf("other person's list = %v, %v", list, err)
	}
	if _, err := store.Connector(ctx, b, created.ID); !errors.Is(err, training.ErrNotFound) {
		t.Fatalf("other person's get = %v", err)
	}
	if _, err := store.ConnectorBySlug(ctx, b, "notion"); !errors.Is(err, training.ErrNotFound) {
		t.Fatalf("other person's slug lookup = %v", err)
	}
	if err := store.SetConnectorState(ctx, b, created.ID, training.ConnectorAuthBearer, training.ConnectorConnected); !errors.Is(err, training.ErrNotFound) {
		t.Fatalf("other person's state change = %v", err)
	}
	if err := store.DeleteConnector(ctx, b, created.ID); !errors.Is(err, training.ErrNotFound) {
		t.Fatalf("other person's delete = %v", err)
	}
	got, err := store.Connector(ctx, a, created.ID)
	if err != nil || got.State != training.ConnectorDisconnected || got.Auth != training.ConnectorAuthOAuth {
		t.Fatalf("owner's connector after the other's attempts = %+v, %v", got, err)
	}

	// The same slug is free for the other person.
	if _, err := store.CreateConnector(ctx, b, notionConnector()); err != nil {
		t.Fatalf("the other person's own notion: %v", err)
	}
	if _, err := store.CreateConnector(ctx, a, notionConnector()); !errors.Is(err, training.ErrConnectorExists) {
		t.Fatalf("duplicate slug = %v", err)
	}
}

func TestConnectorStateAndDelete(t *testing.T) {
	store, a, _ := connectorStore(t)
	ctx := context.Background()
	c, err := store.CreateConnector(ctx, a, notionConnector())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetConnectorState(ctx, a, c.ID, training.ConnectorAuthOAuth, training.ConnectorConnected); err != nil {
		t.Fatal(err)
	}
	got, err := store.ConnectorBySlug(ctx, a, "notion")
	if err != nil || got.State != training.ConnectorConnected || got.Auth != training.ConnectorAuthOAuth || got.URL != c.URL {
		t.Fatalf("after state change = %+v, %v", got, err)
	}
	if err := store.DeleteConnector(ctx, a, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Connector(ctx, a, c.ID); !errors.Is(err, training.ErrNotFound) {
		t.Fatalf("after delete = %v", err)
	}
	if err := store.DeleteConnector(ctx, a, c.ID); !errors.Is(err, training.ErrNotFound) {
		t.Fatalf("second delete = %v", err)
	}
}

func TestConnectorsAreCappedPerPerson(t *testing.T) {
	store, a, b := connectorStore(t)
	ctx := context.Background()
	for i := 0; i < training.MaxConnectors; i++ {
		c := notionConnector()
		c.Slug = "s" + string(rune('a'+i))
		if _, err := store.CreateConnector(ctx, a, c); err != nil {
			t.Fatal(err)
		}
	}
	c := notionConnector()
	c.Slug = "onemore"
	if _, err := store.CreateConnector(ctx, a, c); !errors.Is(err, training.ErrTooManyConnectors) {
		t.Fatalf("over the cap = %v", err)
	}
	if _, err := store.CreateConnector(ctx, b, c); err != nil {
		t.Fatalf("the other person is not capped by the first: %v", err)
	}
	list, err := store.ListConnectors(ctx, a)
	if err != nil || len(list) != training.MaxConnectors || list[0].Slug != "sa" {
		t.Fatalf("list = %d items, %v", len(list), err)
	}
}

// Production already ran 017 when 016 arrives: a missing lower version still applies.
func TestConnectorsMigrationAppliesAfterALaterOne(t *testing.T) {
	path := filepath.Join(t.TempDir(), "training.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`DROP TABLE connectors; DELETE FROM schema_migrations WHERE version=16`); err != nil {
		t.Fatal(err)
	}
	store.Close()
	again, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	var n int
	if err := again.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version IN (16,17)`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("versions = %d, %v", n, err)
	}
	if _, err := again.ListConnectors(context.Background(), 1); err != nil {
		t.Fatalf("connectors table after a late 016: %v", err)
	}
}
