package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"agent-gateway/internal/config"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

type ListOptions struct {
	Limit  int
	Offset int
}

type UsageLogFilter struct {
	ListOptions
	UserID   uint
	APIKeyID uint
	Endpoint string
}

type RouteTraceFilter struct {
	ListOptions
	UserID      uint
	APIKeyID    uint
	Endpoint    string
	IntentClass string
	RouteTier   string
}

type CreateProjectInput struct {
	UserID                uint
	Name                  string
	Description           string
	Mode                  string
	PrivacyMode           string
	WebSearchEnabled      bool
	AggressiveCompression bool
}

type CreateSourceInput struct {
	UserID          uint
	ProjectID       uint
	Name            string
	Provider        string
	BaseURL         string
	APIKey          string
	SupportedModels []string
}

type CreateAPIKeyInput struct {
	UserID         uint
	ProjectID      uint
	Name           string
	AllowWebSearch bool
}

type CreateManagedModelInput struct {
	GroupName       string
	DisplayName     string
	Description     string
	ModelIdentifier string
	Provider        string
	Icon            string
	SourceType      string
	BaseURL         string
	APIKey          string
	RouteStrategy   string
	SortOrder       int
}

type UpdateManagedModelInput struct {
	ID              uint
	GroupName       string
	DisplayName     string
	Description     string
	ModelIdentifier string
	Provider        string
	Icon            string
	SourceType      string
	BaseURL         string
	APIKey          string
	RouteStrategy   string
	SortOrder       int
}

type UpdateProjectSettingsInput struct {
	ProjectID             uint
	UserID                uint
	Mode                  string
	PrivacyMode           string
	WebSearchEnabled      bool
	AggressiveCompression bool
}

type UpdateUserAdminInput struct {
	UserID   uint
	Name     string
	Plan     string
	Role     string
	Balance  int64
	IsActive bool
}

type EnsureLocalIdentityInput struct {
	UserName              string
	ProjectName           string
	APIKeyName            string
	APIKeyValue           string
	AggressiveCompression bool
}

type UsageSummary struct {
	Requests        int64
	SavedTokens     int64
	CacheHits       int64
	CompressionHits int64
	LastRequestAt   *time.Time
}

type SQLiteStore struct {
	db     *sql.DB
	gormDB *gorm.DB
}

type UpstreamRecord struct {
	ID              int64
	Name            string
	BaseURL         string
	APIKey          string
	Weight          int
	Enabled         bool
	SupportedModels []string
}

type ModelRouteRecord struct {
	ID           int64
	ModelPattern string
	Strategy     string
	Upstreams    []string
}

func Open(ctx context.Context, path string) (*SQLiteStore, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create db directory: %w", err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	db.SetMaxOpenConns(1)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}

	gormDB, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("open gorm sqlite: %w", err)
	}

	store := &SQLiteStore{
		db:     db,
		gormDB: gormDB,
	}
	if err := store.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}

	return store, nil
}

