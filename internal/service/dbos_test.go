package service

import (
	"context"
	"testing"
	"time"

	"github.com/dbos-inc/dbos-transact-golang/dbos"
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

func TestServiceLockDoesNotBlockWorkflowShutdown(t *testing.T) {
	s := testStore(t)
	ctx := t.Context()
	lock, err := acquireServiceLock(ctx, s.Pool)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.Close(context.Background()) })
	if other, err := acquireServiceLock(ctx, s.Pool); err == nil {
		_ = other.Close(context.Background())
		t.Fatal("second service acquired the database lock")
	}
	dc, err := s.WorkflowContext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err = dbos.Launch(dc); err != nil {
		t.Fatal(err)
	}
	if err = dbos.Shutdown(dc, 5*time.Second); err != nil {
		t.Fatalf("workflow shutdown waited for the service lock: %v", err)
	}
	if other, err := acquireServiceLock(ctx, s.Pool); err == nil {
		_ = other.Close(context.Background())
		t.Fatal("service lock was released before workflow shutdown")
	}
	if err = lock.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	other, err := acquireServiceLock(ctx, s.Pool)
	if err != nil {
		t.Fatalf("service lock was not released after connection close: %v", err)
	}
	_ = other.Close(context.Background())
}
