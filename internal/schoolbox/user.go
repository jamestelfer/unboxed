package schoolbox

import (
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
)

type Role struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
}

// Person is the summary form of a user nested in other records.
type Person struct {
	ID         int    `json:"id"`
	ExternalID string `json:"externalId"`
	FullName   string `json:"fullName"`
	Role       Role   `json:"role"`
}

type User struct {
	ID         int      `json:"id"`
	ExternalID string   `json:"externalId"`
	FullName   string   `json:"fullName"`
	Role       Role     `json:"role"`
	Email      string   `json:"email"`
	Students   []Person `json:"students"`
}

var currentUserIDPattern = regexp.MustCompile(`window\.schoolboxUser\s*=\s*\{"id":(\d+)`)

// CurrentUserID returns the internal id of the token's user. The JWT sub claim
// is the SIS externalId, and only the homepage exposes the internal id.
func (c *Client) CurrentUserID(ctx context.Context) (int, error) {
	resp, err := c.send(ctx, "/", nil, "text/html")
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return 0, fmt.Errorf("GET /: %w", err)
	}
	m := currentUserIDPattern.FindSubmatch(body)
	if m == nil {
		return 0, errors.New("GET /: window.schoolboxUser not found")
	}
	return strconv.Atoi(string(m[1]))
}

func (c *Client) CurrentUser(ctx context.Context) (User, error) {
	id, err := c.CurrentUserID(ctx)
	if err != nil {
		return User{}, err
	}
	return c.User(ctx, id)
}

// User is only permitted for the current user or a superuser.
func (c *Client) User(ctx context.Context, id int) (User, error) {
	return get[User](ctx, c, "/api/user/"+strconv.Itoa(id), nil)
}
