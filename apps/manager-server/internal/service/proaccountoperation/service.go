package proaccountoperation

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/proaccountdraft"
)

const (
	defaultCleanupTTL = 30 * time.Minute
	maxCleanupTTL     = 24 * time.Hour
	maxContextBytes   = 64 * 1024
)

var (
	ErrInvalidOperation         = errors.New("invalid pro account operation")
	ErrSensitiveOperationData   = errors.New("operation context contains sensitive data")
	ErrInvalidStateTransition   = errors.New("invalid pro account operation state transition")
	ErrOperationNotFound        = proaccountdraft.ErrNotFound
	ErrOperationVersionConflict = proaccountdraft.ErrVersionConflict
)

type StartInput struct {
	OperationID       string
	IdempotencyKey    string
	OperationType     string
	ProAccountID      string
	CleanupDeadlineMS int64
	Context           map[string]any
}

type TransitionInput struct {
	ExpectedVersion    int64
	ProAccountID       string
	State              string
	RetryCount         int
	CleanupDeadlineMS  int64
	CompensationAction string
	ErrorCode          string
	ErrorSummary       string
	Context            map[string]any
}

type Service struct {
	repository proaccountdraft.Repository
	now        func() time.Time
}

func New(repository proaccountdraft.Repository) *Service {
	return &Service{repository: repository, now: time.Now}
}

func (s *Service) Start(ctx context.Context, input StartInput) (model.ProAccountDraft, bool, error) {
	input.OperationID = strings.TrimSpace(input.OperationID)
	if input.OperationID == "" {
		generated, err := newOperationID()
		if err != nil {
			return model.ProAccountDraft{}, false, err
		}
		input.OperationID = generated
	}
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	input.OperationType = strings.ToLower(strings.TrimSpace(input.OperationType))
	input.ProAccountID = strings.TrimSpace(input.ProAccountID)
	if !validIdentifier(input.OperationID, 128) || !validIdentifier(input.IdempotencyKey, 256) || !validOperationType(input.OperationType) {
		return model.ProAccountDraft{}, false, ErrInvalidOperation
	}
	if err := validateOperationContext(input.Context); err != nil {
		return model.ProAccountDraft{}, false, err
	}
	now := s.now()
	deadline := input.CleanupDeadlineMS
	if deadline <= now.UnixMilli() {
		deadline = now.Add(defaultCleanupTTL).UnixMilli()
	}
	if deadline > now.Add(maxCleanupTTL).UnixMilli() {
		return model.ProAccountDraft{}, false, fmt.Errorf("%w: cleanup deadline exceeds 24 hours", ErrInvalidOperation)
	}
	return s.repository.Create(ctx, model.ProAccountDraftCreate{
		OperationID: input.OperationID, IdempotencyKey: input.IdempotencyKey,
		OperationType: input.OperationType, ProAccountID: input.ProAccountID,
		CleanupDeadlineMS: deadline, Context: cloneContext(input.Context),
	}, now.UnixMilli())
}

func (s *Service) Get(ctx context.Context, operationID string) (model.ProAccountDraft, error) {
	item, ok, err := s.repository.Get(ctx, strings.TrimSpace(operationID))
	if err != nil {
		return model.ProAccountDraft{}, err
	}
	if !ok {
		return model.ProAccountDraft{}, ErrOperationNotFound
	}
	return item, nil
}

func (s *Service) Transition(ctx context.Context, operationID string, input TransitionInput) (model.ProAccountDraft, error) {
	current, err := s.Get(ctx, operationID)
	if err != nil {
		return model.ProAccountDraft{}, err
	}
	if input.ExpectedVersion != current.Version {
		return model.ProAccountDraft{}, ErrOperationVersionConflict
	}
	input.State = strings.ToLower(strings.TrimSpace(input.State))
	if !canTransition(current.State, input.State) {
		return model.ProAccountDraft{}, fmt.Errorf("%w: %s -> %s", ErrInvalidStateTransition, current.State, input.State)
	}
	if input.RetryCount < current.RetryCount || input.RetryCount > current.RetryCount+1 {
		return model.ProAccountDraft{}, fmt.Errorf("%w: retry count must stay unchanged or increment by one", ErrInvalidOperation)
	}
	if err := validateOperationContext(input.Context); err != nil {
		return model.ProAccountDraft{}, err
	}
	deadline := input.CleanupDeadlineMS
	if deadline <= 0 {
		deadline = current.CleanupDeadlineMS
	}
	contextValue := input.Context
	if contextValue == nil {
		contextValue = current.Context
	}
	return s.repository.Update(ctx, current.OperationID, current.Version, model.ProAccountDraftUpdate{
		ProAccountID: strings.TrimSpace(input.ProAccountID),
		State:        input.State, RetryCount: input.RetryCount, CleanupDeadlineMS: deadline,
		CompensationAction: strings.TrimSpace(input.CompensationAction),
		ErrorCode:          strings.TrimSpace(input.ErrorCode),
		ErrorSummary:       truncate(strings.TrimSpace(input.ErrorSummary), 512),
		Context:            cloneContext(contextValue),
	}, s.now().UnixMilli())
}