func (s *SQLiteStore) Close() error {
	var firstErr error
	if s.gormDB != nil {
		if sqlDB, err := s.gormDB.DB(); err == nil && sqlDB != nil {
			if err := sqlDB.Close(); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	if s.db != nil {
		if err := s.db.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (s *SQLiteStore) migrate(ctx context.Context) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS upstreams (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL UNIQUE,
			base_url TEXT NOT NULL,
			api_key TEXT NOT NULL,
			weight INTEGER NOT NULL DEFAULT 1,
			enabled INTEGER NOT NULL DEFAULT 1,
			supported_models TEXT NOT NULL DEFAULT ''
		);`,
		`CREATE TABLE IF NOT EXISTS model_routes (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			model_pattern TEXT NOT NULL UNIQUE,
			strategy TEXT NOT NULL DEFAULT 'round_robin',
			upstreams TEXT NOT NULL
		);`,
	}

	for _, statement := range statements {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("run migration: %w", err)
		}
	}

	if err := s.gormDB.WithContext(ctx).AutoMigrate(&User{}, &Project{}, &Source{}, &ManagedModel{}, &APIKey{}, &ConsoleSession{}, &ConsoleEmailCode{}, &ContactRequest{}, &ContactNote{}, &UsageLog{}, &RouteTrace{}, &CacheEntry{}, &SiteSetting{}); err != nil {
		return fmt.Errorf("run gorm migrations: %w", err)
	}

	if err := s.ensureProjects(ctx); err != nil {
		return err
	}

	return nil
}

func (s *SQLiteStore) SeedIfEmpty(ctx context.Context, cfg *config.Config) error {
	var upstreamCount int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM upstreams`).Scan(&upstreamCount); err != nil {
		return fmt.Errorf("count upstreams: %w", err)
	}

	if upstreamCount == 0 {
		for _, upstream := range cfg.Bootstrap.Upstreams {
			if _, err := s.db.ExecContext(
				ctx,
				`INSERT INTO upstreams (name, base_url, api_key, weight, enabled, supported_models) VALUES (?, ?, ?, ?, ?, ?)`,
				upstream.Name,
				upstream.BaseURL,
				upstream.APIKey,
				upstream.Weight,
				boolToInt(upstream.Enabled != nil && *upstream.Enabled),
				strings.Join(upstream.SupportedModels, ","),
			); err != nil {
				return fmt.Errorf("seed upstream %s: %w", upstream.Name, err)
			}
		}
	}

	var routeCount int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM model_routes`).Scan(&routeCount); err != nil {
		return fmt.Errorf("count model_routes: %w", err)
	}

	if routeCount == 0 {
		for _, route := range cfg.Bootstrap.ModelRoutes {
			if _, err := s.db.ExecContext(
				ctx,
				`INSERT INTO model_routes (model_pattern, strategy, upstreams) VALUES (?, ?, ?)`,
				route.ModelPattern,
				route.Strategy,
				strings.Join(route.Upstreams, ","),
			); err != nil {
				return fmt.Errorf("seed route %s: %w", route.ModelPattern, err)
			}
		}
	}

	var userCount int64
	if err := s.gormDB.WithContext(ctx).Model(&User{}).Count(&userCount).Error; err != nil {
		return fmt.Errorf("count users: %w", err)
	}

	if userCount == 0 {
		for _, seed := range cfg.Bootstrap.Users {
			user := User{
				Name:     seed.Name,
				Balance:  seed.Balance,
				Plan:     seed.Plan,
				IsActive: seed.IsActive != nil && *seed.IsActive,
			}
			if err := s.gormDB.WithContext(ctx).Create(&user).Error; err != nil {
				return fmt.Errorf("seed user %s: %w", seed.Name, err)
			}
		}
	}

	var keyCount int64
	if err := s.gormDB.WithContext(ctx).Model(&APIKey{}).Count(&keyCount).Error; err != nil {
		return fmt.Errorf("count api keys: %w", err)
	}

	if keyCount == 0 {
		for _, seed := range cfg.Bootstrap.APIKeys {
			var user User
			if err := s.gormDB.WithContext(ctx).Where("name = ?", seed.UserName).First(&user).Error; err != nil {
				return fmt.Errorf("find user %s for api key %s: %w", seed.UserName, seed.Name, err)
			}

			apiKey := APIKey{
				Name:           seed.Name,
				Key:            seed.Key,
				UserID:         user.ID,
				AllowWebSearch: seed.AllowWebSearch != nil && *seed.AllowWebSearch,
				IsActive:       seed.IsActive != nil && *seed.IsActive,
			}
			if err := s.gormDB.WithContext(ctx).Create(&apiKey).Error; err != nil {
				return fmt.Errorf("seed api key %s: %w", seed.Name, err)
			}
		}
	}

	if err := s.ensureProjects(ctx); err != nil {
		return err
	}

	return nil
}

func (s *SQLiteStore) FindAPIKeyWithUser(ctx context.Context, key string) (*APIKey, error) {
	var apiKey APIKey
	if err := s.gormDB.WithContext(ctx).Preload("User").Preload("Project").Where("key = ?", key).First(&apiKey).Error; err != nil {
		return nil, err
	}
	return &apiKey, nil
}

func (s *SQLiteStore) FindUserByID(ctx context.Context, userID uint) (*User, error) {
	var user User
	if err := s.gormDB.WithContext(ctx).Where("id = ?", userID).First(&user).Error; err != nil {
		return nil, err
	}
	return &user, nil
}

func (s *SQLiteStore) CountActiveAPIKeysByUser(ctx context.Context, userID uint) (int64, error) {
	var total int64
	err := s.gormDB.WithContext(ctx).
		Model(&APIKey{}).
		Where("user_id = ? AND is_active = ?", userID, true).
		Count(&total).Error
	return total, err
}

func (s *SQLiteStore) ListProjectsByUser(ctx context.Context, userID uint) ([]Project, error) {
	var projects []Project
	if err := s.gormDB.WithContext(ctx).
		Preload("APIKeys").
		Where("user_id = ?", userID).
		Order("id ASC").
		Find(&projects).Error; err != nil {
		return nil, err
	}
	return projects, nil
}

func (s *SQLiteStore) CreateProject(ctx context.Context, input CreateProjectInput) (*Project, error) {
	project := Project{
		UserID:                input.UserID,
		Name:                  strings.TrimSpace(input.Name),
		Description:           strings.TrimSpace(input.Description),
		Mode:                  normalizeProjectMode(input.Mode),
		PrivacyMode:           normalizeProjectPrivacyMode(input.PrivacyMode),
		WebSearchEnabled:      input.WebSearchEnabled,
		AggressiveCompression: input.AggressiveCompression,
		IsActive:              true,
	}
	if err := s.gormDB.WithContext(ctx).Create(&project).Error; err != nil {
		return nil, err
	}

	if err := s.gormDB.WithContext(ctx).
		Model(&Project{}).
		Where("id = ?", project.ID).
		Updates(map[string]any{
			"name":                   strings.TrimSpace(input.Name),
			"description":            strings.TrimSpace(input.Description),
			"mode":                   normalizeProjectMode(input.Mode),
			"privacy_mode":           normalizeProjectPrivacyMode(input.PrivacyMode),
			"web_search_enabled":     input.WebSearchEnabled,
			"aggressive_compression": input.AggressiveCompression,
			"user_id":                input.UserID,
			"is_active":              true,
		}).Error; err != nil {
		return nil, err
	}

	if err := s.gormDB.WithContext(ctx).First(&project, project.ID).Error; err != nil {
		return nil, err
	}

	return &project, nil
}

func (s *SQLiteStore) UpdateProjectSettings(ctx context.Context, input UpdateProjectSettingsInput) (*Project, error) {
	project, err := s.FindProjectByUser(ctx, input.UserID, input.ProjectID)
	if err != nil {
		return nil, err
	}

	project.Mode = normalizeProjectMode(input.Mode)
	project.PrivacyMode = normalizeProjectPrivacyMode(input.PrivacyMode)
	project.WebSearchEnabled = input.WebSearchEnabled
	project.AggressiveCompression = input.AggressiveCompression

	if err := s.gormDB.WithContext(ctx).Save(project).Error; err != nil {
		return nil, err
	}
	return project, nil
}

func (s *SQLiteStore) ListSourcesByUser(ctx context.Context, userID uint) ([]Source, error) {
	var sources []Source
	if err := s.gormDB.WithContext(ctx).
		Preload("Project").
		Where("user_id = ?", userID).
		Order("id ASC").
		Find(&sources).Error; err != nil {
		return nil, err
	}
	return sources, nil
}

func (s *SQLiteStore) ListActiveSourcesByProject(ctx context.Context, userID uint, projectID uint) ([]Source, error) {
	var sources []Source
	if err := s.gormDB.WithContext(ctx).
		Where("user_id = ? AND project_id = ? AND is_active = ?", userID, projectID, true).
		Order("id ASC").
		Find(&sources).Error; err != nil {
		return nil, err
	}
	return sources, nil
}

func (s *SQLiteStore) CreateSource(ctx context.Context, input CreateSourceInput) (*Source, error) {
	source := Source{
		Name:            strings.TrimSpace(input.Name),
		Kind:            "byok",
		Provider:        normalizeSourceProvider(input.Provider),
		BaseURL:         strings.TrimSpace(input.BaseURL),
		APIKey:          strings.TrimSpace(input.APIKey),
		SupportedModels: strings.Join(cleanStringList(input.SupportedModels), ","),
		UserID:          input.UserID,
		ProjectID:       input.ProjectID,
		IsActive:        true,
	}
	if err := s.gormDB.WithContext(ctx).Create(&source).Error; err != nil {
		return nil, err
	}
	return &source, nil
}

func (s *SQLiteStore) CreateAPIKey(ctx context.Context, input CreateAPIKeyInput) (*APIKey, error) {
	keyValue, err := generateAPIKey()
	if err != nil {
		return nil, err
	}

	projectID := input.ProjectID
	apiKey := APIKey{
		Name:           strings.TrimSpace(input.Name),
		Key:            keyValue,
		UserID:         input.UserID,
		ProjectID:      &projectID,
		AllowWebSearch: input.AllowWebSearch,
		IsActive:       true,
	}
	if err := s.gormDB.WithContext(ctx).Create(&apiKey).Error; err != nil {
		return nil, err
	}
	return &apiKey, nil
}

func (s *SQLiteStore) ListManagedModels(ctx context.Context) ([]ManagedModel, error) {
	var models []ManagedModel
	if err := s.gormDB.WithContext(ctx).Order("group_name ASC, sort_order ASC, id ASC").Find(&models).Error; err != nil {
		return nil, err
	}
	return models, nil
}

func (s *SQLiteStore) ListManagedModelsMap(ctx context.Context) (map[string]ManagedModel, error) {
	models, err := s.ListManagedModels(ctx)
	if err != nil {
		return nil, err
	}
	result := make(map[string]ManagedModel, len(models))
	for _, model := range models {
		result[model.ModelIdentifier] = model
	}
	return result, nil
}

func (s *SQLiteStore) CreateManagedModel(ctx context.Context, input CreateManagedModelInput) (*ManagedModel, error) {
	model := ManagedModel{
		GroupName:       normalizeManagedModelGroup(input.GroupName),
		DisplayName:     strings.TrimSpace(input.DisplayName),
		Description:     strings.TrimSpace(input.Description),
		ModelIdentifier: strings.TrimSpace(input.ModelIdentifier),
		Provider:        normalizeSourceProvider(input.Provider),
		Icon:            normalizeManagedModelIcon(input.Icon, input.Provider),
		SourceType:      normalizeManagedModelSourceType(input.SourceType),
		BaseURL:         strings.TrimSpace(input.BaseURL),
		APIKey:          strings.TrimSpace(input.APIKey),
		RouteStrategy:   normalizeManagedModelStrategy(input.RouteStrategy),
		SortOrder:       normalizeManagedModelSortOrder(input.SortOrder),
		Enabled:         true,
	}
	model.UpstreamName = managedModelUpstreamName(model.ModelIdentifier, model.Provider)

	if err := s.gormDB.WithContext(ctx).Create(&model).Error; err != nil {
		return nil, err
	}
	if err := s.syncManagedModel(ctx, &model); err != nil {
		return nil, err
	}
	return &model, nil
}

func (s *SQLiteStore) UpdateManagedModel(ctx context.Context, input UpdateManagedModelInput) (*ManagedModel, error) {
	var model ManagedModel
	if err := s.gormDB.WithContext(ctx).First(&model, input.ID).Error; err != nil {
		return nil, err
	}

	model.GroupName = normalizeManagedModelGroup(input.GroupName)
	model.DisplayName = strings.TrimSpace(input.DisplayName)
	model.Description = strings.TrimSpace(input.Description)
	model.ModelIdentifier = strings.TrimSpace(input.ModelIdentifier)
	model.Provider = normalizeSourceProvider(input.Provider)
	model.Icon = normalizeManagedModelIcon(input.Icon, input.Provider)
	model.SourceType = normalizeManagedModelSourceType(input.SourceType)
	model.BaseURL = strings.TrimSpace(input.BaseURL)
	if trimmed := strings.TrimSpace(input.APIKey); trimmed != "" {
		model.APIKey = trimmed
	}
	model.RouteStrategy = normalizeManagedModelStrategy(input.RouteStrategy)
	model.SortOrder = normalizeManagedModelSortOrder(input.SortOrder)
	model.UpstreamName = managedModelUpstreamName(model.ModelIdentifier, model.Provider)

	if err := s.gormDB.WithContext(ctx).Save(&model).Error; err != nil {
		return nil, err
	}
	if err := s.syncManagedModel(ctx, &model); err != nil {
		return nil, err
	}
	return &model, nil
}

func (s *SQLiteStore) SetManagedModelEnabled(ctx context.Context, modelID uint, enabled bool) (*ManagedModel, error) {
	var model ManagedModel
	if err := s.gormDB.WithContext(ctx).First(&model, modelID).Error; err != nil {
		return nil, err
	}
	model.Enabled = enabled
	if err := s.gormDB.WithContext(ctx).Save(&model).Error; err != nil {
		return nil, err
	}
	if err := s.syncManagedModel(ctx, &model); err != nil {
		return nil, err
	}
	return &model, nil
}

func (s *SQLiteStore) DeleteManagedModel(ctx context.Context, modelID uint) error {
	var model ManagedModel
	if err := s.gormDB.WithContext(ctx).First(&model, modelID).Error; err != nil {
		return err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	if _, err = tx.ExecContext(ctx, `DELETE FROM model_routes WHERE model_pattern = ?`, model.ModelIdentifier); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM upstreams WHERE name = ?`, model.UpstreamName); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}

	return s.gormDB.WithContext(ctx).Delete(&model).Error
}

