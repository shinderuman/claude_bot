package mastodon

const (
	// ProfileFieldSystemID is the key name for the peer authentication hash in the profile fields
	ProfileFieldSystemID = "SystemID"

	// ProfileFieldMentionStatus is the key name for the mention status in the profile fields
	ProfileFieldMentionStatus = "外部サーバーからのメンション受付"

	// ProfileFieldLastUpdated is the key name for the last updated time in the profile fields
	ProfileFieldLastUpdated = "プロフィール最終更新日時"

	// MentionStatusPublic indicates that the bot accepts mentions from remote users
	MentionStatusPublic = "公開中"

	// MentionStatusStopped indicates that the bot does not accept mentions from remote users
	MentionStatusStopped = "停止中"
)
