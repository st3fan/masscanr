package service

import (
	"context"
	"github.com/dbos-inc/dbos-transact-golang/dbos"
	"testing"
	"time"
)

func TestDBOSTransactionReplay(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if _, e := s.Pool.Exec(ctx, `CREATE TABLE masscanr.counter(n int);INSERT INTO masscanr.counter VALUES(0)`); e != nil {
		t.Fatal(e)
	}
	dc, e := s.WorkflowContext(ctx)
	if e != nil {
		t.Fatal(e)
	}
	ds, e := dbos.NewDataSource(dc, s.Pool)
	if e != nil {
		t.Fatal(e)
	}
	workflow := func(c dbos.Context, _ string) (int, error) {
		return dbos.RunAsTransaction(c, ds, func(ctx context.Context, tx dbos.Tx) (int, error) {
			var n int
			e := tx.QueryRow(ctx, `UPDATE masscanr.counter SET n=n+1 RETURNING n`).Scan(&n)
			return n, e
		})
	}
	dbos.RegisterWorkflow(dc, workflow, dbos.WithWorkflowName("transaction-fixture"))
	if e = dbos.Launch(dc); e != nil {
		t.Fatal(e)
	}
	defer dbos.Shutdown(dc, 5*time.Second)
	id := newID()
	for i := 0; i < 2; i++ {
		h, e := dbos.RunWorkflow(dc, workflow, "", dbos.WithWorkflowID(id))
		if e != nil {
			t.Fatal(e)
		}
		n, e := h.GetResult()
		if e != nil || n != 1 {
			t.Fatal(n, e)
		}
	}
	var n int
	if e = s.Pool.QueryRow(ctx, `SELECT n FROM masscanr.counter`).Scan(&n); e != nil || n != 1 {
		t.Fatal(n, e)
	}
}
