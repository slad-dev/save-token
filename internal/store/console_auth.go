package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
)

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func normalizeUserRole(role string) string {
	role = strings.ToLower(strings.TrimSpace(role))
	if role == RoleAdmin {
		return RoleAdmin
	}
	return RoleUser
}

func (s *SQLiteStore) FindUserByEmail(ctx context.Context, email string) (*User, error) {
	var user User
	if err := s.gormDB.WithContext(ctx).Where("email = ?", normalizeEmail(email)).First(&user).Error; err != nil {
		return nil, err
	}
	return &user, nil
}

func (s *SQLiteStore) FindUserByGitHubID(ctx context.Context, githubID string) (*User, error) {
	var user User
	if err := s.gormDB.WithContext(ctx).Where("git_hub_id = ?", strings.TrimSpace(githubID)).First(&user).Error; err != nil {
		return nil, err
	}
	return &user, nil
}

func (s *SQLiteStore) CreateConsoleUser(ctx context.Context, name, email, passwordHash string) (*User, error) {
	normalizedEmail := normalizeEmail(email)
	if normalizedEmail == "" {
		return nil, errors.New("email is required")
	}

	trimmedName := strings.TrimSpace(name)
	if trimmedName == "" {
		trimmedName = strings.Split(normalizedEmail, "@")[0]
	}

	user := User{
		Name:         trimmedName,
		Email:        &normalizedEmail,
		PasswordHash: passwordHash,
		Role:         RoleUser,
		Balance:      100000,
		Plan:         "free",
		IsActive:     true,
	}
	if err := s.gormDB.WithContext(ctx).Create(&user).Error; err != nil {
		return nil, err
	}
	return &user, nil
}

func (s *SQLiteStore) UpsertGitHubUser(ctx context.Context, githubID, email, name, avatarURL string) (*User, error) {
	githubID = strings.TrimSpace(githubID)
	if githubID == "" {
		return nil, errors.New("github id is required")
	}

	normalizedEmail := normalizeEmail(email)
	trimmedName := strings.TrimSpace(name)
	if trimmedName == "" {
		trimmedName = "github-user"
	}

	var user User
	err := s.gormDB.WithContext(ctx).Where("git_hub_id = ?", githubID).First(&user).Error
	if err == nil {
		user.Name = trimmedName
		user.AvatarURL = strings.TrimSpace(avatarURL)
		if normalizedEmail != "" {
			user.Email = &normalizedEmail
		}
		if err := s.gormDB.WithContext(ctx).Save(&user).Error; err != nil {
			return nil, err
		}
		return &user, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	if normalizedEmail != "" {
		err = s.gormDB.WithContext(ctx).Where("email = ?", normalizedEmail).First(&user).Error
		if err == nil {
			user.GitHubID = &githubID
			user.Name = trimmedName
			user.AvatarURL = strings.TrimSpace(avatarURL)
			if err := s.gormDB.WithContext(ctx).Save(&user).Error; err != nil {
				return nil, err
			}
			return &user, nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
	}

	user = User{
		Name:      trimmedName,
		Email:     nil,
		GitHubID:  &githubID,
		AvatarURL: strings.TrimSpace(avatarURL),
		Role:      RoleUser,
		Balance:   100000,
		Plan:      "free",
		IsActive:  true,
	}
	if normalizedEmail != "" {
		user.Email = &normalizedEmail
	}

	if err := s.gormDB.WithContext(ctx).Create(&user).Error; err != nil {
		return nil, err
	}
	return &user, nil
}

func (s *SQLiteStore) CreateConsoleSession(ctx context.Context, userID uint, provider string, ttl time.Duration) (string, *ConsoleSession, error) {
	token, err := randomToken(32)
	if err != nil {
		return "", nil, err
	}
	hash := hashToken(token)
	now := time.Now()
	session := &ConsoleSession{
		UserID:     userID,
		TokenHash:  hash,
		Provider:   strings.TrimSpace(provider),
		ExpiresAt:  now.Add(ttl),
		LastSeenAt: now,
	}
	if session.Provider == "" {
		session.Provider = "email"
	}
	if err := s.gormDB.WithContext(ctx).Create(session).Error; err != nil {
		return "", nil, err
	}
	return token, session, nil
}

func (s *SQLiteStore) FindConsoleSessionByToken(ctx context.Context, token string) (*ConsoleSession, error) {
	var session ConsoleSession
	err := s.gormDB.WithContext(ctx).
		Preload("User").
		Where("token_hash = ? AND expires_at > ?", hashToken(strings.TrimSpace(token)), time.Now()).
		First(&session).Error
	if err != nil {
		return nil, err
	}
	return &session, nil
}

func (s *SQLiteStore) TouchConsoleSession(ctx context.Context, sessionID uint) error {
	return s.gormDB.WithContext(ctx).
		Model(&ConsoleSession{}).
		Where("id = ?", sessionID).
		Update("last_seen_at", time.Now()).Error
}

func (s *SQLiteStore) DeleteConsoleSessionByToken(ctx context.Context, token string) error {
	return s.gormDB.WithContext(ctx).
		Where("token_hash = ?", hashToken(strings.TrimSpace(token))).
		Delete(&ConsoleSession{}).Error
}

func (s *SQLiteStore) CreateConsoleEmailCode(ctx context.Context, email, code string, ttl time.Duration) (*ConsoleEmailCode, error) {
	normalizedEmail := normalizeEmail(email)
	if normalizedEmail == "" {
		return nil, errors.New("email is required")
	}
	trimmedCode := strings.TrimSpace(code)
	if trimmedCode == "" {
		return nil, errors.New("code is required")
	}
	if ttl <= 0 {
		return nil, errors.New("ttl must be greater than 0")
	}

	now := time.Now()
	record := &ConsoleEmailCode{
		Email:     normalizedEmail,
		CodeHash:  hashEmailCode(normalizedEmail, trimmedCode),
		ExpiresAt: now.Add(ttl),
	}

	err := s.gormDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("email = ?", normalizedEmail).Delete(&ConsoleEmailCode{}).Error; err != nil {
			return err
		}
		if err := tx.
			Where("expires_at <= ? OR used_at IS NOT NULL", now).
			Delete(&ConsoleEmailCode{}).Error; err != nil {
			return err
		}
		return tx.Create(record).Error
	})
	if err != nil {
		return nil, err
	}
	return record, nil
}

func (s *SQLiteStore) ConsumeConsoleEmailCode(ctx context.Context, email, code string) (*ConsoleEmailCode, error) {
	normalizedEmail := normalizeEmail(email)
	trimmedCode := strings.TrimSpace(code)
	if normalizedEmail == "" || trimmedCode == "" {
		return nil, errors.New("email and code are required")
	}

	now := time.Now()
	var matched ConsoleEmailCode
	err := s.gormDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.
			Where("email = ? AND code_hash = ? AND used_at IS NULL AND expires_at > ?", normalizedEmail, hashEmailCode(normalizedEmail, trimmedCode), now).
			Order("id DESC").
			First(&matched).Error; err != nil {
			return err
		}
		return tx.Model(&ConsoleEmailCode{}).Where("id = ?", matched.ID).Update("used_at", now).Error
	})
	if err != nil {
		return nil, err
	}
	return &matched, nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func hashEmailCode(email, code string) string {
	sum := sha256.Sum256([]byte(normalizeEmail(email) + ":" + strings.TrimSpace(code)))
	return hex.EncodeToString(sum[:])
}

func randomToken(size int) (string, error) {
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
