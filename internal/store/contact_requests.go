package store

import (
	"context"
	"errors"
	"strings"

	"gorm.io/gorm"
)

type CreateContactRequestInput struct {
	Name    string
	Email   string
	Company string
	Role    string
	Message string
	Source  string
}

type CreateContactNoteInput struct {
	ContactRequestID uint
	Author           string
	Body             string
}

func (s *SQLiteStore) CreateContactRequest(ctx context.Context, input CreateContactRequestInput) (*ContactRequest, error) {
	name := strings.TrimSpace(input.Name)
	email := strings.ToLower(strings.TrimSpace(input.Email))
	message := strings.TrimSpace(input.Message)
	if name == "" || email == "" || message == "" {
		return nil, errors.New("name, email and message are required")
	}

	request := &ContactRequest{
		Name:    name,
		Email:   email,
		Company: strings.TrimSpace(input.Company),
		Role:    strings.TrimSpace(input.Role),
		Message: message,
		Status:  ContactStatusNew,
		Source:  strings.TrimSpace(input.Source),
	}
	if request.Source == "" {
		request.Source = "website"
	}

	if err := s.gormDB.WithContext(ctx).Create(request).Error; err != nil {
		return nil, err
	}
	return request, nil
}

func normalizeContactStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case ContactStatusContacted:
		return ContactStatusContacted
	case ContactStatusQualified:
		return ContactStatusQualified
	case ContactStatusClosed:
		return ContactStatusClosed
	default:
		return ContactStatusNew
	}
}

func (s *SQLiteStore) ListContactRequests(ctx context.Context, options ListOptions, status string) ([]ContactRequest, int64, error) {
	query := s.gormDB.WithContext(ctx).Model(&ContactRequest{})
	status = strings.ToLower(strings.TrimSpace(status))
	if status != "" {
		query = query.Where("status = ?", normalizeContactStatus(status))
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var rows []ContactRequest
	if err := query.Order("id DESC").Limit(normalizeLimit(options.Limit)).Offset(normalizeOffset(options.Offset)).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

func (s *SQLiteStore) ContactRequestStatusSummary(ctx context.Context) (map[string]int64, error) {
	type summaryRow struct {
		Status string
		Total  int64
	}
	var rows []summaryRow
	if err := s.gormDB.WithContext(ctx).
		Model(&ContactRequest{}).
		Select("status, count(1) as total").
		Group("status").
		Find(&rows).Error; err != nil {
		return nil, err
	}

	out := map[string]int64{
		ContactStatusNew:       0,
		ContactStatusContacted: 0,
		ContactStatusQualified: 0,
		ContactStatusClosed:    0,
	}
	for _, row := range rows {
		out[normalizeContactStatus(row.Status)] = row.Total
	}
	return out, nil
}

func (s *SQLiteStore) UpdateContactRequestStatus(ctx context.Context, requestID uint, status string) (*ContactRequest, error) {
	var request ContactRequest
	if err := s.gormDB.WithContext(ctx).Where("id = ?", requestID).First(&request).Error; err != nil {
		return nil, err
	}

	request.Status = normalizeContactStatus(status)
	if err := s.gormDB.WithContext(ctx).Save(&request).Error; err != nil {
		return nil, err
	}
	return &request, nil
}

func (s *SQLiteStore) UpdateContactRequestOwner(ctx context.Context, requestID uint, owner string) (*ContactRequest, error) {
	var request ContactRequest
	if err := s.gormDB.WithContext(ctx).Where("id = ?", requestID).First(&request).Error; err != nil {
		return nil, err
	}

	request.Owner = strings.TrimSpace(owner)
	if err := s.gormDB.WithContext(ctx).Save(&request).Error; err != nil {
		return nil, err
	}
	return &request, nil
}

func (s *SQLiteStore) FindContactRequestByID(ctx context.Context, requestID uint) (*ContactRequest, error) {
	var request ContactRequest
	if err := s.gormDB.WithContext(ctx).Where("id = ?", requestID).First(&request).Error; err != nil {
		return nil, err
	}
	return &request, nil
}

func (s *SQLiteStore) ListContactNotesByRequestID(ctx context.Context, requestID uint) ([]ContactNote, error) {
	var rows []ContactNote
	if err := s.gormDB.WithContext(ctx).
		Where("contact_request_id = ?", requestID).
		Order("created_at ASC").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func (s *SQLiteStore) CreateContactNote(ctx context.Context, input CreateContactNoteInput) (*ContactNote, error) {
	author := strings.TrimSpace(input.Author)
	body := strings.TrimSpace(input.Body)
	if input.ContactRequestID == 0 || author == "" || body == "" {
		return nil, errors.New("contact_request_id, author and body are required")
	}

	note := &ContactNote{
		ContactRequestID: input.ContactRequestID,
		Author:           author,
		Body:             body,
	}
	if err := s.gormDB.WithContext(ctx).Create(note).Error; err != nil {
		return nil, err
	}
	return note, nil
}

func (s *SQLiteStore) UpdateContactNote(ctx context.Context, noteID uint, body string) (*ContactNote, error) {
	var note ContactNote
	if err := s.gormDB.WithContext(ctx).Where("id = ?", noteID).First(&note).Error; err != nil {
		return nil, err
	}

	trimmedBody := strings.TrimSpace(body)
	if trimmedBody == "" {
		return nil, errors.New("note body is required")
	}
	note.Body = trimmedBody
	if err := s.gormDB.WithContext(ctx).Save(&note).Error; err != nil {
		return nil, err
	}
	return &note, nil
}

func (s *SQLiteStore) DeleteContactNote(ctx context.Context, noteID uint) error {
	return s.gormDB.WithContext(ctx).Where("id = ?", noteID).Delete(&ContactNote{}).Error
}

func IsContactRequestNotFound(err error) bool {
	return errors.Is(err, gorm.ErrRecordNotFound)
}
