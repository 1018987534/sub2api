package qqactivity

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/migrations"
)

func TestBindingDisplayValidation(t *testing.T) {
	for _, display := range []BindingDisplay{
		{GroupNumber: strings.Repeat("群", 201)},
		{ActivityTitle: strings.Repeat("题", 81)},
		{ActivityText: strings.Repeat("文", 4001)},
		{BindingIntro: strings.Repeat("说", 1001)},
		{BindingNote: strings.Repeat("明", 1001)},
		{ActivityText: "bad\x00text"},
		{ActivityTitle: string([]byte{0xff})},
	} {
		if err := NewStore(nil, nil).UpdateBindingDisplay(context.Background(), "fixture", display); !errors.Is(err, ErrBindingDisplay) {
			t.Fatalf("invalid display accepted: %v", err)
		}
	}
}

func TestPostgresBindingDisplay(t *testing.T) {
	db, s := autoBindingFixture(t)
	ctx := context.Background()
	user := autoAdd(t, db, "12345678@qq.com", "")
	unbound := autoAdd(t, db, "other@example.test", "")
	autoInitialize(t, s, "12345678")
	config, err := s.Config(ctx)
	if err != nil || config.BindingDisplay.GroupNumber != "87654321" || !strings.Contains(config.BindingDisplay.ActivityText, "$2") {
		t.Fatalf("registered group/default copy unavailable: %+v %v", config, err)
	}
	for _, id := range []int64{user, unbound} {
		status, err := s.BindingStatus(ctx, id)
		if err != nil || status.Config == nil || status.Config.BindingDisplay != config.BindingDisplay || status.Bound != (id == user) {
			t.Fatalf("binding response missing display: %+v %v", status, err)
		}
	}
	// A real pending row makes an accidental call to settle observable.
	if _, err := db.Exec(`INSERT INTO qq_activity_rewards(kind,recipient_qq,recipient_user_id,subject_qq,subject_user_id) VALUES('newcomer','12345678',$1,'12345678',$1)`, user); err != nil {
		t.Fatal(err)
	}
	display := BindingDisplay{GroupNumber: " 123456789 ", ActivityTitle: "自定义活动", ActivityText: "第一行\n第二行 <script>plain text</script>", BindingIntro: "自定义绑定说明", BindingNote: "自定义底部说明"}
	if err := s.UpdateBindingDisplay(ctx, "fixture-admin", display); err != nil {
		t.Fatal(err)
	}
	display.GroupNumber = "123456789"
	reloaded := NewStore(db, nil)
	status, err := reloaded.BindingStatus(ctx, unbound)
	if err != nil || status.Config == nil || status.Config.BindingDisplay != display {
		t.Fatalf("display did not persist on reload: %+v %v", status, err)
	}
	if status.Config.Status != config.Status || !status.Config.StartedAt.Equal(*config.StartedAt) || status.Config.NewcomerEnabled != config.NewcomerEnabled || status.Config.InvitationEnabled != config.InvitationEnabled || status.Config.AutoProcessJoin != config.AutoProcessJoin || status.Config.GroupMessagesEnabled != config.GroupMessagesEnabled {
		t.Fatal("display save changed campaign settings")
	}
	autoCount(t, db, `SELECT count(*) FROM qq_activity_groups WHERE group_id='87654321'`, 1)
	autoCount(t, db, `SELECT count(*) FROM qq_activity_groups WHERE group_id='123456789'`, 0)
	autoCount(t, db, `SELECT count(*) FROM qq_activity_rewards WHERE status='pending'`, 1)
	autoCount(t, db, `SELECT count(*) FROM qq_activity_rewards WHERE status='paid'`, 0)
	autoCount(t, db, `SELECT count(*) FROM users WHERE id=$1 AND balance=0`, 1, user)
	autoCount(t, db, `SELECT count(*) FROM qq_activity_audit WHERE action='binding_display' AND actor='fixture-admin'`, 1)
	migration, err := migrations.FS.ReadFile("252_qq_activity_binding_display.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(migration)); err != nil {
		t.Fatal(err)
	}
	config, err = reloaded.Config(ctx)
	if err != nil || config.BindingDisplay != display {
		t.Fatal("idempotent migration overwrote configured copy", err)
	}
	if err := s.UpdateBindingDisplay(ctx, "fixture-admin", BindingDisplay{}); err != nil {
		t.Fatal(err)
	}
	config, err = reloaded.Config(ctx)
	if err != nil || config.BindingDisplay.GroupNumber != "87654321" || config.BindingDisplay.ActivityText != "" || config.BindingDisplay.BindingIntro != "" {
		t.Fatal("empty copy/fallback group did not persist", err)
	}
	t.Log("display persisted for bound/unbound users; registered group retained; pending reward stayed pending; balance=0; migration repeat preserved copy")
}
