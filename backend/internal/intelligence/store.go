package intelligence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"time"
)

type Store interface {
	Configs(context.Context) ([]Config, error)
	Save(context.Context, Config) (Config, error)
	Claim(context.Context, int64, bool) (*Claim, error)
	Finish(context.Context, *Claim, Record) error
	History(context.Context, int64, time.Time, int64, int) ([]Record, error)
	Prune(context.Context) error
}

type SQLStore struct{ DB *sql.DB }

func (s *SQLStore) Configs(ctx context.Context) ([]Config, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT config, version FROM intelligence_check_configs ORDER BY group_id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	configs := []Config{}
	for rows.Next() {
		var raw []byte
		var version int
		if err = rows.Scan(&raw, &version); err != nil {
			return nil, err
		}
		var c Config
		if err = json.Unmarshal(raw, &c); err != nil {
			return nil, err
		}
		c.Version = version
		configs = append(configs, c)
	}
	return configs, rows.Err()
}

func (s *SQLStore) Save(ctx context.Context, c Config) (Config, error) {
	if err := c.Validate(); err != nil {
		return c, err
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return c, err
	}
	var version int
	// Config updates do not revoke an in-flight lease: avoid overlapping paid calls.
	// Existing run keeps its immutable configuration snapshot; next run uses new settings.
	err = s.DB.QueryRowContext(ctx, `INSERT INTO intelligence_check_configs (group_id,config,enabled,interval_minutes,timeout_seconds)
 SELECT $1,$2,$3,$4,$5 WHERE $6=0
 ON CONFLICT (group_id) DO NOTHING RETURNING version`, c.GroupID, raw, c.Enabled, c.IntervalMinutes, c.TimeoutSeconds, c.Version).Scan(&version)
	if errors.Is(err, sql.ErrNoRows) && c.Version > 0 {
		err = s.DB.QueryRowContext(ctx, `UPDATE intelligence_check_configs SET config=$2,enabled=$3,interval_minutes=$4,timeout_seconds=$5,version=version+1,next_run_at=NOW()+($4 * INTERVAL '1 minute'),updated_at=NOW() WHERE group_id=$1 AND version=$6 RETURNING version`, c.GroupID, raw, c.Enabled, c.IntervalMinutes, c.TimeoutSeconds, c.Version).Scan(&version)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return c, ErrConflict
	}
	c.Version = version
	return c, err
}

func (s *SQLStore) Claim(ctx context.Context, groupID int64, manual bool) (*Claim, error) {
	token := uuid.NewString()
	// Row locking + fencing protects multiple control instances and manual/timer races.
	row := s.DB.QueryRowContext(ctx, `UPDATE intelligence_check_configs SET lease_token=$2,lease_until=NOW()+((timeout_seconds+60)*INTERVAL '1 second'),next_run_at=NOW()+(interval_minutes*INTERVAL '1 minute')
 WHERE group_id=(SELECT group_id FROM intelligence_check_configs WHERE ($1=0 OR group_id=$1) AND ($3 OR (enabled AND next_run_at<=NOW())) AND (lease_until IS NULL OR lease_until<NOW()) ORDER BY next_run_at FOR UPDATE SKIP LOCKED LIMIT 1)
 RETURNING config,version`, groupID, token, manual)
	var raw []byte
	var version int
	if err := row.Scan(&raw, &version); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrConflict
		}
		return nil, err
	}
	var c Config
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, err
	}
	c.Version = version
	return &Claim{Config: c, Token: token}, nil
}

func (s *SQLStore) Finish(ctx context.Context, claim *Claim, r Record) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE intelligence_check_configs SET lease_token=NULL,lease_until=NULL WHERE group_id=$1 AND lease_token=$2`, claim.Config.GroupID, claim.Token)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrConflict
	}
	raw, err := json.Marshal(claim.Config)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO intelligence_check_runs(group_id,checked_at,duration_ms,status,answer,error,config_snapshot) VALUES($1,$2,$3,$4,$5,$6,$7)`, r.GroupID, r.CheckedAt, r.DurationMS, r.Status, r.Answer, r.Error, raw)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SQLStore) History(ctx context.Context, groupID int64, since time.Time, beforeID int64, limit int) ([]Record, error) {
	if limit < 1 || limit > 1000 {
		limit = 1000
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id,group_id,checked_at,duration_ms,status,answer,error,config_snapshot FROM intelligence_check_runs WHERE group_id=$1 AND checked_at>=$2 AND ($3=0 OR id<$3) ORDER BY id DESC LIMIT $4`, groupID, since, beforeID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	results := []Record{}
	for rows.Next() {
		var r Record
		var raw []byte
		if err = rows.Scan(&r.ID, &r.GroupID, &r.CheckedAt, &r.DurationMS, &r.Status, &r.Answer, &r.Error, &raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &r.Config); err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

func (s *SQLStore) Prune(ctx context.Context) error {
	for {
		result, err := s.DB.ExecContext(ctx, "DELETE FROM intelligence_check_runs WHERE id IN (SELECT id FROM intelligence_check_runs WHERE checked_at<NOW()-INTERVAL '7 days' LIMIT 1000)")
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if n < 1000 {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
}
