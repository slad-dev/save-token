package security

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const (
	passwordVersion = "pbkdf2_sha256"
	defaultIters    = 120000
	saltBytes       = 16
	keyBytes        = 32
)

func HashPassword(password string) (string, error) {
	password = strings.TrimSpace(password)
	if len(password) < 8 {
		return "", errors.New("password must be at least 8 characters")
	}

	salt := make([]byte, saltBytes)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}

	key, err := pbkdf2.Key(sha256.New, password, salt, defaultIters, keyBytes)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("%s$%d$%s$%s", passwordVersion, defaultIters, base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

func VerifyPassword(encodedHash, password string) bool {
	parts := strings.Split(encodedHash, "$")
	if len(parts) != 4 || parts[0] != passwordVersion {
		return false
	}

	iters, err := strconv.Atoi(parts[1])
	if err != nil || iters <= 0 {
		return false
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[2])
	if err != nil || len(salt) == 0 {
		return false
	}

	storedKey, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil || len(storedKey) == 0 {
		return false
	}

	computed, err := pbkdf2.Key(sha256.New, password, salt, iters, len(storedKey))
	if err != nil {
		return false
	}

	return subtle.ConstantTimeCompare(storedKey, computed) == 1
}
