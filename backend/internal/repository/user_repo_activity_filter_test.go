package repository

import (
	"testing"
	"time"

	dbuser "github.com/Wei-Shaw/sub2api/ent/user"
	"github.com/stretchr/testify/require"

	entsql "entgo.io/ent/dialect/sql"
)

func TestUserActivityFiltersRenderCorrelatedNotExistsQueries(t *testing.T) {
	tests := []struct {
		name      string
		predicate func(*entsql.Selector)
		wantArgs  int
	}{
		{
			name:      "inactive window",
			predicate: userHasNoUsageSince(time.Date(2026, 7, 7, 0, 0, 0, 0, time.UTC)),
			wantArgs:  1,
		},
		{
			name:      "never used",
			predicate: userHasNoUsage(),
			wantArgs:  0,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			users := entsql.Table(dbuser.Table)
			selector := entsql.Select(users.C(dbuser.FieldID)).From(users)
			test.predicate(selector)
			query, args := selector.Query()

			require.Contains(t, query, "NOT (EXISTS")
			require.Contains(t, query, "`usage_logs`.`user_id` = `users`.`id`")
			require.Len(t, args, test.wantArgs)
			if test.wantArgs == 1 {
				require.Contains(t, query, "`usage_logs`.`created_at` >= ?")
			}
		})
	}
}

func TestUserRechargeFilterRendersTotalRechargedPredicate(t *testing.T) {
	tests := []struct {
		name         string
		hasRecharged bool
		wantOperator string
	}{
		{name: "recharged", hasRecharged: true, wantOperator: ">"},
		{name: "not recharged", hasRecharged: false, wantOperator: "<="},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			users := entsql.Table(dbuser.Table)
			selector := entsql.Select(users.C(dbuser.FieldID)).From(users)
			userHasRecharged(test.hasRecharged)(selector)
			query, args := selector.Query()

			require.Contains(t, query, "`users`.`total_recharged` "+test.wantOperator+" ?")
			require.Equal(t, []any{float64(0)}, args)
		})
	}
}