func (s *SQLiteStore) FindProjectByUser(ctx context.Context, userID, projectID uint) (*Project, error) {
	var project Project
	if err := s.gormDB.WithContext(ctx).Where("id = ? AND user_id = ?", projectID, userID).First(&project).Error; err != nil {
		return nil, err
	}
	return &project, nil
}

func (s *SQLiteStore) FindProjectDetailByUser(ctx context.Context, userID, projectID uint) (*Project, error) {
	var project Project
	if err := s.gormDB.WithContext(ctx).
		Preload("APIKeys").
		Preload("Sources").
		Where("id = ? AND user_id = ?", projectID, userID).
		First(&project).Error; err != nil {
		return nil, err
	}
	return &project, nil
}

func (s *SQLiteStore) ListUsers(ctx context.Context, options ListOptions) ([]User, int64, error) {
	var total int64
	query := s.gormDB.WithContext(ctx).Model(&User{})
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var users []User
	if err := query.Order("id ASC").Limit(normalizeLimit(options.Limit)).Offset(normalizeOffset(options.Offset)).Find(&users).Error; err != nil {
		return nil, 0, err
	}
	return users, total, nil
}

func (s *SQLiteStore) UpdateUserAdmin(ctx context.Context, input UpdateUserAdminInput) (*User, error) {
	var user User
	if err := s.gormDB.WithContext(ctx).First(&user, input.UserID).Error; err != nil {
		return nil, err
	}

	name := strings.TrimSpace(input.Name)
	if name == "" {
		name = user.Name
	}
	if strings.TrimSpace(name) == "" {
		name = "user"
	}

	plan := strings.TrimSpace(input.Plan)
	if plan == "" {
		plan = "free"
	}

	user.Name = name
	user.Plan = plan
	user.Role = normalizeUserRole(input.Role)
	user.Balance = input.Balance
	user.IsActive = input.IsActive

	if err := s.gormDB.WithContext(ctx).Save(&user).Error; err != nil {
		return nil, err
	}
	return &user, nil
}

