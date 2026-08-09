// Package domain contains the CLI's stable model. External flomo API wire
// formats must be mapped into these types by an adapter before entering the
// service or storage layers.
package domain

import "encoding/json"

type LoginResult struct {
	AccessToken string
	Nickname    string
	Email       string
	Slug        string
}

type Memo struct {
	Slug      string
	Content   string
	CreatedAt string
	UpdatedAt string
	DeletedAt string
	MemoFrom  string
	Source    string
	Pin       json.RawMessage
	Files     json.RawMessage
	Tags      json.RawMessage
}

type AddMemoRequest struct {
	Content   string
	CreatedAt int64
	MemoFrom  string
	Source    string
	Timezone  string
}

type EditMemoRequest struct {
	Content        string
	CreatedAt      string
	LocalUpdatedAt int64
	MemoFrom       string
	Source         string
	FileIDs        []any
	Timezone       string
	Pinned         bool
}

type User struct {
	Slug       string `json:"slug,omitempty"`
	Nickname   string `json:"nickname,omitempty"`
	Email      string `json:"email,omitempty"`
	MemoCount  int    `json:"memo_count,omitempty"`
	CreatedAt  string `json:"created_at,omitempty"`
	ProExpired string `json:"pro_expired_at,omitempty"`
}
