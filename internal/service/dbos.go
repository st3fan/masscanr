package service

import (
	"context"
	"github.com/dbos-inc/dbos-transact-golang/dbos"
)

// WorkflowVersion changes only with an intentional workflow migration.
const WorkflowVersion = "masscanr-v10-1"

func (s *Store) WorkflowContext(ctx context.Context) (dbos.Context, error) {
	return dbos.NewContext(ctx, dbos.Config{AppName: "masscanr", SystemDBPool: s.Pool, ApplicationVersion: WorkflowVersion, ExecutorID: "masscanr-single"})
}
