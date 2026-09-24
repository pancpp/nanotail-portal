package database

import (
	"time"

	"github.com/uptrace/bun"
)

type TailscaleClient struct {
	bun.BaseModel `bun:"table:tailscale_clients"`

	PID          int64     `bun:"pid,pk,autoincrement"`
	ClientID     string    `bun:"client_id"`
	ClientSecret string    `bun:"client_secret"`
	CreateTime   time.Time `bun:"create_time,nullzero,notnull,default:current_timestamp"`
	UpdateTime   time.Time `bun:"update_time,nullzero,notnull,default:current_timestamp"`
	ApiTokenId   string    `bun:"api_token_id"`
	ApiToken     string    `bun:"api_token"`
	AuthKeyId    string    `bun:"auth_key_id"`
	AuthKey      string    `bun:"auth_key"`
}
