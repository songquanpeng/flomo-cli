package flomo

import (
	"encoding/json"

	"github.com/songquanpeng/flomo-cli/internal/domain"
)

// The DTOs below deliberately mirror the current web API. They are private so
// field churn cannot leak into commands, services, SQLite, or JSON CLI output.
type loginDTO struct {
	AccessToken string `json:"access_token"`
	Nickname    string `json:"nickname,omitempty"`
	Email       string `json:"email,omitempty"`
	Slug        string `json:"slug,omitempty"`
}

type memoDTO struct {
	Slug      string          `json:"slug"`
	Content   string          `json:"content"`
	CreatedAt string          `json:"created_at"`
	UpdatedAt string          `json:"updated_at"`
	DeletedAt string          `json:"deleted_at,omitempty"`
	MemoFrom  string          `json:"memo_from,omitempty"`
	Source    string          `json:"source,omitempty"`
	Pin       json.RawMessage `json:"pin,omitempty"`
	Files     json.RawMessage `json:"files,omitempty"`
	Tags      json.RawMessage `json:"tags,omitempty"`
}

type userDTO struct {
	Slug       string `json:"slug,omitempty"`
	Nickname   string `json:"nickname,omitempty"`
	Email      string `json:"email,omitempty"`
	MemoCount  int    `json:"memo_count,omitempty"`
	CreatedAt  string `json:"created_at,omitempty"`
	ProExpired string `json:"pro_expired_at,omitempty"`
}

func (dto loginDTO) domain() domain.LoginResult {
	return domain.LoginResult{AccessToken: dto.AccessToken, Nickname: dto.Nickname, Email: dto.Email, Slug: dto.Slug}
}

func (dto memoDTO) domain() domain.Memo {
	return domain.Memo{
		Slug: dto.Slug, Content: dto.Content, CreatedAt: dto.CreatedAt, UpdatedAt: dto.UpdatedAt,
		DeletedAt: dto.DeletedAt, MemoFrom: dto.MemoFrom, Source: dto.Source,
		Pin: dto.Pin, Files: dto.Files, Tags: dto.Tags,
	}
}

func (dto userDTO) domain() domain.User {
	return domain.User{
		Slug: dto.Slug, Nickname: dto.Nickname, Email: dto.Email, MemoCount: dto.MemoCount,
		CreatedAt: dto.CreatedAt, ProExpired: dto.ProExpired,
	}
}
