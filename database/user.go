package database

import (
	"time"

	"github.com/uptrace/bun"
)

type User struct {
	bun.BaseModel `bun:"table:users"`

	PID        int64     `bun:"pid,pk,autoincrement"`
	Username   string    `bun:"username,notnull,unique"`
	Passwd     string    `bun:"passwd,notnull"`
	CreateTime time.Time `bun:"create_time,nullzero,notnull,default:current_timestamp"`
	UpdateTime time.Time `bun:"update_time,nullzero,notnull,default:current_timestamp"`
	Role       string    `bun:"role,default:'user'"`
}
