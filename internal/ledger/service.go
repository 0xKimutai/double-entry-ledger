package ledger

import (
	"context"
	"strings"
)

type Service struct {
	repository Repository
}

func NewService(repository Repository) *Service {
	return &Service{repository: repository}
}

func (s *Service) CreateAccount(ctx context.Context, name, currency string) (Account, error) {
	name = strings.TrimSpace(name)
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if len(name) == 0 || len(name) > 120 || !validCurrency(currency) {
		return Account{}, ErrInvalidInput
	}
	return s.repository.CreateAccount(ctx, name, currency)
}

func (s *Service) GetAccount(ctx context.Context, id string) (Account, error) {
	if !validID(id) {
		return Account{}, ErrNotFound
	}
	return s.repository.GetAccount(ctx, strings.ToLower(id))
}

func (s *Service) ListEntries(ctx context.Context, accountID string, page Page) ([]Entry, error) {
	if !validID(accountID) {
		return nil, ErrNotFound
	}
	if page.Limit < 1 || page.Limit > 100 || page.Offset < 0 {
		return nil, ErrInvalidInput
	}
	return s.repository.ListEntries(ctx, strings.ToLower(accountID), page)
}

func (s *Service) Deposit(ctx context.Context, accountID, idempotencyKey string, amountMinor int64) (Transfer, bool, error) {
	if !validID(accountID) {
		return Transfer{}, false, ErrNotFound
	}
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if !validIdempotencyKey(idempotencyKey) || amountMinor <= 0 {
		return Transfer{}, false, ErrInvalidInput
	}
	return s.repository.Deposit(ctx, strings.ToLower(accountID), idempotencyKey, amountMinor)
}

func (s *Service) Transfer(ctx context.Context, sourceAccountID, destinationAccountID, idempotencyKey string, amountMinor int64) (Transfer, bool, error) {
	if !validID(sourceAccountID) || !validID(destinationAccountID) {
		return Transfer{}, false, ErrNotFound
	}
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if !validIdempotencyKey(idempotencyKey) || amountMinor <= 0 || sourceAccountID == destinationAccountID {
		return Transfer{}, false, ErrInvalidInput
	}
	return s.repository.Transfer(ctx, strings.ToLower(sourceAccountID), strings.ToLower(destinationAccountID), idempotencyKey, amountMinor)
}

func validCurrency(currency string) bool {
	if len(currency) != 3 {
		return false
	}
	for _, char := range currency {
		if char < 'A' || char > 'Z' {
			return false
		}
	}
	return true
}

func validID(id string) bool {
	if len(id) != 36 {
		return false
	}
	for i, char := range id {
		switch i {
		case 8, 13, 18, 23:
			if char != '-' {
				return false
			}
		default:
			if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f') || (char >= 'A' && char <= 'F')) {
				return false
			}
		}
	}
	return true
}

func validIdempotencyKey(key string) bool {
	return len(key) > 0 && len(key) <= 255
}
