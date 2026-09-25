package intelligence

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestIntelligenceSaveCAS(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	s := SQLStore{DB: db}
	c := DefaultConfig(1)
	mock.ExpectQuery("INSERT INTO intelligence_check_configs").WithArgs(c.GroupID, sqlmock.AnyArg(), false, 10, 900, 0).WillReturnRows(sqlmock.NewRows([]string{"version"}).AddRow(1))
	saved, err := s.Save(context.Background(), c)
	require.NoError(t, err)
	require.Equal(t, 1, saved.Version)
	mock.ExpectQuery("INSERT INTO intelligence_check_configs").WillReturnError(sql.ErrNoRows)
	_, err = s.Save(context.Background(), c)
	require.ErrorIs(t, err, ErrConflict)
	c.Version = 1
	mock.ExpectQuery("INSERT INTO intelligence_check_configs").WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery("UPDATE intelligence_check_configs SET").WithArgs(c.GroupID, sqlmock.AnyArg(), false, 10, 900, 1).WillReturnRows(sqlmock.NewRows([]string{"version"}).AddRow(2))
	saved, err = s.Save(context.Background(), c)
	require.NoError(t, err)
	require.Equal(t, 2, saved.Version)
	mock.ExpectQuery("INSERT INTO intelligence_check_configs").WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery("UPDATE intelligence_check_configs SET").WillReturnError(sql.ErrNoRows)
	_, err = s.Save(context.Background(), c)
	require.ErrorIs(t, err, ErrConflict)
	require.NoError(t, mock.ExpectationsWereMet())
}
func TestIntelligenceClaimLeaseAndSnapshot(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	s := SQLStore{DB: db}
	c := DefaultConfig(3)
	raw, _ := json.Marshal(c)
	mock.ExpectQuery("UPDATE intelligence_check_configs SET lease_token=.*FOR UPDATE SKIP LOCKED LIMIT 1").
		WithArgs(int64(0), sqlmock.AnyArg(), false).WillReturnRows(sqlmock.NewRows([]string{"config", "version"}).AddRow(raw, 8))
	claim, err := s.Claim(context.Background(), 0, false)
	require.NoError(t, err)
	require.Equal(t, 8, claim.Config.Version)
	require.Equal(t, 10, claim.Config.IntervalMinutes)
	require.NotEmpty(t, claim.Token)
	mock.ExpectQuery("UPDATE intelligence_check_configs SET lease_token=").WithArgs(int64(3), sqlmock.AnyArg(), true).WillReturnError(sql.ErrNoRows)
	_, err = s.Claim(context.Background(), 3, true)
	require.ErrorIs(t, err, ErrConflict)
	require.NoError(t, mock.ExpectationsWereMet())
}
func TestIntelligenceFinishFencing(t *testing.T) {
	for _, owned := range []bool{true, false} {
		t.Run(map[bool]string{true: "owned", false: "stale"}[owned], func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			s := SQLStore{DB: db}
			claim := &Claim{Config: DefaultConfig(1), Token: "fence"}
			mock.ExpectBegin()
			rows := int64(0)
			if owned {
				rows = 1
			}
			mock.ExpectExec("UPDATE intelligence_check_configs SET lease_token=NULL").WithArgs(int64(1), "fence").WillReturnResult(sqlmock.NewResult(0, rows))
			if owned {
				mock.ExpectExec("INSERT INTO intelligence_check_runs").WithArgs(int64(1), sqlmock.AnyArg(), int64(1), "normal", "21", "", sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(1, 1))
				mock.ExpectCommit()
			} else {
				mock.ExpectRollback()
			}
			err = s.Finish(context.Background(), claim, Record{GroupID: 1, CheckedAt: time.Now(), DurationMS: 1, Status: "normal", Answer: "21"})
			if owned {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, ErrConflict)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
