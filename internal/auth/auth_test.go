package auth

import "testing"

func TestPasswordHash(t *testing.T) {
	hash, err := hashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if !passwordMatches("correct horse battery staple", hash) {
		t.Fatal("correct password did not match")
	}
	if passwordMatches("wrong password", hash) {
		t.Fatal("wrong password matched")
	}
}

func TestTokenHash(t *testing.T) {
	token, err := newToken()
	if err != nil {
		t.Fatal(err)
	}
	if token == "" || string(tokenHash(token)) == token {
		t.Fatal("token was not generated and hashed safely")
	}
}
