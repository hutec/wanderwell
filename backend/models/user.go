package models

type User struct {
	ID           string `json:"id"`
	Firstname    string `json:"name"`
	Lastname     string `json:"-"`
	ExpiresAt    int64  `json:"-"`
	RefreshToken string `json:"-"`
	AccessToken  string `json:"-"`
}
