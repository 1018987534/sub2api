package qqactivity

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"unicode/utf8"
)

var ErrBindingDisplay = errors.New("请检查展示文案：群号最多 200 字，标题最多 80 字，活动说明最多 4000 字，绑定说明各最多 1000 字。")

// BindingDisplay is plain presentation copy. It never changes registered groups
// or reward eligibility, and saving it does not trigger reward settlement.
type BindingDisplay struct {
	GroupNumber   string `json:"group_number"`
	ActivityTitle string `json:"activity_title"`
	ActivityText  string `json:"activity_text"`
	BindingIntro  string `json:"binding_intro"`
	BindingNote   string `json:"binding_note"`
}

func (s *Store) UpdateBindingDisplay(ctx context.Context, actor string, display BindingDisplay) error {
	fields := []struct {
		value *string
		limit int
	}{{&display.GroupNumber, 200}, {&display.ActivityTitle, 80}, {&display.ActivityText, 4000}, {&display.BindingIntro, 1000}, {&display.BindingNote, 1000}}
	for _, field := range fields {
		*field.value = strings.TrimSpace(*field.value)
		if !utf8.ValidString(*field.value) || strings.ContainsRune(*field.value, 0) || utf8.RuneCountInString(*field.value) > field.limit {
			return ErrBindingDisplay
		}
	}
	return s.transaction(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE qq_activity_config SET display_group_number=$1,display_activity_title=$2,display_activity_text=$3,display_binding_intro=$4,display_binding_note=$5,updated_at=NOW() WHERE id=1`, display.GroupNumber, display.ActivityTitle, display.ActivityText, display.BindingIntro, display.BindingNote)
		if err != nil {
			return err
		}
		return audit(ctx, tx, actor, "binding_display", "", display)
	})
}
