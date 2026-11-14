package watch

import (
	"context"
	"sort"
	"time"

	"github.com/st3fan/masscanr/internal/runapi"
)

type source interface {
	Get(context.Context, string) (runapi.Run, error)
	Active(context.Context) ([]runapi.Run, error)
	Stop(context.Context, string) (runapi.Run, error)
}

type snapshot struct {
	Runs    []runapi.Run
	Last    *runapi.Run
	Updated time.Time
}

// Poll builds a complete snapshot before publishing it. A failed request leaves
// the previous snapshot untouched, including when discovery temporarily fails.
func poll(ctx context.Context, api source, id string, previous snapshot) (snapshot, error) {
	next := snapshot{Last: previous.Last}
	if id != "" {
		r, err := api.Get(ctx, id)
		if err != nil {
			return previous, err
		}
		next.Runs = []runapi.Run{r}
	} else {
		runs, err := api.Active(ctx)
		if err != nil {
			return previous, err
		}
		current := map[string]bool{}
		for _, r := range runs {
			current[r.ID] = true
			next.Runs = append(next.Runs, r)
		}
		for _, old := range previous.Runs {
			if current[old.ID] || !old.Active {
				continue
			}
			r, err := api.Get(ctx, old.ID)
			if err != nil {
				return previous, err
			}
			if r.Active {
				next.Runs = append(next.Runs, r)
			} else if next.Last == nil || !r.CreatedAt.Before(next.Last.CreatedAt) {
				v := r
				next.Last = &v
			}
		}
		if len(next.Runs) > 0 {
			next.Last = nil
		}
	}
	sort.Slice(next.Runs, func(i, j int) bool { return next.Runs[i].ID < next.Runs[j].ID })
	next.Updated = time.Now()
	return next, nil
}
func delay(interval time.Duration, failures int) time.Duration {
	if failures == 0 {
		return interval
	}
	d := interval
	for i := 0; i < min(failures, 10); i++ {
		d = min(30*time.Second, d*2)
	}
	return min(30*time.Second, d)
}