func (s *Service) ListRecoverable(ctx context.Context, limit int) ([]model.ProAccountDraft, error) {
	return s.repository.ListRecoverable(ctx, s.now().UnixMilli(), limit)
}

func canTransition(from string, to string) bool {
	from = strings.ToLower(strings.TrimSpace(from))
	to = strings.ToLower(strings.TrimSpace(to))
	if from == "" || to == "" || from == to {
		return false
	}
	if from == model.ProOperationStateEnabled || from == model.ProOperationStateCancelled || from == model.ProOperationStateFailed {
		return false
	}
	if to == model.ProOperationStateCancelled || to == model.ProOperationStateCompensating || to == model.ProOperationStateFailed {
		return true
	}
	allowed := map[string]map[string]bool{
		model.ProOperationStateDraftCreated: {
			model.ProOperationStateCredentialSavedDisabled: true,
			model.ProOperationStateProbed:                  true,
			model.ProOperationStateModelsConfigured:        true,
			model.ProOperationStateTested:                  true,
		},
		model.ProOperationStateProbed: {
			model.ProOperationStateCredentialSavedDisabled: true,
			model.ProOperationStateModelsConfigured:        true,
		},
		model.ProOperationStateCredentialSavedDisabled: {
			model.ProOperationStateProbed:           true,
			model.ProOperationStateModelsConfigured: true,
			model.ProOperationStateTested:           true,
		},
		model.ProOperationStateModelsConfigured: {
			model.ProOperationStateTested:  true,
			model.ProOperationStateEnabled: true,
		},
		model.ProOperationStateTested: {
			model.ProOperationStateEnabled: true,
		},
		model.ProOperationStateCompensating: {
			model.ProOperationStateEnabled: true,
		},
	}
	return allowed[from][to]
}

func validOperationType(value string) bool {
	switch value {
	case "add", "edit", "model_update", "test", "enable", "disable", "delete", "migrate", "rebind", "reset":
		return true
	default:
		return false
	}
}

func validIdentifier(value string, maxLength int) bool {
	if value == "" || len(value) > maxLength {
		return false
	}
	for _, current := range value {
		if unicode.IsLetter(current) || unicode.IsDigit(current) {
			continue
		}
		switch current {
		case '-', '_', '.', ':':
			continue
		default:
			return false
		}
	}
	return true
}

func validateOperationContext(value map[string]any) error {
	if value == nil {
		return nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("%w: invalid context", ErrInvalidOperation)
	}
	if len(raw) > maxContextBytes {
		return fmt.Errorf("%w: context exceeds %d bytes", ErrInvalidOperation, maxContextBytes)
	}
	// 先通过 JSON 归一化嵌套 map，避免 map[string]string 等具体类型绕过敏感字段检查。
	var normalized any
	if err := json.Unmarshal(raw, &normalized); err != nil {
		return fmt.Errorf("%w: invalid context", ErrInvalidOperation)
	}
	if containsSensitiveKey(normalized) {
		return ErrSensitiveOperationData
	}
	return nil
}

func containsSensitiveKey(value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			normalized := strings.NewReplacer("-", "", "_", "", ".", "").Replace(strings.ToLower(strings.TrimSpace(key)))
			for _, blocked := range []string{"apikey", "token", "secret", "authorization", "cookie", "password", "privatekey", "credential"} {
				if strings.Contains(normalized, blocked) {
					return true
				}
			}
			if containsSensitiveKey(child) {
				return true
			}
		}
	case []any:
		for _, child := range typed {
			if containsSensitiveKey(child) {
				return true
			}
		}
	case []map[string]any:
		for _, child := range typed {
			if containsSensitiveKey(child) {
				return true
			}
		}
	}
	return false
}

func cloneContext(value map[string]any) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return map[string]any{}
	}
	result := map[string]any{}
	if err := json.Unmarshal(raw, &result); err != nil {
		return map[string]any{}
	}
	return result
}

func newOperationID() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	return fmt.Sprintf("%s-%s-%s-%s-%s",
		hex.EncodeToString(raw[0:4]), hex.EncodeToString(raw[4:6]), hex.EncodeToString(raw[6:8]),
		hex.EncodeToString(raw[8:10]), hex.EncodeToString(raw[10:16])), nil
}

func truncate(value string, limit int) string {
	if limit <= 0 || len(value) <= limit {
		return value
	}
	return value[:limit]
}
