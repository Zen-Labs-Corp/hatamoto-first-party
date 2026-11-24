package core

import (
	"errors"
	"time"
)

// User is a row of the users table.
type User struct {
	ID           int64     `json:"id"`
	CommenterKey string    `json:"commenter_key"`
	Username     string    `json:"username"`
	About        string    `json:"about"`
	Karma        int       `json:"karma"`
	Showdead     bool      `json:"showdead"`
	Noprocrast   bool      `json:"noprocrast"`
	Maxvisit     int       `json:"maxvisit"`
	Minaway      int       `json:"minaway"`
	CreatedAt    time.Time `json:"created_at"`
}

const userSelect = "SELECT to_jsonb(u) FROM users u WHERE "

func oneUser(where string, arg any) (*User, error) {
	var u User
	if err := QueryJSON(userSelect+where, &u, arg); err != nil {
		if errors.Is(err, ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &u, nil
}

// UserByKey returns the member behind a commenter key, or nil. An empty key
// makes no host call.
func UserByKey(key string) (*User, error) {
	if key == "" {
		return nil, nil
	}
	return oneUser("u.commenter_key = $1", key)
}

// UserByName returns the member with that username (case-insensitive), or nil.
func UserByName(name string) (*User, error) {
	return oneUser("u.username_lower = lower($1)", name)
}

// UserByID returns the member with that id, or nil.
func UserByID(id int64) (*User, error) {
	return oneUser("u.id = $1", id)
}

// AddKarma adds delta (which may be negative) to a member's karma.
func AddKarma(userID int64, delta int) error {
	_, err := Exec("UPDATE users SET karma = karma + $1 WHERE id = $2", delta, userID)
	return err
}
