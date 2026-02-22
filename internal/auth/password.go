// Copyright © 2026 Runable.app. GPL-3.0.
//
// Package auth (password.go) provides bcrypt password hashing and comparison
// for user authentication. HashPassword hashes a plaintext password; ComparePassword
// checks a password against a stored hash.
package auth

import (
	"golang.org/x/crypto/bcrypt"
)

const bcryptCost = bcrypt.DefaultCost

// HashPassword returns a bcrypt hash of the password.
func HashPassword(password string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// ComparePassword returns true if password matches the bcrypt hash.
func ComparePassword(hash, password string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	return err == nil
}