func (s *SQLiteStore) GetSiteSettings(ctx context.Context) (map[string]string, error) {
	var records []SiteSetting
	if err := s.gormDB.WithContext(ctx).Order("key ASC").Find(&records).Error; err != nil {
		return nil, err
	}

	result := make(map[string]string, len(records))
	for _, record := range records {
		result[record.Key] = record.Value
	}
	return result, nil
}

func (s *SQLiteStore) UpsertSiteSettings(ctx context.Context, values map[string]string) error {
	return s.gormDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for key, value := range values {
			trimmedKey := strings.TrimSpace(key)
			if trimmedKey == "" {
				continue
			}

			var record SiteSetting
			err := tx.Where("key = ?", trimmedKey).First(&record).Error
			if err == nil {
				record.Value = value
				if err := tx.Save(&record).Error; err != nil {
					return err
				}
				continue
			}
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}

			if err := tx.Create(&SiteSetting{Key: trimmedKey, Value: value}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *SQLiteStore) ListAPIKeys(ctx context.Context, options ListOptions) ([]APIKey, int64, error) {
	var total int64
	query := s.gormDB.WithContext(ctx).Model(&APIKey{})
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var apiKeys []APIKey
	if err := query.Preload("User").Preload("Project").Order("id ASC").Limit(normalizeLimit(options.Limit)).Offset(normalizeOffset(options.Offset)).Find(&apiKeys).Error; err != nil {
		return nil, 0, err
	}
	return apiKeys, total, nil
}

func (s *SQLiteStore) ListUsageLogs(ctx context.Context, filter UsageLogFilter) ([]UsageLog, int64, error) {
	query := s.gormDB.WithContext(ctx).Model(&UsageLog{})
	if filter.UserID > 0 {
		query = query.Where("user_id = ?", filter.UserID)
	}
	if filter.APIKeyID > 0 {
		query = query.Where("api_key_id = ?", filter.APIKeyID)
	}
	if filter.Endpoint != "" {
		query = query.Where("endpoint = ?", filter.Endpoint)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var logs []UsageLog
	if err := query.Order("id DESC").Limit(normalizeLimit(filter.Limit)).Offset(normalizeOffset(filter.Offset)).Find(&logs).Error; err != nil {
		return nil, 0, err
	}
	return logs, total, nil
}

func (s *SQLiteStore) ListUsageLogsByUserAndTimeRange(ctx context.Context, userID uint, start, end time.Time) ([]UsageLog, error) {
	query := s.gormDB.WithContext(ctx).
		Model(&UsageLog{}).
		Where("user_id = ? AND created_at >= ? AND created_at < ?", userID, start, end)

	var logs []UsageLog
	if err := query.Order("created_at ASC").Find(&logs).Error; err != nil {
		return nil, err
	}
	return logs, nil
}

func (s *SQLiteStore) ListRouteTraces(ctx context.Context, filter RouteTraceFilter) ([]RouteTrace, int64, error) {
	query := s.gormDB.WithContext(ctx).Model(&RouteTrace{})
	if filter.UserID > 0 {
		query = query.Where("user_id = ?", filter.UserID)
	}
	if filter.APIKeyID > 0 {
		query = query.Where("api_key_id = ?", filter.APIKeyID)
	}
	if filter.Endpoint != "" {
		query = query.Where("endpoint = ?", filter.Endpoint)
	}
	if filter.IntentClass != "" {
		query = query.Where("intent_class = ?", filter.IntentClass)
	}
	if filter.RouteTier != "" {
		query = query.Where("route_tier = ?", filter.RouteTier)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var traces []RouteTrace
	if err := query.Order("id DESC").Limit(normalizeLimit(filter.Limit)).Offset(normalizeOffset(filter.Offset)).Find(&traces).Error; err != nil {
		return nil, 0, err
	}
	return traces, total, nil
}

func (s *SQLiteStore) ListRouteTracesByUserAndTimeRange(ctx context.Context, userID uint, start, end time.Time) ([]RouteTrace, error) {
	query := s.gormDB.WithContext(ctx).
		Model(&RouteTrace{}).
		Where("user_id = ? AND created_at >= ? AND created_at < ?", userID, start, end)

	var traces []RouteTrace
	if err := query.Order("created_at ASC").Find(&traces).Error; err != nil {
		return nil, err
	}
	return traces, nil
}

func (s *SQLiteStore) ensureProjects(ctx context.Context) error {
	var users []User
	if err := s.gormDB.WithContext(ctx).Find(&users).Error; err != nil {
		return fmt.Errorf("list users for project backfill: %w", err)
	}

	for _, user := range users {
		var projects []Project
		if err := s.gormDB.WithContext(ctx).Where("user_id = ?", user.ID).Order("id ASC").Find(&projects).Error; err != nil {
			return fmt.Errorf("list projects for user %d: %w", user.ID, err)
		}

		var defaultProject Project
		if len(projects) == 0 {
			defaultProject = Project{
				UserID:                user.ID,
				Name:                  "Default Project",
				Description:           "Auto-created to group existing gateway keys and usage under a project.",
				Mode:                  "hybrid",
				PrivacyMode:           PrivacyModeStandard,
				WebSearchEnabled:      true,
				AggressiveCompression: false,
				IsActive:              true,
			}
			if err := s.gormDB.WithContext(ctx).Create(&defaultProject).Error; err != nil {
				return fmt.Errorf("create default project for user %d: %w", user.ID, err)
			}
		} else {
			defaultProject = projects[0]
			for i := range projects {
				if normalizeProjectPrivacyMode(projects[i].PrivacyMode) != projects[i].PrivacyMode {
					projects[i].PrivacyMode = normalizeProjectPrivacyMode(projects[i].PrivacyMode)
					if err := s.gormDB.WithContext(ctx).Save(&projects[i]).Error; err != nil {
						return fmt.Errorf("backfill project privacy mode for project %d: %w", projects[i].ID, err)
					}
				}
			}
		}

		if err := s.gormDB.WithContext(ctx).
			Model(&APIKey{}).
			Where("user_id = ? AND project_id IS NULL", user.ID).
			Update("project_id", defaultProject.ID).Error; err != nil {
			return fmt.Errorf("backfill api key projects for user %d: %w", user.ID, err)
		}
	}

	return nil
}

func normalizeProjectMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "managed":
		return "managed"
	case "byok":
		return "byok"
	default:
		return "hybrid"
	}
}

func normalizeProjectPrivacyMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case PrivacyModeStrict:
		return PrivacyModeStrict
	default:
		return PrivacyModeStandard
	}
}

