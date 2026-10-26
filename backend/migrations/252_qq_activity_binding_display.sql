-- Presentation settings for our QQ binding dialog; registered groups and rewards
-- remain independent. Empty group copy falls back to the registered group IDs.
ALTER TABLE qq_activity_config
    ADD COLUMN IF NOT EXISTS display_group_number TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS display_activity_title TEXT NOT NULL DEFAULT '新人体验与邀请奖励',
    ADD COLUMN IF NOT EXISTS display_activity_text TEXT NOT NULL DEFAULT E'符合条件的新人完成入群、绑定且 QQ 达到 16 级后，可领取 $2 体验额度，每人限领一次。\n真实受邀成员满足条件且新人体验额度实际到账后，邀请人才会获得 $2 邀请余额；邀请人本人不限等级。',
    ADD COLUMN IF NOT EXISTS display_binding_intro TEXT NOT NULL DEFAULT '注册 QQ 邮箱能匹配到群成员时会自动绑定，已有绑定继续有效。使用其他邮箱注册的，请在此验证本人 QQ 邮箱。',
    ADD COLUMN IF NOT EXISTS display_binding_note TEXT NOT NULL DEFAULT '每个 QQ 只能关联一个官网账号。注册邮箱保持不变，修改个人资料中的用户名不再用于绑定。';