func normalizeSourceProvider(provider string) string {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "" {
		return "custom"
	}
	return provider
}

func cleanStringList(values []string) []string {
	items := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			items = append(items, value)
		}
	}
	return items
}

func generateAPIKey() (string, error) {
	buf := make([]byte, 18)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "sk-" + hex.EncodeToString(buf), nil
}

var ErrInsufficientBalance = errors.New("insufficient balance")

func (s *SQLiteStore) RecordUsageAndDeduct(ctx context.Context, log UsageLog) error {
	return s.gormDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if log.CreditsCharged > 0 && log.Success {
			result := tx.Model(&User{}).
				Where("id = ? AND balance >= ?", log.UserID, log.CreditsCharged).
				Update("balance", gorm.Expr("balance - ?", log.CreditsCharged))
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected == 0 {
				return ErrInsufficientBalance
			}
		}

		if err := tx.Create(&log).Error; err != nil {
			return err
		}
		return nil
	})
}

func (s *SQLiteStore) CreateRouteTrace(ctx context.Context, trace RouteTrace) error {
	return s.gormDB.WithContext(ctx).Create(&trace).Error
}

func (s *SQLiteStore) EnsureLocalIdentity(ctx context.Context, input EnsureLocalIdentityInput) (*APIKey, error) {
	userName := strings.TrimSpace(input.UserName)
	if userName == "" {
		userName = "Local User"
	}
	projectName := strings.TrimSpace(input.ProjectName)
	if projectName == "" {
		projectName = "Local Gateway"
	}
	apiKeyName := strings.TrimSpace(input.APIKeyName)
	if apiKeyName == "" {
		apiKeyName = "Local Gateway Key"
	}
	apiKeyValue := strings.TrimSpace(input.APIKeyValue)
	if apiKeyValue == "" {
		apiKeyValue = "sk-local-gateway"
	}

	var result APIKey
	err := s.gormDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var user User
		if err := tx.Where("name = ?", userName).First(&user).Error; err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			user = User{
				Name:     userName,
				Role:     RoleAdmin,
				Balance:  1_000_000_000,
				Plan:     "local",
				IsActive: true,
			}
			if err := tx.Create(&user).Error; err != nil {
				return err
			}
		} else {
			updates := map[string]any{
				"role":      RoleAdmin,
				"balance":   int64(1_000_000_000),
				"plan":      "local",
				"is_active": true,
			}
			if err := tx.Model(&User{}).Where("id = ?", user.ID).Updates(updates).Error; err != nil {
				return err
			}
		}

		var project Project
		if err := tx.Where("user_id = ? AND name = ?", user.ID, projectName).First(&project).Error; err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			project = Project{
				UserID:                user.ID,
				Name:                  projectName,
				Description:           "Auto-created local gateway project.",
				Mode:                  "managed",
				PrivacyMode:           PrivacyModeStandard,
				WebSearchEnabled:      false,
				AggressiveCompression: input.AggressiveCompression,
				IsActive:              true,
			}
			if err := tx.Create(&project).Error; err != nil {
				return err
			}
		} else {
			updates := map[string]any{
				"mode":                   "managed",
				"privacy_mode":           PrivacyModeStandard,
				"web_search_enabled":     false,
				"aggressive_compression": input.AggressiveCompression,
				"is_active":              true,
			}
			if err := tx.Model(&Project{}).Where("id = ?", project.ID).Updates(updates).Error; err != nil {
				return err
			}
		}

		if err := tx.Where("user_id = ? AND name = ?", user.ID, apiKeyName).First(&result).Error; err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			result = APIKey{
				Name:           apiKeyName,
				Key:            apiKeyValue,
				UserID:         user.ID,
				ProjectID:      &project.ID,
				AllowWebSearch: false,
				IsActive:       true,
			}
			if err := tx.Create(&result).Error; err != nil {
				return err
			}
		} else {
			updates := map[string]any{
				"key":              apiKeyValue,
				"project_id":       project.ID,
				"allow_web_search": false,
				"is_active":        true,
			}
			if err := tx.Model(&APIKey{}).Where("id = ?", result.ID).Updates(updates).Error; err != nil {
				return err
			}
		}

		return tx.Preload("User").Preload("Project").First(&result, result.ID).Error
	})
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func (s *SQLiteStore) UpsertManagedUpstream(ctx context.Context, name, baseURL, apiKey string, supportedModels []string, modelPattern, strategy string) error {
	upstreamName := strings.TrimSpace(name)
	if upstreamName == "" {
		return errors.New("upstream name is required")
	}
	baseURL = strings.TrimSpace(baseURL)
	apiKey = strings.TrimSpace(apiKey)
	modelPattern = strings.TrimSpace(modelPattern)
	if modelPattern == "" {
		modelPattern = "*"
	}
	strategy = strings.TrimSpace(strategy)
	if strategy == "" {
		strategy = "fixed"
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	if baseURL == "" || apiKey == "" {
		if _, err = tx.ExecContext(ctx, `DELETE FROM model_routes WHERE model_pattern = ?`, modelPattern); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM upstreams WHERE name = ?`, upstreamName); err != nil {
			return err
		}
		err = tx.Commit()
		return err
	}

	if _, err = tx.ExecContext(
		ctx,
		`INSERT INTO upstreams (name, base_url, api_key, weight, enabled, supported_models)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(name) DO UPDATE SET
		   base_url=excluded.base_url,
		   api_key=excluded.api_key,
		   weight=excluded.weight,
		   enabled=excluded.enabled,
		   supported_models=excluded.supported_models`,
		upstreamName,
		baseURL,
		apiKey,
		1,
		1,
		strings.Join(cleanStringList(supportedModels), ","),
	); err != nil {
		return err
	}
	if _, err = tx.ExecContext(
		ctx,
		`INSERT INTO model_routes (model_pattern, strategy, upstreams)
		 VALUES (?, ?, ?)
		 ON CONFLICT(model_pattern) DO UPDATE SET
		   strategy=excluded.strategy,
		   upstreams=excluded.upstreams`,
		modelPattern,
		strategy,
		upstreamName,
	); err != nil {
		return err
	}

	err = tx.Commit()
	return err
}

func (s *SQLiteStore) SummarizeUsage(ctx context.Context, userID uint) (UsageSummary, error) {
	var summary UsageSummary
	var lastRequestAt sql.NullString

	usageRow := s.db.QueryRowContext(
		ctx,
		`SELECT
			COUNT(1),
			COALESCE(SUM(saved_tokens), 0),
			MAX(created_at)
		FROM usage_logs
		WHERE user_id = ?`,
		userID,
	)
	if err := usageRow.Scan(&summary.Requests, &summary.SavedTokens, &lastRequestAt); err != nil {
		return UsageSummary{}, err
	}
	if lastRequestAt.Valid && strings.TrimSpace(lastRequestAt.String) != "" {
		parsed, err := parseSQLiteTime(lastRequestAt.String)
		if err != nil {
			return UsageSummary{}, err
		}
		summary.LastRequestAt = &parsed
	}

	traceRow := s.db.QueryRowContext(
		ctx,
		`SELECT
			COALESCE(SUM(CASE WHEN cache_hit = 1 THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN compression_applied = 1 THEN 1 ELSE 0 END), 0)
		FROM route_traces
		WHERE user_id = ?`,
		userID,
	)
	if err := traceRow.Scan(&summary.CacheHits, &summary.CompressionHits); err != nil {
		return UsageSummary{}, err
	}

	return summary, nil
}

func parseSQLiteTime(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05",
	}
	for _, layout := range layouts {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, fmt.Errorf("parse sqlite time %q: unsupported format", value)
}

func (s *SQLiteStore) FindValidCacheEntry(ctx context.Context, userID uint, endpoint, requestHash string) (*CacheEntry, error) {
	var entry CacheEntry
	err := s.gormDB.WithContext(ctx).
		Where("user_id = ? AND endpoint = ? AND request_hash = ? AND expires_at > ?", userID, endpoint, requestHash, time.Now()).
		Order("id DESC").
		First(&entry).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &entry, nil
}

func (s *SQLiteStore) UpsertCacheEntry(ctx context.Context, entry CacheEntry) error {
	return s.gormDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("user_id = ? AND endpoint = ? AND request_hash = ?", entry.UserID, entry.Endpoint, entry.RequestHash).Delete(&CacheEntry{}).Error; err != nil {
			return err
		}
		return tx.Create(&entry).Error
	})
}

func (s *SQLiteStore) ListValidSemanticCacheEntries(ctx context.Context, userID uint, endpoint, model string, limit int) ([]CacheEntry, error) {
	query := s.gormDB.WithContext(ctx).
		Where("user_id = ? AND endpoint = ? AND expires_at > ? AND query_text <> ''", userID, endpoint, time.Now())
	if model != "" {
		query = query.Where("model = ?", model)
	}

	var entries []CacheEntry
	if err := query.Order("created_at DESC").Limit(normalizeLimit(limit)).Find(&entries).Error; err != nil {
		return nil, err
	}
	return entries, nil
}

func (s *SQLiteStore) LoadUpstreams(ctx context.Context) ([]UpstreamRecord, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, base_url, api_key, weight, enabled, supported_models FROM upstreams WHERE enabled = 1 ORDER BY id ASC`)
	if err != nil {
		return nil, fmt.Errorf("query upstreams: %w", err)
	}
	defer rows.Close()

	var records []UpstreamRecord
	for rows.Next() {
		var record UpstreamRecord
		var supportedModels string
		var enabled int
		if err := rows.Scan(&record.ID, &record.Name, &record.BaseURL, &record.APIKey, &record.Weight, &enabled, &supportedModels); err != nil {
			return nil, fmt.Errorf("scan upstream: %w", err)
		}
		record.Enabled = enabled == 1
		record.SupportedModels = splitCSV(supportedModels)
		records = append(records, record)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate upstreams: %w", err)
	}

	return records, nil
}

func (s *SQLiteStore) LoadModelRoutes(ctx context.Context) ([]ModelRouteRecord, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, model_pattern, strategy, upstreams FROM model_routes ORDER BY id ASC`)
	if err != nil {
		return nil, fmt.Errorf("query model routes: %w", err)
	}
	defer rows.Close()

	var records []ModelRouteRecord
	for rows.Next() {
		var record ModelRouteRecord
		var upstreams string
		if err := rows.Scan(&record.ID, &record.ModelPattern, &record.Strategy, &upstreams); err != nil {
			return nil, fmt.Errorf("scan route: %w", err)
		}
		record.Upstreams = splitCSV(upstreams)
		records = append(records, record)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate model routes: %w", err)
	}

	return records, nil
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func splitCSV(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}

	parts := strings.Split(value, ",")
	items := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			items = append(items, part)
		}
	}
	return items
}

func (s *SQLiteStore) syncManagedModel(ctx context.Context, model *ManagedModel) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	if model.Enabled {
		if _, err = tx.ExecContext(
			ctx,
			`INSERT INTO upstreams (name, base_url, api_key, weight, enabled, supported_models)
			 VALUES (?, ?, ?, ?, ?, ?)
			 ON CONFLICT(name) DO UPDATE SET
			   base_url=excluded.base_url,
			   api_key=excluded.api_key,
			   weight=excluded.weight,
			   enabled=excluded.enabled,
			   supported_models=excluded.supported_models`,
			model.UpstreamName,
			model.BaseURL,
			model.APIKey,
			1,
			1,
			model.ModelIdentifier,
		); err != nil {
			return fmt.Errorf("sync upstream: %w", err)
		}
		if _, err = tx.ExecContext(
			ctx,
			`INSERT INTO model_routes (model_pattern, strategy, upstreams)
			 VALUES (?, ?, ?)
			 ON CONFLICT(model_pattern) DO UPDATE SET
			   strategy=excluded.strategy,
			   upstreams=excluded.upstreams`,
			model.ModelIdentifier,
			model.RouteStrategy,
			model.UpstreamName,
		); err != nil {
			return fmt.Errorf("sync route: %w", err)
		}
	} else {
		if _, err = tx.ExecContext(ctx, `UPDATE upstreams SET enabled = 0 WHERE name = ?`, model.UpstreamName); err != nil {
			return fmt.Errorf("disable upstream: %w", err)
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM model_routes WHERE model_pattern = ?`, model.ModelIdentifier); err != nil {
			return fmt.Errorf("delete route: %w", err)
		}
	}

	if err = tx.Commit(); err != nil {
		return err
	}
	return nil
}

var nonAlnumRegexp = regexp.MustCompile(`[^a-z0-9]+`)

func managedModelUpstreamName(modelIdentifier, provider string) string {
	base := strings.ToLower(strings.TrimSpace(provider + "-" + modelIdentifier))
	base = nonAlnumRegexp.ReplaceAllString(base, "-")
	base = strings.Trim(base, "-")
	if base == "" {
		base = "managed-model"
	}
	return "managed-" + base
}

func normalizeManagedModelStrategy(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "round_robin":
		return "round_robin"
	default:
		return "fixed"
	}
}

func normalizeManagedModelSourceType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "proxy":
		return "proxy"
	default:
		return "official"
	}
}

func normalizeManagedModelGroup(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "默认分组"
	}
	return value
}

func normalizeManagedModelSortOrder(value int) int {
	if value < 0 {
		return 0
	}
	if value == 0 {
		return 100
	}
	return value
}

func normalizeManagedModelIcon(icon, provider string) string {
	icon = strings.ToLower(strings.TrimSpace(icon))
	if icon != "" {
		return icon
	}
	switch normalizeSourceProvider(provider) {
	case "openai":
		return "sparkles"
	case "anthropic":
		return "brain"
	case "google", "gemini":
		return "gem"
	case "deepseek":
		return "bot"
	case "siliconflow":
		return "orbit"
	case "openrouter":
		return "route"
	default:
		return "cpu"
	}
}

func normalizeLimit(limit int) int {
	if limit <= 0 {
		return 20
	}
	if limit > 100 {
		return 100
	}
	return limit
}

func normalizeOffset(offset int) int {
	if offset < 0 {
		return 0
	}
	return offset
}
